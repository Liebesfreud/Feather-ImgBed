package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type organizationTestContext struct {
	app     *App
	handler http.Handler
	cookie  *http.Cookie
	csrf    string
	images  []Image
}

func newOrganizationTestContext(t *testing.T) organizationTestContext {
	t.Helper()
	a := newTestApp(t)
	baseHandler := a.Handler()
	cookie, csrf := initializeTestApp(t, baseHandler)
	images := make([]Image, 0, 2)
	for index, content := range [][]byte{pngBytes(t), append(pngBytes(t), byte(0))} {
		body, contentType := uploadBody(t, content)
		recorder, response := request(t, baseHandler, http.MethodPost, "/api/v1/images", body, cookie, csrf, "", contentType)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("上传组织测试图片 %d 失败: %s", index, recorder.Body.String())
		}
		var image Image
		if err := json.Unmarshal(response.Data, &image); err != nil {
			t.Fatal(err)
		}
		images = append(images, image)
	}
	a.mux = http.NewServeMux()
	a.registerOrganizationRoutes()
	return organizationTestContext{app: a, handler: a.Handler(), cookie: cookie, csrf: csrf, images: images}
}

func TestFavoriteAndTagAPIs(t *testing.T) {
	test := newOrganizationTestContext(t)
	imageID := test.images[0].ID

	recorder, response := request(t, test.handler, http.MethodPatch, "/api/v1/images/"+imageID,
		strings.NewReader(`{"favorite":true}`), test.cookie, test.csrf, "", "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("收藏图片失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var updated Image
	_ = json.Unmarshal(response.Data, &updated)
	if !updated.Favorite {
		t.Fatalf("收藏状态未更新: %+v", updated)
	}
	requested, affected, notFound, err := test.app.bulkSetFavorite(
		t.Context(), []string{test.images[0].ID, test.images[1].ID, "missing", test.images[1].ID}, true,
	)
	if err != nil || requested != 3 || affected != 2 || notFound != 1 {
		t.Fatalf("批量收藏结果错误: requested=%d affected=%d notFound=%d err=%v", requested, affected, notFound, err)
	}

	tagIDs := make([]string, 0, 2)
	for _, payload := range []string{
		`{"name":"旅行","color":"#22C55E"}`,
		`{"name":"精选","color":"#3b82f6"}`,
	} {
		recorder, response = request(t, test.handler, http.MethodPost, "/api/v1/tags",
			strings.NewReader(payload), test.cookie, test.csrf, "", "application/json")
		if recorder.Code != http.StatusCreated {
			t.Fatalf("创建标签失败: %d %s", recorder.Code, recorder.Body.String())
		}
		var tag Tag
		_ = json.Unmarshal(response.Data, &tag)
		tagIDs = append(tagIDs, tag.ID)
	}
	recorder, response = request(t, test.handler, http.MethodPost, "/api/v1/tags",
		strings.NewReader(`{"name":"旅行","color":"#000000"}`), test.cookie, test.csrf, "", "application/json")
	if recorder.Code != http.StatusConflict || response.Error == nil || response.Error.Code != "TAG_NAME_EXISTS" {
		t.Fatalf("重复标签名称未被拒绝: %d %s", recorder.Code, recorder.Body.String())
	}

	payload, _ := json.Marshal(map[string]any{"tag_ids": []string{tagIDs[0], tagIDs[1], tagIDs[0]}})
	recorder, _ = request(t, test.handler, http.MethodPut, "/api/v1/images/"+imageID+"/tags",
		bytes.NewReader(payload), test.cookie, test.csrf, "", "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("设置图片标签失败: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder, response = request(t, test.handler, http.MethodGet, "/api/v1/images/"+imageID+"/tags",
		nil, test.cookie, "", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取图片标签失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var imageTags []Tag
	_ = json.Unmarshal(response.Data, &imageTags)
	if len(imageTags) != 2 {
		t.Fatalf("图片标签未去重或读取不完整: %+v", imageTags)
	}

	bulkPayload, _ := json.Marshal(map[string]any{
		"action": "add", "ids": []string{test.images[0].ID, test.images[1].ID}, "tag_ids": []string{tagIDs[0]},
	})
	recorder, _ = request(t, test.handler, http.MethodPost, "/api/v1/images/bulk/tags",
		bytes.NewReader(bulkPayload), test.cookie, test.csrf, "", "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("批量添加标签失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var associations int
	if err := test.app.db.QueryRow(`SELECT COUNT(*) FROM image_tags WHERE tag_id=?`, tagIDs[0]).Scan(&associations); err != nil || associations != 2 {
		t.Fatalf("批量标签关联错误: count=%d err=%v", associations, err)
	}

	recorder, _ = request(t, test.handler, http.MethodDelete, "/api/v1/tags/"+tagIDs[0],
		nil, test.cookie, test.csrf, "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("删除标签失败: %d %s", recorder.Code, recorder.Body.String())
	}
	if err := test.app.db.QueryRow(`SELECT COUNT(*) FROM image_tags WHERE tag_id=?`, tagIDs[0]).Scan(&associations); err != nil || associations != 0 {
		t.Fatalf("删除标签未级联解除关联: count=%d err=%v", associations, err)
	}
}

func TestAlbumAPIsDoNotDeleteImages(t *testing.T) {
	test := newOrganizationTestContext(t)
	recorder, response := request(t, test.handler, http.MethodPost, "/api/v1/albums",
		strings.NewReader(`{"name":"夏日相册","description":"海边照片"}`), test.cookie, test.csrf, "", "application/json")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("创建相册失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var album Album
	_ = json.Unmarshal(response.Data, &album)

	payload, _ := json.Marshal(map[string]any{"ids": []string{test.images[0].ID, test.images[1].ID, test.images[0].ID}})
	recorder, response = request(t, test.handler, http.MethodPost, "/api/v1/albums/"+album.ID+"/images",
		bytes.NewReader(payload), test.cookie, test.csrf, "", "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("加入相册失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var addResult struct {
		Requested int `json:"requested"`
		Added     int `json:"added"`
	}
	_ = json.Unmarshal(response.Data, &addResult)
	if addResult.Requested != 2 || addResult.Added != 2 {
		t.Fatalf("加入相册结果错误: %+v", addResult)
	}

	updatePayload, _ := json.Marshal(map[string]any{
		"name": "夏日精选", "description": "更新描述", "cover_image_id": test.images[0].ID,
	})
	recorder, response = request(t, test.handler, http.MethodPut, "/api/v1/albums/"+album.ID,
		bytes.NewReader(updatePayload), test.cookie, test.csrf, "", "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("更新相册封面失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var updated Album
	_ = json.Unmarshal(response.Data, &updated)
	if updated.CoverImageID != test.images[0].ID || updated.ImageCount != 2 {
		t.Fatalf("相册封面或数量错误: %+v", updated)
	}

	recorder, response = request(t, test.handler, http.MethodGet, "/api/v1/albums/"+album.ID,
		nil, test.cookie, "", "", "")
	if recorder.Code != http.StatusOK || !bytes.Contains(response.Data, []byte(test.images[1].ID)) {
		t.Fatalf("读取相册详情失败: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder, _ = request(t, test.handler, http.MethodDelete,
		"/api/v1/albums/"+album.ID+"/images/"+test.images[0].ID,
		nil, test.cookie, test.csrf, "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("移除相册图片失败: %d %s", recorder.Code, recorder.Body.String())
	}
	if err := test.app.db.QueryRow(`SELECT COALESCE(cover_image_id,'') FROM albums WHERE id=?`, album.ID).Scan(&updated.CoverImageID); err != nil || updated.CoverImageID != "" {
		t.Fatalf("移除封面图片后未清空封面: cover=%q err=%v", updated.CoverImageID, err)
	}

	recorder, _ = request(t, test.handler, http.MethodDelete, "/api/v1/albums/"+album.ID,
		nil, test.cookie, test.csrf, "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("删除相册失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var imageCount int
	if err := test.app.db.QueryRow(`SELECT COUNT(*) FROM images WHERE id IN (?,?)`, test.images[0].ID, test.images[1].ID).Scan(&imageCount); err != nil || imageCount != 2 {
		t.Fatalf("删除相册误删图片: count=%d err=%v", imageCount, err)
	}
}

func TestOrganizationBatchValidationIsAtomic(t *testing.T) {
	test := newOrganizationTestContext(t)
	recorder, response := request(t, test.handler, http.MethodPost, "/api/v1/images/bulk/tags",
		strings.NewReader(`{"action":"add","ids":["missing"],"tag_ids":["missing"]}`),
		test.cookie, test.csrf, "", "application/json")
	if recorder.Code != http.StatusBadRequest || response.Error == nil {
		t.Fatalf("非法批量标签请求未被拒绝: %d %s", recorder.Code, recorder.Body.String())
	}
	var count int
	if err := test.app.db.QueryRow(`SELECT COUNT(*) FROM image_tags`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("失败批量操作留下部分写入: count=%d err=%v", count, err)
	}

	ids := make([]string, maxOrganizationBatch+1)
	for index := range ids {
		ids[index] = "id"
	}
	payload, _ := json.Marshal(map[string]any{"ids": ids})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/albums/missing/images", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(test.cookie)
	req.Header.Set("X-CSRF-Token", test.csrf)
	recorderHTTP := httptest.NewRecorder()
	test.handler.ServeHTTP(recorderHTTP, req)
	if recorderHTTP.Code != http.StatusBadRequest {
		t.Fatalf("超量相册批次未被拒绝: %d %s", recorderHTTP.Code, recorderHTTP.Body.String())
	}
}
