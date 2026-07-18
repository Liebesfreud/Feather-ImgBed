package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type ThumbnailRebuildItem struct {
	ImageID string `json:"image_id"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type ThumbnailRebuildReport struct {
	Total   int                    `json:"total"`
	Created int                    `json:"created"`
	Skipped int                    `json:"skipped"`
	Failed  int                    `json:"failed"`
	Items   []ThumbnailRebuildItem `json:"items"`
}

func (a *App) RebuildThumbnails(ctx context.Context) (ThumbnailRebuildReport, error) {
	report := ThumbnailRebuildReport{Items: make([]ThumbnailRebuildItem, 0)}
	rows, err := a.db.QueryContext(ctx, `SELECT i.id,i.object_key,i.storage_id,i.mime_type,
			COALESCE(v.object_key,'')
		FROM images i
		LEFT JOIN image_variants v ON v.image_id=i.id AND v.kind='thumbnail'
		WHERE i.deleted_at IS NULL
		  AND (v.id IS NULL OR v.mime_type!='image/webp' OR v.object_key NOT LIKE '%.webp')
		ORDER BY i.created_at,i.id`)
	if err != nil {
		return report, err
	}
	type candidate struct {
		id, objectKey, storageID, mimeType, oldThumbnailKey string
	}
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.objectKey, &item.storageID, &item.mimeType, &item.oldThumbnailKey); err != nil {
			_ = rows.Close()
			return report, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Close(); err != nil {
		return report, err
	}
	report.Total = len(candidates)

	for _, item := range candidates {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		result := ThumbnailRebuildItem{ImageID: item.id}
		record, err := a.storageRecord(ctx, item.storageID)
		if err != nil {
			result.Status, result.Message = "failed", "读取存储配置失败"
			report.Failed++
			report.Items = append(report.Items, result)
			continue
		}
		if record.Type == "telegram" && !strings.HasPrefix(item.objectKey, "v2/") {
			result.Status, result.Message = "skipped", "Telegram 旧记录缺少 file_id，无法回填"
			report.Skipped++
			report.Items = append(report.Items, result)
			continue
		}
		backend, err := a.backend(record)
		if err != nil {
			result.Status, result.Message = "failed", "创建存储后端失败"
			report.Failed++
			report.Items = append(report.Items, result)
			continue
		}
		if err := a.rebuildOneThumbnail(ctx, backend, record, item.id, item.objectKey, item.mimeType, item.oldThumbnailKey); err != nil {
			a.logger.Warn("缩略图回填失败", "image_id", item.id, "storage_id", item.storageID, "error", err)
			result.Status, result.Message = "failed", err.Error()
			report.Failed++
		} else {
			result.Status = "created"
			report.Created++
		}
		report.Items = append(report.Items, result)
	}
	return report, nil
}

func (a *App) rebuildOneThumbnail(
	ctx context.Context,
	backend storageBackend,
	record StorageRecord,
	imageID, objectKey, mimeType, oldThumbnailKey string,
) error {
	openCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	source, err := backend.Open(openCtx, objectKey)
	if err != nil {
		cancel()
		return fmt.Errorf("读取原图失败: %w", err)
	}
	temp, err := osCreateTemp(a.cfg.DataDir)
	if err != nil {
		_ = source.Close()
		cancel()
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	defer temp.Close()
	settings, settingsErr := loadSettings(ctx, a.db)
	if settingsErr != nil {
		_ = source.Close()
		cancel()
		return settingsErr
	}
	written, copyErr := io.Copy(temp, io.LimitReader(source, settings.MaxFileSize+1))
	closeErr := source.Close()
	cancel()
	if copyErr != nil {
		return fmt.Errorf("下载原图失败: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("关闭原图失败: %w", closeErr)
	}
	if written > settings.MaxFileSize {
		return fmt.Errorf("原图超过当前单文件大小限制")
	}
	generated, err := generateThumbnail(temp, mimeType, imageID)
	if err != nil {
		return fmt.Errorf("生成缩略图失败: %w", err)
	}
	putCtx, putCancel := context.WithTimeout(ctx, 45*time.Second)
	storedKey, err := backend.Put(putCtx, generated.ObjectKey, generated.Reader, generated.Size, generated.MIMEType)
	putCancel()
	if err != nil {
		return fmt.Errorf("写入缩略图失败: %w", err)
	}
	variant := ImageVariant{
		ID: newUUIDv7(), ImageID: imageID, Kind: "thumbnail", ObjectKey: storedKey,
		PublicURL: publicURL(record, storedKey, settings.SiteURL), MIMEType: generated.MIMEType, Size: generated.Size,
		Width: generated.Width, Height: generated.Height, CreatedAt: nowUTC(),
	}
	_, err = a.db.ExecContext(ctx, `INSERT INTO image_variants(
		id,image_id,kind,object_key,public_url,mime_type,size,width,height,created_at
	) VALUES(?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(image_id,kind) DO UPDATE SET
		object_key=excluded.object_key,
		public_url=excluded.public_url,
		mime_type=excluded.mime_type,
		size=excluded.size,
		width=excluded.width,
		height=excluded.height,
		created_at=excluded.created_at`,
		variant.ID, variant.ImageID, variant.Kind, variant.ObjectKey, variant.PublicURL,
		variant.MIMEType, variant.Size, variant.Width, variant.Height, variant.CreatedAt)
	if err == nil {
		if oldThumbnailKey != "" && oldThumbnailKey != storedKey {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cleanupCancel()
			if deleteErr := backend.Delete(cleanupCtx, oldThumbnailKey); deleteErr != nil {
				a.logger.Warn("旧缩略图清理失败", "image_id", imageID, "object_key", oldThumbnailKey, "error", deleteErr)
			}
		}
		return nil
	}
	rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer rollbackCancel()
	if deleteErr := backend.Delete(rollbackCtx, storedKey); deleteErr != nil {
		a.logger.Error("缩略图回填数据库失败且存储回滚失败", "image_id", imageID, "error", deleteErr)
	}
	return fmt.Errorf("保存缩略图记录失败: %w", err)
}
