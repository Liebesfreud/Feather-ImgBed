package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestExportAndImportDirectory(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	cookie, csrf := initializeTestApp(t, handler)
	body, contentType := uploadBody(t, pngBytes(t))
	recorder, response := request(t, handler, http.MethodPost, "/api/v1/images", body, cookie, csrf, "", contentType)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("上传失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var uploaded Image
	if err := json.Unmarshal(response.Data, &uploaded); err != nil {
		t.Fatal(err)
	}
	now := nowUTC()
	if _, err := a.db.Exec(`INSERT INTO tags(id,name,color,created_at,updated_at) VALUES('export-tag','导出标签','#22c55e',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO image_tags(image_id,tag_id) VALUES(?, 'export-tag')`, uploaded.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO albums(id,name,description,cover_image_id,created_at,updated_at) VALUES('export-album','导出相册','',?, ?,?)`, uploaded.ID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO album_images(album_id,image_id,position,added_at) VALUES('export-album',?,0,?)`, uploaded.ID, now); err != nil {
		t.Fatal(err)
	}
	exportDir := filepath.Join(t.TempDir(), "export")
	report, err := a.ExportData(context.Background(), exportDir, false)
	if err != nil || report.Images != 1 || report.Variants == 0 {
		t.Fatalf("导出失败: %+v %v", report, err)
	}
	if _, err := os.Stat(filepath.Join(exportDir, "metadata.json")); err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(filepath.Join(exportDir, "metadata.json"))
	if err != nil || !containsBytes(metadata, []byte(uploaded.ID)) {
		t.Fatalf("导出元数据缺少图片: %v", err)
	}
	var portable PortableExport
	if err := json.Unmarshal(metadata, &portable); err != nil {
		t.Fatal(err)
	}
	if len(portable.Images) != 1 || len(portable.Tags) != 1 || len(portable.ImageTags) != 1 || len(portable.Albums) != 1 || len(portable.AlbumImages) != 1 {
		t.Fatalf("导出未完整保留组织元数据: %+v", portable)
	}
	if portable.Images[0].Object.SHA256 == "" || portable.Images[0].Object.Path == "" {
		t.Fatalf("导出原图缺少对象校验信息: %+v", portable.Images[0].Object)
	}
	b := newTestApp(t)
	initializeTestApp(t, b.Handler())
	importReport, err := b.ImportDirectory(context.Background(), exportDir, "", true, 0)
	if err != nil || importReport.Imported != 1 || importReport.Failed != 0 {
		t.Fatalf("目录导入失败: %+v %v", importReport, err)
	}
}

func containsBytes(value, target []byte) bool {
	for index := 0; index+len(target) <= len(value); index++ {
		match := true
		for offset := range target {
			if value[index+offset] != target[offset] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
