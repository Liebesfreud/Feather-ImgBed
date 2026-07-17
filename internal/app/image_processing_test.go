package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func processingPNGBytes(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewNRGBA(image.Rect(0, 0, 96, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 96; x++ {
			canvas.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 2), G: uint8(y * 3), B: 120, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, canvas); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func processingJPEGBytes(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 80, 60))
	for y := 0; y < 60; y++ {
		for x := 0; x < 80; x++ {
			canvas.Set(x, y, color.RGBA{R: uint8(x * 3), G: uint8(y * 4), B: 90, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, canvas, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func processingSourceFile(t *testing.T, content []byte) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "processing-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func TestGenerateConfiguredWebPAndWatermarkVariants(t *testing.T) {
	settings := ProcessingSettings{
		GenerateWebP: true, WebPQuality: 82,
		WatermarkEnabled: true, WatermarkText: "Feather", WatermarkPosition: "bottom-right",
	}
	for _, test := range []struct {
		name, mime string
		content    []byte
	}{
		{name: "png", mime: "image/png", content: processingPNGBytes(t)},
		{name: "jpeg", mime: "image/jpeg", content: processingJPEGBytes(t)},
	} {
		t.Run(test.name, func(t *testing.T) {
			generated, failures := generateProcessingVariants(processingSourceFile(t, test.content), test.mime, "image-id", settings)
			if len(failures) != 0 || len(generated) != 2 {
				t.Fatalf("派生生成结果错误: generated=%+v failures=%v", generated, failures)
			}
			byKind := make(map[string]generatedImage)
			for _, item := range generated {
				byKind[item.Kind] = item.Image
			}
			webpData, err := io.ReadAll(byKind["webp"].Reader)
			if err != nil {
				t.Fatal(err)
			}
			if len(webpData) < 12 || string(webpData[:4]) != "RIFF" || string(webpData[8:12]) != "WEBP" {
				t.Fatalf("WebP 派生格式无效: %x", webpData[:min(len(webpData), 16)])
			}
			config, format, err := image.DecodeConfig(bytes.NewReader(webpData))
			if err != nil || format != "webp" || config.Width == 0 || config.Height == 0 {
				t.Fatalf("WebP 派生不可解码: config=%+v format=%s err=%v", config, format, err)
			}
			watermarkData, err := io.ReadAll(byKind["watermarked"].Reader)
			if err != nil {
				t.Fatal(err)
			}
			config, format, err = image.DecodeConfig(bytes.NewReader(watermarkData))
			expectedFormat := "png"
			if test.mime == "image/jpeg" {
				expectedFormat = "jpeg"
			}
			if err != nil || format != expectedFormat || config.Width == 0 || config.Height == 0 {
				t.Fatalf("水印派生不可解码: config=%+v format=%s err=%v", config, format, err)
			}
		})
	}
}

func TestConfiguredProcessingSkipsGIF(t *testing.T) {
	var buffer bytes.Buffer
	if err := gif.Encode(&buffer, image.NewPaletted(image.Rect(0, 0, 8, 8), color.Palette{color.Black}), nil); err != nil {
		t.Fatal(err)
	}
	generated, failures := generateProcessingVariants(
		processingSourceFile(t, buffer.Bytes()), "image/gif", "gif-id",
		ProcessingSettings{
			GenerateWebP: true, WebPQuality: 82,
			WatermarkEnabled: true, WatermarkText: "skip", WatermarkPosition: "center",
		},
	)
	if len(generated) != 0 || len(failures) != 0 {
		t.Fatalf("GIF 默认不应生成处理派生: generated=%+v failures=%v", generated, failures)
	}
}

func TestProcessingSettingsValidation(t *testing.T) {
	settings := defaultSettings()
	settings.SiteName = "test"
	settings.DefaultStorageID = "local"
	if message := validateSettings(settings); message != "" {
		t.Fatalf("默认处理设置应有效: %s", message)
	}
	settings.Processing.GenerateWebP = true
	settings.Processing.WebPQuality = 0
	if message := validateSettings(settings); !strings.Contains(message, "WebP") {
		t.Fatalf("WebP 质量未校验: %s", message)
	}
	settings = defaultSettings()
	settings.SiteName = "test"
	settings.DefaultStorageID = "local"
	settings.Processing.WatermarkEnabled = true
	if message := validateSettings(settings); !strings.Contains(message, "水印文字") {
		t.Fatalf("空水印文字未校验: %s", message)
	}
	settings.Processing.WatermarkText = "Feather"
	settings.Processing.WatermarkPosition = "outside"
	if message := validateSettings(settings); !strings.Contains(message, "水印位置") {
		t.Fatalf("水印位置未校验: %s", message)
	}
}

func saveTestProcessingSettings(t *testing.T, a *App, processing ProcessingSettings) {
	t.Helper()
	settings, err := loadSettings(t.Context(), a.db)
	if err != nil {
		t.Fatal(err)
	}
	settings.Processing = processing
	tx, err := a.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveSettingsTx(t.Context(), tx, settings); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestUploadStoresConfiguredVariantsAndDetailListsThem(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	cookie, csrf := initializeTestApp(t, handler)
	saveTestProcessingSettings(t, a, ProcessingSettings{
		GenerateWebP: true, WebPQuality: 82,
		WatermarkEnabled: true, WatermarkText: "Feather", WatermarkPosition: "center",
	})
	body, contentType := uploadBody(t, processingPNGBytes(t))
	recorder, response := request(t, handler, http.MethodPost, "/api/v1/images", body, cookie, csrf, "", contentType)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("启用处理后上传失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var uploaded Image
	_ = json.Unmarshal(response.Data, &uploaded)
	recorder, response = request(t, handler, http.MethodGet, "/api/v1/images/"+uploaded.ID, nil, cookie, "", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取图片详情失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var detail Image
	_ = json.Unmarshal(response.Data, &detail)
	if detail.PublicURL != uploaded.PublicURL {
		t.Fatalf("派生处理覆盖了原图 URL: %q != %q", detail.PublicURL, uploaded.PublicURL)
	}
	expectedKinds := []string{"thumbnail", "watermarked", "webp"}
	if len(detail.Variants) != len(expectedKinds) {
		t.Fatalf("图片详情派生数量错误: %+v", detail.Variants)
	}
	for index, expected := range expectedKinds {
		if detail.Variants[index].Kind != expected || detail.Variants[index].PublicURL == "" {
			t.Fatalf("图片详情派生顺序/内容错误: %+v", detail.Variants)
		}
	}
}

type processingFailureStorage struct {
	putKeys    []string
	deleteKeys []string
	failKinds  bool
}

func (s *processingFailureStorage) Put(_ context.Context, key string, reader io.Reader, _ int64, _ string) (string, error) {
	s.putKeys = append(s.putKeys, key)
	_, _ = io.Copy(io.Discard, reader)
	if s.failKinds && (strings.Contains(key, "/image.webp") || strings.Contains(key, "/watermarked.")) {
		return "", errors.New("variant failure")
	}
	return key, nil
}
func (s *processingFailureStorage) Delete(_ context.Context, key string) error {
	s.deleteKeys = append(s.deleteKeys, key)
	return nil
}
func (s *processingFailureStorage) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}
func (s *processingFailureStorage) Test(context.Context) error { return nil }

func TestProcessingStorageFailureDoesNotFailOriginalUpload(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	cookie, csrf := initializeTestApp(t, handler)
	saveTestProcessingSettings(t, a, ProcessingSettings{
		GenerateWebP: true, WebPQuality: 82,
		WatermarkEnabled: true, WatermarkText: "Feather", WatermarkPosition: "center",
	})
	storage := &processingFailureStorage{failKinds: true}
	a.backendFactory = func(StorageRecord) (storageBackend, error) { return storage, nil }
	body, contentType := uploadBody(t, processingPNGBytes(t))
	recorder, response := request(t, handler, http.MethodPost, "/api/v1/images", body, cookie, csrf, "", contentType)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("派生写入失败不应影响原图: %d %s", recorder.Code, recorder.Body.String())
	}
	var uploaded Image
	_ = json.Unmarshal(response.Data, &uploaded)
	var imageCount, variantCount int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM images WHERE id=?`, uploaded.ID).Scan(&imageCount)
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM image_variants WHERE image_id=?`, uploaded.ID).Scan(&variantCount)
	if imageCount != 1 || variantCount != 1 {
		t.Fatalf("派生失败后的元数据错误: images=%d variants=%d", imageCount, variantCount)
	}
}

func TestDatabaseFailureRollsBackAllStoredProcessingObjects(t *testing.T) {
	a := newTestApp(t)
	body, contentType := uploadBody(t, processingPNGBytes(t))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/images", body)
	request.Header.Set("Content-Type", contentType)
	if err := request.ParseMultipartForm(2 << 20); err != nil {
		t.Fatal(err)
	}
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	storage := &processingFailureStorage{}
	settings := defaultSettings()
	settings.Processing = ProcessingSettings{
		GenerateWebP: true, WebPQuality: 82,
		WatermarkEnabled: true, WatermarkText: "Feather", WatermarkPosition: "center",
	}
	header := request.MultipartForm.File["file"][0]
	_, apiErr := a.uploadOne(request, settings, StorageRecord{ID: "local", Type: "local"}, storage, header)
	if apiErr == nil || apiErr.Code != "DATABASE_ERROR" {
		t.Fatalf("预期数据库失败，得到 %+v", apiErr)
	}
	if len(storage.putKeys) != 4 || len(storage.deleteKeys) != 4 {
		t.Fatalf("数据库失败未回滚原图和全部派生: put=%+v delete=%+v", storage.putKeys, storage.deleteKeys)
	}
	deleted := make(map[string]bool, len(storage.deleteKeys))
	for _, key := range storage.deleteKeys {
		deleted[key] = true
	}
	for _, key := range storage.putKeys {
		if !deleted[key] {
			t.Fatalf("对象未回滚: %s", key)
		}
	}
}
