package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

type DataImportItem struct {
	Path    string `json:"path"`
	Status  string `json:"status"`
	ImageID string `json:"image_id,omitempty"`
	Message string `json:"message,omitempty"`
}

type DataImportReport struct {
	Root     string           `json:"root"`
	Scanned  int              `json:"scanned"`
	Imported int              `json:"imported"`
	Skipped  int              `json:"skipped"`
	Failed   int              `json:"failed"`
	Items    []DataImportItem `json:"items"`
}

type DataExportReport struct {
	Path          string `json:"path"`
	Images        int    `json:"images"`
	Variants      int    `json:"variants"`
	ExportedBytes int64  `json:"exported_bytes"`
	Skipped       int    `json:"skipped"`
}

type PortableExport struct {
	FormatVersion      int                  `json:"format_version"`
	ApplicationVersion string               `json:"application_version"`
	CreatedAt          string               `json:"created_at"`
	Settings           Settings             `json:"settings"`
	Storages           []PortableStorage    `json:"storages"`
	Images             []PortableImage      `json:"images"`
	Tags               []Tag                `json:"tags"`
	ImageTags          []PortableImageTag   `json:"image_tags"`
	Albums             []Album              `json:"albums"`
	AlbumImages        []PortableAlbumImage `json:"album_images"`
}

type PortableStorage struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
}

type PortableImage struct {
	ID           string            `json:"id"`
	Hash         string            `json:"hash"`
	OriginalName string            `json:"original_name"`
	MIMEType     string            `json:"mime_type"`
	Size         int64             `json:"size"`
	Width        int               `json:"width"`
	Height       int               `json:"height"`
	StorageID    string            `json:"storage_id"`
	Favorite     bool              `json:"favorite"`
	DeletedAt    string            `json:"deleted_at,omitempty"`
	CreatedAt    string            `json:"created_at"`
	Object       PortableObject    `json:"object"`
	Variants     []PortableVariant `json:"variants,omitempty"`
}

type PortableObject struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type PortableVariant struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	MIMEType  string         `json:"mime_type"`
	Size      int64          `json:"size"`
	Width     int            `json:"width"`
	Height    int            `json:"height"`
	CreatedAt string         `json:"created_at"`
	Object    PortableObject `json:"object"`
}

type PortableImageTag struct {
	ImageID string `json:"image_id"`
	TagID   string `json:"tag_id"`
}

type PortableAlbumImage struct {
	AlbumID  string `json:"album_id"`
	ImageID  string `json:"image_id"`
	Position int    `json:"position"`
	AddedAt  string `json:"added_at"`
}

// ImportDirectory ingests image files from a local directory through the same
// validation and processing pipeline as browser uploads. It never deletes or
// modifies source files.
func (a *App) ImportDirectory(ctx context.Context, root, storageID string, recursive bool, limit int) (DataImportReport, error) {
	root, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return DataImportReport{}, err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("路径不是目录")
		}
		return DataImportReport{}, err
	}
	if limit < 0 || limit > 100000 {
		return DataImportReport{}, errors.New("导入数量必须在 0 到 100000 之间")
	}
	report := DataImportReport{Root: root, Items: make([]DataImportItem, 0)}
	err = filepath.Walk(root, func(filename string, fileInfo os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if filename == root {
			return nil
		}
		if fileInfo.IsDir() {
			if !recursive && filename != root {
				return filepath.SkipDir
			}
			return nil
		}
		if fileInfo.Mode()&os.ModeSymlink != 0 || !fileInfo.Mode().IsRegular() {
			report.Skipped++
			return nil
		}
		if limit > 0 && report.Scanned >= limit {
			return filepath.SkipAll
		}
		if isPortableVariantPath(root, filename) {
			report.Skipped++
			return nil
		}
		file, openErr := os.Open(filename)
		if openErr != nil {
			report.Failed++
			report.Items = append(report.Items, DataImportItem{Path: filename, Status: "failed", Message: openErr.Error()})
			report.Scanned++
			return nil
		}
		header := make([]byte, 512)
		n, readErr := io.ReadFull(file, header)
		_ = file.Close()
		if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
			report.Skipped++
			return nil
		}
		if detectImageMIME(header[:n]) == "" {
			report.Skipped++
			return nil
		}
		report.Scanned++
		file, openErr = os.Open(filename)
		if openErr != nil {
			report.Failed++
			report.Items = append(report.Items, DataImportItem{Path: filename, Status: "failed", Message: openErr.Error()})
			return nil
		}
		image, importErr := a.ingestImage(ctx, file, filepath.Base(filename), fileInfo.Size(), storageID)
		_ = file.Close()
		if importErr != nil {
			report.Failed++
			report.Items = append(report.Items, DataImportItem{Path: filename, Status: "failed", Message: importErr.Error()})
			return nil
		}
		report.Imported++
		report.Items = append(report.Items, DataImportItem{Path: filename, Status: "imported", ImageID: image.ID})
		return nil
	})
	if err != nil && !errors.Is(err, filepath.SkipAll) {
		return report, err
	}
	return report, nil
}

func isPortableVariantPath(root, filename string) bool {
	rel, err := filepath.Rel(root, filename)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return len(parts) >= 3 && parts[0] == "objects" && !strings.HasPrefix(strings.ToLower(parts[len(parts)-1]), "original.")
}

// ExportData creates a self-contained directory containing metadata and the
// actual original/variant bytes fetched from every configured storage. The
// directory is built beside the destination and renamed only after success.
func (a *App) ExportData(ctx context.Context, output string, includeTrash bool) (DataExportReport, error) {
	output, err := filepath.Abs(strings.TrimSpace(output))
	if err != nil {
		return DataExportReport{}, err
	}
	if _, err := os.Stat(output); err == nil {
		return DataExportReport{}, errors.New("导出目标已存在，请指定一个新目录")
	} else if !os.IsNotExist(err) {
		return DataExportReport{}, err
	}
	parent := filepath.Dir(output)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return DataExportReport{}, err
	}
	stage, err := os.MkdirTemp(parent, ".feather-export-*")
	if err != nil {
		return DataExportReport{}, err
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(stage)
		}
	}()
	settings, err := loadSettings(ctx, a.db)
	if err != nil {
		return DataExportReport{}, err
	}
	export := PortableExport{FormatVersion: 1, ApplicationVersion: a.cfg.Version, CreatedAt: nowUTC(), Settings: settings}
	if err := a.collectPortableMetadata(ctx, &export, includeTrash); err != nil {
		return DataExportReport{}, err
	}
	backends := make(map[string]storageBackend)
	for imageIndex := range export.Images {
		image := &export.Images[imageIndex]
		if _, ok := backends[image.StorageID]; !ok {
			record, recordErr := a.storageRecord(ctx, image.StorageID)
			if recordErr != nil {
				return DataExportReport{}, recordErr
			}
			backend, backendErr := a.backend(record)
			if backendErr != nil {
				return DataExportReport{}, backendErr
			}
			backends[image.StorageID] = backend
		}
		backend := backends[image.StorageID]
		originalPath, actualPath, pathErr := portableExportPath(stage, image.ID, "original"+portableExtension(image.MIMEType))
		if pathErr != nil {
			return DataExportReport{}, fmt.Errorf("图片 %s 的导出路径无效: %w", image.ID, pathErr)
		}
		object, copyErr := exportObject(ctx, backend, image.Object.ObjectPath(), actualPath, image.Size)
		if copyErr != nil {
			return DataExportReport{}, fmt.Errorf("导出图片 %s 失败: %w", image.ID, copyErr)
		}
		image.Object.Path, image.Object.SHA256 = originalPath, object.hash
		for variantIndex := range image.Variants {
			variant := &image.Variants[variantIndex]
			variantPath, actualVariantPath, pathErr := portableExportPath(stage, image.ID, variant.Kind+portableExtension(variant.MIMEType))
			if pathErr != nil {
				return DataExportReport{}, fmt.Errorf("图片 %s 的 %s 派生图导出路径无效: %w", image.ID, variant.Kind, pathErr)
			}
			variantObject, variantErr := exportObject(ctx, backend, variant.ObjectPath(), actualVariantPath, variant.Size)
			if variantErr != nil {
				return DataExportReport{}, fmt.Errorf("导出图片 %s 的 %s 派生图失败: %w", image.ID, variant.Kind, variantErr)
			}
			variant.Object.Path, variant.Object.SHA256 = variantPath, variantObject.hash
		}
	}
	metadataPath := filepath.Join(stage, "metadata.json")
	metadata, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return DataExportReport{}, err
	}
	if err := os.WriteFile(metadataPath, metadata, 0600); err != nil {
		return DataExportReport{}, err
	}
	if err := os.WriteFile(filepath.Join(stage, "README.txt"), []byte("Feather-ImgBed 数据导出\n\nmetadata.json 包含图片、标签、相册、关联关系和对象校验信息；objects/ 包含原图与派生图。\n可使用 data import-dir <本目录> 重新导入原图；当前版本不会自动恢复原 ID、标签和相册关系。\n"), 0600); err != nil {
		return DataExportReport{}, err
	}
	if err := os.Rename(stage, output); err != nil {
		return DataExportReport{}, err
	}
	keepStage = true
	var exportedBytes int64
	_ = filepath.Walk(output, func(_ string, info os.FileInfo, walkErr error) error {
		if walkErr == nil && info != nil && info.Mode().IsRegular() {
			exportedBytes += info.Size()
		}
		return nil
	})
	variants := 0
	for _, image := range export.Images {
		variants += len(image.Variants)
	}
	return DataExportReport{Path: output, Images: len(export.Images), Variants: variants, ExportedBytes: exportedBytes, Skipped: 0}, nil
}

type exportedObject struct{ hash string }

func (a PortableObject) ObjectPath() string  { return a.Path }
func (a PortableVariant) ObjectPath() string { return a.Object.Path }

func exportObject(ctx context.Context, backend storageBackend, key, target string, expected int64) (exportedObject, error) {
	if expected < 0 {
		return exportedObject{}, errors.New("对象大小不能为负数")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return exportedObject{}, err
	}
	reader, err := backend.Open(ctx, key)
	if err != nil {
		return exportedObject{}, err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		_ = reader.Close()
		return exportedObject{}, err
	}
	hash := sha256.New()
	written, copyErr := copyWithContext(ctx, io.MultiWriter(file, hash), io.LimitReader(reader, expected+1))
	readerCloseErr := reader.Close()
	closeErr := file.Close()
	if copyErr == nil {
		copyErr = readerCloseErr
	}
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(target)
		return exportedObject{}, copyErr
	}
	if written != expected {
		_ = os.Remove(target)
		return exportedObject{}, fmt.Errorf("对象大小不一致：期望 %d，读取 %d", expected, written)
	}
	return exportedObject{hash: hex.EncodeToString(hash.Sum(nil))}, nil
}

func portableExtension(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/avif":
		return ".avif"
	default:
		ext, _ := mime.ExtensionsByType(mimeType)
		if len(ext) > 0 {
			return ext[0]
		}
		return ".bin"
	}
}

func portableExportPath(stage, imageID, filename string) (string, string, error) {
	for _, component := range []string{imageID, filename} {
		if component == "" || component == "." || component == ".." || strings.ContainsAny(component, `/\\`) || strings.IndexByte(component, 0) >= 0 {
			return "", "", errors.New("对象路径包含不安全的目录分量")
		}
	}
	relative := filepath.Join("objects", imageID, filename)
	target := filepath.Join(stage, relative)
	if !pathWithin(stage, target) {
		return "", "", errors.New("对象路径越出导出目录")
	}
	return filepath.ToSlash(relative), target, nil
}

func (a *App) collectPortableMetadata(ctx context.Context, export *PortableExport, includeTrash bool) error {
	rows, err := a.db.QueryContext(ctx, "SELECT id,name,type,enabled FROM storages ORDER BY created_at")
	if err != nil {
		return err
	}
	for rows.Next() {
		var item PortableStorage
		var enabled int
		if err := rows.Scan(&item.ID, &item.Name, &item.Type, &enabled); err != nil {
			_ = rows.Close()
			return err
		}
		item.Enabled = enabled == 1
		export.Storages = append(export.Storages, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	query := `SELECT id,hash,original_name,mime_type,size,width,height,storage_id,favorite,COALESCE(deleted_at,''),created_at,object_key FROM images`
	if !includeTrash {
		query += " WHERE deleted_at IS NULL"
	}
	query += " ORDER BY created_at,id"
	rows, err = a.db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	imageIDs := make(map[string]struct{})
	for rows.Next() {
		var item PortableImage
		var width, height sql.NullInt64
		var favorite int
		var objectKey string
		if err := rows.Scan(&item.ID, &item.Hash, &item.OriginalName, &item.MIMEType, &item.Size, &width, &height, &item.StorageID, &favorite, &item.DeletedAt, &item.CreatedAt, &objectKey); err != nil {
			_ = rows.Close()
			return err
		}
		if width.Valid {
			item.Width = int(width.Int64)
		}
		if height.Valid {
			item.Height = int(height.Int64)
		}
		item.Favorite = favorite == 1
		item.Object = PortableObject{Path: objectKey}
		export.Images = append(export.Images, item)
		imageIDs[item.ID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for index := range export.Images {
		variantRows, err := a.db.QueryContext(ctx, "SELECT id,kind,mime_type,size,width,height,created_at,object_key FROM image_variants WHERE image_id=? ORDER BY kind", export.Images[index].ID)
		if err != nil {
			return err
		}
		for variantRows.Next() {
			var variant PortableVariant
			var width, height int
			var objectKey string
			if err := variantRows.Scan(&variant.ID, &variant.Kind, &variant.MIMEType, &variant.Size, &width, &height, &variant.CreatedAt, &objectKey); err != nil {
				_ = variantRows.Close()
				return err
			}
			variant.Width, variant.Height, variant.Object = width, height, PortableObject{Path: objectKey}
			export.Images[index].Variants = append(export.Images[index].Variants, variant)
		}
		if err := variantRows.Err(); err != nil {
			_ = variantRows.Close()
			return err
		}
		_ = variantRows.Close()
	}
	tags, err := queryTags(ctx, a.db, "")
	if err != nil {
		return err
	}
	export.Tags = tags
	tagCounts := make(map[string]int)
	rows, err = a.db.QueryContext(ctx, "SELECT image_id,tag_id FROM image_tags")
	if err != nil {
		return err
	}
	for rows.Next() {
		var relation PortableImageTag
		if err := rows.Scan(&relation.ImageID, &relation.TagID); err != nil {
			_ = rows.Close()
			return err
		}
		if _, ok := imageIDs[relation.ImageID]; ok {
			export.ImageTags = append(export.ImageTags, relation)
			tagCounts[relation.TagID]++
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for index := range export.Tags {
		export.Tags[index].ImageCount = tagCounts[export.Tags[index].ID]
	}
	albums, err := queryAlbumsForExport(ctx, a.db)
	if err != nil {
		return err
	}
	export.Albums = albums
	for index := range export.Albums {
		if export.Albums[index].CoverImageID != "" {
			if _, ok := imageIDs[export.Albums[index].CoverImageID]; !ok {
				export.Albums[index].CoverImageID = ""
			}
		}
	}
	albumCounts := make(map[string]int)
	rows, err = a.db.QueryContext(ctx, "SELECT album_id,image_id,position,added_at FROM album_images ORDER BY album_id,position,added_at")
	if err != nil {
		return err
	}
	for rows.Next() {
		var relation PortableAlbumImage
		if err := rows.Scan(&relation.AlbumID, &relation.ImageID, &relation.Position, &relation.AddedAt); err != nil {
			_ = rows.Close()
			return err
		}
		if _, ok := imageIDs[relation.ImageID]; ok {
			export.AlbumImages = append(export.AlbumImages, relation)
			albumCounts[relation.AlbumID]++
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for index := range export.Albums {
		export.Albums[index].ImageCount = albumCounts[export.Albums[index].ID]
	}
	return nil
}

func queryAlbumsForExport(ctx context.Context, db *sql.DB) ([]Album, error) {
	rows, err := db.QueryContext(ctx, `SELECT a.id,a.name,a.description,COALESCE(a.cover_image_id,''),COALESCE((SELECT COUNT(*) FROM album_images ai WHERE ai.album_id=a.id),0),a.created_at,a.updated_at FROM albums a ORDER BY a.created_at,a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Album, 0)
	for rows.Next() {
		var item Album
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.CoverImageID, &item.ImageCount, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
