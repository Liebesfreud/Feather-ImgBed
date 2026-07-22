package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

type StorageMigrationItem struct {
	ImageID      string `json:"image_id"`
	Status       string `json:"status"`
	ObjectsMoved int    `json:"objects_moved"`
	Message      string `json:"message,omitempty"`
}

type StorageMigrationReport struct {
	SourceID string                 `json:"source_id"`
	TargetID string                 `json:"target_id"`
	Scanned  int                    `json:"scanned"`
	Migrated int                    `json:"migrated"`
	Failed   int                    `json:"failed"`
	DryRun   bool                   `json:"dry_run"`
	Items    []StorageMigrationItem `json:"items"`
}

type migrationImage struct {
	ID, ObjectKey, StorageID, MIMEType string
	Size                               int64
	Variants                           []migrationVariant
}

type migrationVariant struct {
	ID, Kind, ObjectKey, MIMEType string
	Size                          int64
}

type migrationObject struct {
	kind, id, oldKey, mime string
	size                   int64
}

// MigrateStorage moves image objects and their variants to another configured
// storage. Database references are switched only after every target object is
// uploaded; failed cleanup leaves a recoverable orphan and is reported.
func (a *App) MigrateStorage(ctx context.Context, sourceID, targetID string, limit int, includeTrash, dryRun bool) (StorageMigrationReport, error) {
	sourceID, targetID = strings.TrimSpace(sourceID), strings.TrimSpace(targetID)
	if sourceID == "" || targetID == "" || sourceID == targetID {
		return StorageMigrationReport{}, errors.New("源存储和目标存储必须是两个不同的存储")
	}
	if limit < 0 || limit > 100000 {
		return StorageMigrationReport{}, errors.New("迁移数量必须在 0 到 100000 之间")
	}
	source, err := a.storageRecord(ctx, sourceID)
	if err != nil {
		return StorageMigrationReport{}, fmt.Errorf("读取源存储失败: %w", err)
	}
	target, err := a.storageRecord(ctx, targetID)
	if err != nil {
		return StorageMigrationReport{}, fmt.Errorf("读取目标存储失败: %w", err)
	}
	if !target.Enabled {
		return StorageMigrationReport{}, errors.New("目标存储未启用")
	}
	sourceBackend, err := a.backend(source)
	if err != nil {
		return StorageMigrationReport{}, fmt.Errorf("初始化源存储失败: %w", err)
	}
	targetBackend, err := a.backend(target)
	if err != nil {
		return StorageMigrationReport{}, fmt.Errorf("初始化目标存储失败: %w", err)
	}
	settings, err := loadSettings(ctx, a.db)
	if err != nil {
		return StorageMigrationReport{}, err
	}
	images, err := a.migrationImages(ctx, sourceID, limit, includeTrash)
	if err != nil {
		return StorageMigrationReport{}, err
	}
	report := StorageMigrationReport{SourceID: sourceID, TargetID: targetID, Scanned: len(images), DryRun: dryRun, Items: make([]StorageMigrationItem, 0, len(images))}
	for _, image := range images {
		item := StorageMigrationItem{ImageID: image.ID, Status: "pending"}
		objects := make([]migrationObject, 0, len(image.Variants)+1)
		objects = append(objects, migrationObject{kind: "original", id: image.ID, oldKey: image.ObjectKey, mime: image.MIMEType, size: image.Size})
		for _, variant := range image.Variants {
			objects = append(objects, migrationObject{kind: variant.Kind, id: variant.ID, oldKey: variant.ObjectKey, mime: variant.MIMEType, size: variant.Size})
		}
		if dryRun {
			item.Status, item.ObjectsMoved = "planned", len(objects)
			report.Migrated++
			report.Items = append(report.Items, item)
			continue
		}
		moved, newKeys, moveErr := a.copyMigrationObjects(ctx, sourceBackend, targetBackend, image.ID, objects)
		item.ObjectsMoved = moved
		if moveErr != nil {
			item.Status, item.Message = "failed", moveErr.Error()
			report.Failed++
			report.Items = append(report.Items, item)
			continue
		}
		if err := a.commitMigrationImage(ctx, image, target, settings.SiteURL, objects, newKeys); err != nil {
			cleanupMessage := cleanupMigrationObjects(targetBackend, newKeys)
			item.Status, item.Message = "failed", "更新数据库失败: "+err.Error()
			if cleanupMessage != "" {
				item.Message += "; 目标对象清理失败: " + cleanupMessage
			}
			report.Failed++
			report.Items = append(report.Items, item)
			continue
		}
		cleanupMessage := ""
		for _, object := range objects {
			deleteCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			deleteErr := sourceBackend.Delete(deleteCtx, object.oldKey)
			cancel()
			if deleteErr != nil {
				if cleanupMessage != "" {
					cleanupMessage += "; "
				}
				cleanupMessage += object.oldKey + ": " + deleteErr.Error()
			}
		}
		item.Status = "migrated"
		if cleanupMessage != "" {
			item.Status = "migrated_with_warning"
			item.Message = "旧对象清理失败: " + cleanupMessage
		}
		report.Migrated++
		report.Items = append(report.Items, item)
	}
	return report, nil
}

func (a *App) migrationImages(ctx context.Context, storageID string, limit int, includeTrash bool) ([]migrationImage, error) {
	query := "SELECT id,object_key,storage_id,mime_type,size FROM images WHERE storage_id=?"
	args := []any{storageID}
	if !includeTrash {
		query += " AND deleted_at IS NULL"
	}
	query += " ORDER BY created_at,id"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	items := make([]migrationImage, 0)
	for rows.Next() {
		var item migrationImage
		if err := rows.Scan(&item.ID, &item.ObjectKey, &item.StorageID, &item.MIMEType, &item.Size); err != nil {
			_ = rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	for index := range items {
		variantRows, err := a.db.QueryContext(ctx, "SELECT id,kind,object_key,mime_type,size FROM image_variants WHERE image_id=? ORDER BY kind", items[index].ID)
		if err != nil {
			return nil, err
		}
		for variantRows.Next() {
			var variant migrationVariant
			if err := variantRows.Scan(&variant.ID, &variant.Kind, &variant.ObjectKey, &variant.MIMEType, &variant.Size); err != nil {
				_ = variantRows.Close()
				return nil, err
			}
			items[index].Variants = append(items[index].Variants, variant)
		}
		if err := variantRows.Err(); err != nil {
			_ = variantRows.Close()
			return nil, err
		}
		_ = variantRows.Close()
	}
	return items, nil
}

func (a *App) copyMigrationObjects(ctx context.Context, source, target storageBackend, imageID string, objects []migrationObject) (int, map[string]string, error) {
	newKeys := make(map[string]string, len(objects))
	moved := 0
	for _, object := range objects {
		readerCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		reader, err := source.Open(readerCtx, object.oldKey)
		if err != nil {
			cancel()
			return moved, newKeys, migrationCopyError(fmt.Errorf("读取 %s 对象失败: %w", object.oldKey, err), target, newKeys)
		}
		newKey, keyErr := migrationObjectKey(imageID, object.kind, object.oldKey)
		if keyErr != nil {
			_ = reader.Close()
			cancel()
			return moved, newKeys, migrationCopyError(keyErr, target, newKeys)
		}
		counter := &migrationByteCounter{}
		limited := io.TeeReader(io.LimitReader(reader, object.size+1), counter)
		storedKey, putErr := target.Put(readerCtx, newKey, limited, object.size, object.mime)
		closeErr := reader.Close()
		cancel()
		if putErr == nil && counter.total != object.size {
			putErr = fmt.Errorf("对象大小不一致：期望 %d，读取 %d", object.size, counter.total)
		}
		if putErr == nil {
			putErr = closeErr
		}
		if putErr != nil {
			if storedKey != "" {
				newKeys[object.id] = storedKey
			}
			return moved, newKeys, migrationCopyError(fmt.Errorf("写入目标对象 %s 失败: %w", newKey, putErr), target, newKeys)
		}
		newKeys[object.id] = storedKey
		moved++
	}
	return moved, newKeys, nil
}

func migrationObjectKey(imageID, kind, oldKey string) (string, error) {
	for _, component := range []string{imageID, kind} {
		if component == "" || component == "." || component == ".." || strings.ContainsAny(component, `/\\`) || strings.IndexByte(component, 0) >= 0 {
			return "", errors.New("迁移对象路径包含不安全的目录分量")
		}
	}
	extension := path.Ext(path.Base(oldKey))
	if len(extension) > 10 || strings.ContainsAny(extension, `/\\`) {
		extension = ".bin"
	}
	return path.Join("migrated", imageID, kind+"-"+newUUIDv7()+extension), nil
}

type migrationByteCounter struct{ total int64 }

func (c *migrationByteCounter) Write(value []byte) (int, error) {
	c.total += int64(len(value))
	return len(value), nil
}

func migrationCopyError(source error, backend storageBackend, keys map[string]string) error {
	if cleanupMessage := cleanupMigrationObjects(backend, keys); cleanupMessage != "" {
		return fmt.Errorf("%w; 目标对象清理失败: %s", source, cleanupMessage)
	}
	return source
}

func cleanupMigrationObjects(backend storageBackend, keys map[string]string) string {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var messages []string
	for _, key := range keys {
		if err := backend.Delete(cleanupCtx, key); err != nil {
			messages = append(messages, key+": "+err.Error())
		}
	}
	return strings.Join(messages, "; ")
}

func (a *App) commitMigrationImage(ctx context.Context, image migrationImage, target StorageRecord, siteURL string, objects []migrationObject, newKeys map[string]string) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	originalKey := newKeys[image.ID]
	if originalKey == "" {
		return errors.New("迁移结果缺少原图对象")
	}
	result, err := tx.ExecContext(ctx, "UPDATE images SET object_key=?,storage_type=?,storage_id=?,public_url=? WHERE id=? AND storage_id=?", originalKey, target.Type, target.ID, publicURL(target, originalKey, siteURL), image.ID, image.StorageID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err == nil {
			err = errors.New("图片存储已被其他操作修改")
		}
		return err
	}
	for _, object := range objects {
		if object.kind == "original" {
			continue
		}
		newKey := newKeys[object.id]
		if newKey == "" {
			return errors.New("迁移结果缺少派生对象")
		}
		result, err := tx.ExecContext(ctx, "UPDATE image_variants SET object_key=?,public_url=? WHERE id=? AND image_id=?", newKey, publicURL(target, newKey, siteURL), object.id, image.ID)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err == nil {
				err = errors.New("图片派生对象已被其他操作修改")
			}
			return err
		}
	}
	return tx.Commit()
}
