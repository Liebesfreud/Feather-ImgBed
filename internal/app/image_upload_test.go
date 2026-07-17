package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

type recordingUploadStorage struct {
	mu             sync.Mutex
	putCount       int
	failPutAt      int
	putKeys        []string
	deleteKeys     []string
	deleteCtxError []error
	openContent    []byte
	openError      error
}

func (s *recordingUploadStorage) Put(_ context.Context, key string, reader io.Reader, _ int64, _ string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putCount++
	s.putKeys = append(s.putKeys, key)
	_, _ = io.Copy(io.Discard, reader)
	if s.failPutAt == s.putCount {
		return "", errors.New("simulated put failure")
	}
	return key, nil
}

func (s *recordingUploadStorage) Open(context.Context, string) (io.ReadCloser, error) {
	if s.openError != nil {
		return nil, s.openError
	}
	return io.NopCloser(bytes.NewReader(s.openContent)), nil
}

func (s *recordingUploadStorage) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteKeys = append(s.deleteKeys, key)
	s.deleteCtxError = append(s.deleteCtxError, ctx.Err())
	return nil
}

func (s *recordingUploadStorage) Test(context.Context) error { return nil }

func prepareIngestTest(t *testing.T) (*App, StorageRecord, Settings) {
	t.Helper()
	a := newTestApp(t)
	initializeTestApp(t, a.Handler())
	settings, err := loadSettings(context.Background(), a.db)
	if err != nil {
		t.Fatal(err)
	}
	record, err := a.storageRecord(context.Background(), settings.DefaultStorageID)
	if err != nil {
		t.Fatal(err)
	}
	return a, record, settings
}

func TestIngestHandlesOriginalAndThumbnailUploadFailures(t *testing.T) {
	content := pngBytes(t)
	tests := []struct {
		name          string
		failPutAt     int
		wantErrorCode string
		wantImage     bool
		wantVariant   bool
	}{
		{name: "原图写入失败", failPutAt: 1, wantErrorCode: "STORAGE_WRITE_FAILED"},
		{name: "缩略图写入失败时保留原图", failPutAt: 2, wantImage: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			a, _, _ := prepareIngestTest(t)
			storage := &recordingUploadStorage{failPutAt: test.failPutAt}
			a.backendFactory = func(StorageRecord) (storageBackend, error) { return storage, nil }

			img, err := a.ingestImage(context.Background(), bytes.NewReader(content), "test.png", int64(len(content)), "local")
			if test.wantErrorCode != "" {
				var apiErr *apiError
				if !errors.As(err, &apiErr) || apiErr.Code != test.wantErrorCode {
					t.Fatalf("错误为 %v，期望 %s", err, test.wantErrorCode)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var imageCount, variantCount int
			if err := a.db.QueryRow(`SELECT count(*) FROM images`).Scan(&imageCount); err != nil {
				t.Fatal(err)
			}
			if err := a.db.QueryRow(`SELECT count(*) FROM image_variants`).Scan(&variantCount); err != nil {
				t.Fatal(err)
			}
			if (imageCount == 1) != test.wantImage || (variantCount == 1) != test.wantVariant {
				t.Fatalf("记录数量错误: images=%d variants=%d", imageCount, variantCount)
			}
			if test.wantImage && (img.ID == "" || img.ThumbnailURL != "") {
				t.Fatalf("缩略图失败后的图片响应错误: %+v", img)
			}
		})
	}
}

func TestThumbnailGenerationFailureFallsBackToOriginal(t *testing.T) {
	a, _, _ := prepareIngestTest(t)
	complete := pngBytes(t)
	if len(complete) < 33 {
		t.Fatal("PNG 测试数据异常")
	}
	truncated := complete[:33]
	if _, _, err := image.DecodeConfig(bytes.NewReader(truncated)); err != nil {
		t.Fatalf("截断测试图应保留可读的尺寸信息: %v", err)
	}
	storage := &recordingUploadStorage{}
	a.backendFactory = func(StorageRecord) (storageBackend, error) { return storage, nil }

	img, err := a.ingestImage(context.Background(), bytes.NewReader(truncated), "truncated.png", int64(len(truncated)), "local")
	if err != nil {
		t.Fatalf("缩略图生成失败不应使原图上传失败: %v", err)
	}
	if img.ID == "" || img.ThumbnailURL != "" {
		t.Fatalf("缩略图失败回退结果错误: %+v", img)
	}
	if len(storage.putKeys) != 1 {
		t.Fatalf("缩略图生成失败时不应尝试第二次写入: %#v", storage.putKeys)
	}
	var imageCount, variantCount int
	_ = a.db.QueryRow(`SELECT count(*) FROM images`).Scan(&imageCount)
	_ = a.db.QueryRow(`SELECT count(*) FROM image_variants`).Scan(&variantCount)
	if imageCount != 1 || variantCount != 0 {
		t.Fatalf("回退后的数据库记录错误: images=%d variants=%d", imageCount, variantCount)
	}
}

func TestDatabaseFailureRollsBackOriginalAndThumbnailWithIndependentContext(t *testing.T) {
	a, record, settings := prepareIngestTest(t)
	storage := &recordingUploadStorage{}
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	content := pngBytes(t)
	_, apiErr := a.ingestImageWithBackend(ctx, bytes.NewReader(content), "test.png", int64(len(content)), settings, record, storage, "test-request")
	if apiErr == nil || apiErr.Code != "DATABASE_ERROR" {
		t.Fatalf("预期数据库错误，得到 %+v", apiErr)
	}
	if len(storage.putKeys) != 2 {
		t.Fatalf("数据库失败前应已写入原图与缩略图: %#v", storage.putKeys)
	}
	if len(storage.deleteKeys) != 2 {
		t.Fatalf("数据库失败后应回滚原图与缩略图: %#v", storage.deleteKeys)
	}
	for _, contextError := range storage.deleteCtxError {
		if contextError != nil {
			t.Fatalf("回滚上下文继承了请求取消: %v", contextError)
		}
	}
	if storage.deleteKeys[0] != storage.putKeys[1] || storage.deleteKeys[1] != storage.putKeys[0] {
		t.Fatalf("应先回滚派生对象再回滚原图: put=%#v delete=%#v", storage.putKeys, storage.deleteKeys)
	}
}

func TestGeneratedThumbnailUsesExpectedDimensionsAndEncoding(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 800, 400))
	source.Set(0, 0, color.NRGBA{R: 255, A: 100})
	var content bytes.Buffer
	if err := png.Encode(&content, source); err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(t.TempDir(), "source-*.png")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write(content.Bytes()); err != nil {
		t.Fatal(err)
	}
	generated, err := generateThumbnail(file, "image/png", "image-id")
	if err != nil {
		t.Fatal(err)
	}
	if generated.Width != 480 || generated.Height != 240 || generated.MIMEType != "image/png" ||
		generated.ObjectKey != "variants/image-id/thumbnail.png" {
		t.Fatalf("缩略图属性错误: %+v", generated)
	}
	thumbnail, format, err := image.Decode(generated.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" || thumbnail.Bounds().Dx() != 480 || thumbnail.Bounds().Dy() != 240 {
		t.Fatalf("缩略图内容错误: format=%s bounds=%v", format, thumbnail.Bounds())
	}
}

func TestAPIErrorStillSerializesAsStructuredObject(t *testing.T) {
	data, err := json.Marshal(uploadResult{Error: &apiError{Code: "FAILED", Message: "失败"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"error":{"code":"FAILED","message":"失败"}`) {
		t.Fatalf("apiError 实现 Error 后响应结构发生变化: %s", data)
	}
}

func TestRebuildThumbnailsUsesStorageOpen(t *testing.T) {
	a, _, _ := prepareIngestTest(t)
	content := pngBytes(t)
	insertImageForTest(t, a, "legacy", nowUTC(), "")
	storage := &recordingUploadStorage{openContent: content}
	a.backendFactory = func(StorageRecord) (storageBackend, error) { return storage, nil }

	report, err := a.RebuildThumbnails(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || report.Created != 1 || report.Failed != 0 {
		t.Fatalf("缩略图回填报告错误: %+v", report)
	}
	var count int
	if err := a.db.QueryRow(`SELECT count(*) FROM image_variants WHERE image_id='legacy' AND kind='thumbnail'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("缩略图回填记录错误: count=%d err=%v", count, err)
	}
}

func TestRebuildThumbnailsSkipsTelegram(t *testing.T) {
	a, _, _ := prepareIngestTest(t)
	insertImageForTest(t, a, "legacy", nowUTC(), "")
	if _, err := a.db.Exec(`UPDATE storages SET type='telegram' WHERE id='local'`); err != nil {
		t.Fatal(err)
	}

	report, err := a.RebuildThumbnails(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || report.Skipped != 1 || report.Items[0].Status != "skipped" {
		t.Fatalf("Telegram 回填应被跳过: %+v", report)
	}
}
