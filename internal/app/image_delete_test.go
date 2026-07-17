package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func insertImageForTest(t *testing.T, a *App, id, createdAt, deletedAt string) {
	t.Helper()
	var deleted any
	if deletedAt != "" {
		deleted = deletedAt
	}
	_, err := a.db.Exec(`INSERT INTO images(
		id,hash,original_name,object_key,storage_type,storage_id,mime_type,size,width,height,
		public_url,created_at,deleted_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, "hash-"+id, id+".png", id+".png", "local", "local", "image/png", 10, 2, 3,
		"/files/"+id+".png", createdAt, deleted)
	if err != nil {
		t.Fatal(err)
	}
}

func responseMap(t *testing.T, response testResponse) map[string]any {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(response.Data, &data); err != nil {
		t.Fatal(err)
	}
	return data
}

func responseImages(t *testing.T, response testResponse) ([]Image, string) {
	t.Helper()
	var data struct {
		Items      []Image `json:"items"`
		NextCursor string  `json:"next_cursor"`
	}
	if err := json.Unmarshal(response.Data, &data); err != nil {
		t.Fatal(err)
	}
	return data.Items, data.NextCursor
}

func TestBulkTrashAndFavorite(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	cookie, csrf := initializeTestApp(t, handler)
	now := nowUTC()
	insertImageForTest(t, a, "one", now, "")
	insertImageForTest(t, a, "two", now, "")

	recorder, response := request(t, handler, http.MethodPost, "/api/v1/images/bulk",
		strings.NewReader(`{"action":"trash","ids":["one","one","missing"]}`),
		cookie, csrf, "", "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("批量移入回收站失败: %d %s", recorder.Code, recorder.Body.String())
	}
	data := responseMap(t, response)
	if data["requested"] != float64(2) || data["affected"] != float64(1) || data["not_found"] != float64(1) {
		t.Fatalf("批量结果错误: %#v", data)
	}

	for _, value := range []bool{true, false} {
		body := `{"action":"favorite","ids":["two"],"value":` + map[bool]string{true: "true", false: "false"}[value] + `}`
		recorder, _ = request(t, handler, http.MethodPost, "/api/v1/images/bulk",
			strings.NewReader(body), cookie, csrf, "", "application/json")
		if recorder.Code != http.StatusOK {
			t.Fatalf("批量收藏 value=%v 失败: %d %s", value, recorder.Code, recorder.Body.String())
		}
		var favorite bool
		if err := a.db.QueryRow(`SELECT favorite FROM images WHERE id='two'`).Scan(&favorite); err != nil {
			t.Fatal(err)
		}
		if favorite != value {
			t.Fatalf("收藏值为 %v，期望 %v", favorite, value)
		}
	}

	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = newUUIDv7()
	}
	body, _ := json.Marshal(map[string]any{"action": "trash", "ids": tooMany})
	recorder, _ = request(t, handler, http.MethodPost, "/api/v1/images/bulk",
		bytes.NewReader(body), cookie, csrf, "", "application/json")
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("超过 100 个 ID 应被拒绝，得到 %d", recorder.Code)
	}
	for _, body := range []string{
		`{"action":"trash","ids":[]}`,
		`{"action":"unknown","ids":["two"]}`,
		`{"action":"favorite","ids":["two"]}`,
	} {
		recorder, _ = request(t, handler, http.MethodPost, "/api/v1/images/bulk",
			strings.NewReader(body), cookie, csrf, "", "application/json")
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("无效批量请求应被拒绝: %s => %d", body, recorder.Code)
		}
	}
}

func TestTrashPaginationRestoreAndPermanentDelete(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	cookie, csrf := initializeTestApp(t, handler)
	deletedAt := "2026-07-17T10:00:00.000000000Z"
	for _, id := range []string{"a", "b", "c"} {
		insertImageForTest(t, a, id, "2026-07-01T00:00:00.000000000Z", deletedAt)
	}

	recorder, response := request(t, handler, http.MethodGet, "/api/v1/trash?limit=2", nil, cookie, "", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取回收站失败: %d %s", recorder.Code, recorder.Body.String())
	}
	first, cursor := responseImages(t, response)
	if len(first) != 2 || cursor == "" {
		t.Fatalf("第一页错误: items=%d cursor=%q", len(first), cursor)
	}
	recorder, response = request(t, handler, http.MethodGet, "/api/v1/trash?limit=2&cursor="+cursor, nil, cookie, "", "", "")
	second, _ := responseImages(t, response)
	if recorder.Code != http.StatusOK || len(second) != 1 {
		t.Fatalf("第二页错误: %d %s", recorder.Code, recorder.Body.String())
	}
	seen := map[string]bool{}
	for _, item := range append(first, second...) {
		if seen[item.ID] {
			t.Fatalf("回收站分页重复图片 %s", item.ID)
		}
		seen[item.ID] = true
	}

	recorder, _ = request(t, handler, http.MethodPost, "/api/v1/trash/a/restore", nil, cookie, csrf, "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("恢复失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var deleted any
	if err := a.db.QueryRow(`SELECT deleted_at FROM images WHERE id='a'`).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if deleted != nil {
		t.Fatalf("恢复后 deleted_at 未清空: %#v", deleted)
	}

	recorder, _ = request(t, handler, http.MethodDelete, "/api/v1/trash/b", nil, cookie, csrf, "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("永久删除失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var count int
	if err := a.db.QueryRow(`SELECT count(*) FROM images WHERE id='b'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("永久删除后记录仍存在: count=%d err=%v", count, err)
	}

	normalCursor := encodeCursor("desc", deletedAt, "c")
	recorder, _ = request(t, handler, http.MethodGet, "/api/v1/trash?cursor="+normalCursor, nil, cookie, "", "", "")
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("普通图库游标不应能用于回收站，得到 %d", recorder.Code)
	}
}

func TestImageOrderingCursorAndDateFilters(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	cookie, _ := initializeTestApp(t, handler)
	insertImageForTest(t, a, "a", "2026-07-01T00:00:00.000000000Z", "")
	insertImageForTest(t, a, "b", "2026-07-02T00:00:00.000000000Z", "")
	insertImageForTest(t, a, "c", "2026-07-02T00:00:00.000000000Z", "")
	insertImageForTest(t, a, "d", "2026-07-03T00:00:00.000000000Z", "")

	for _, order := range []string{"asc", "desc"} {
		target := "/api/v1/images?limit=2&order=" + order
		recorder, response := request(t, handler, http.MethodGet, target, nil, cookie, "", "", "")
		first, cursor := responseImages(t, response)
		if recorder.Code != http.StatusOK || len(first) != 2 || cursor == "" {
			t.Fatalf("%s 第一页错误: %d %s", order, recorder.Code, recorder.Body.String())
		}
		recorder, response = request(t, handler, http.MethodGet, target+"&cursor="+cursor, nil, cookie, "", "", "")
		second, _ := responseImages(t, response)
		if recorder.Code != http.StatusOK || len(second) != 2 {
			t.Fatalf("%s 第二页错误: %d %s", order, recorder.Code, recorder.Body.String())
		}
		seen := map[string]bool{}
		for _, item := range append(first, second...) {
			if seen[item.ID] {
				t.Fatalf("%s 分页重复图片 %s", order, item.ID)
			}
			seen[item.ID] = true
		}
		otherOrder := map[string]string{"asc": "desc", "desc": "asc"}[order]
		recorder, _ = request(t, handler, http.MethodGet,
			"/api/v1/images?order="+otherOrder+"&cursor="+cursor, nil, cookie, "", "", "")
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("切换排序后旧游标应失效，得到 %d", recorder.Code)
		}
	}

	target := "/api/v1/images?from=2026-07-02T00:00:00Z&to=2026-07-02T00:00:00Z"
	recorder, response := request(t, handler, http.MethodGet, target, nil, cookie, "", "", "")
	items, _ := responseImages(t, response)
	if recorder.Code != http.StatusOK || len(items) != 2 {
		t.Fatalf("日期边界筛选错误: %d %s", recorder.Code, recorder.Body.String())
	}
}

type deleteTestStorage struct {
	mu       sync.Mutex
	failKeys map[string]error
	deleted  []string
}

func (s *deleteTestStorage) Put(context.Context, string, io.Reader, int64, string) (string, error) {
	return "", errors.New("not implemented")
}
func (s *deleteTestStorage) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}
func (s *deleteTestStorage) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, key)
	return s.failKeys[key]
}
func (s *deleteTestStorage) Test(context.Context) error { return nil }

func TestPurgeReturnsPerItemResultsAndKeepsFailures(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	cookie, csrf := initializeTestApp(t, handler)
	deletedAt := nowUTC()
	insertImageForTest(t, a, "failed", deletedAt, deletedAt)
	insertImageForTest(t, a, "success", deletedAt, deletedAt)
	_, err := a.db.Exec(`INSERT INTO image_variants(
		id,image_id,kind,object_key,public_url,mime_type,size,width,height,created_at
	) VALUES('variant','failed','thumbnail','failed-thumb.png','/files/failed-thumb.png','image/png',5,2,2,?)`, deletedAt)
	if err != nil {
		t.Fatal(err)
	}
	storage := &deleteTestStorage{failKeys: map[string]error{"failed.png": errors.New("simulated failure")}}
	a.backendFactory = func(StorageRecord) (storageBackend, error) { return storage, nil }

	recorder, response := request(t, handler, http.MethodPost, "/api/v1/trash/purge",
		strings.NewReader(`{"ids":["failed","success"]}`), cookie, csrf, "", "application/json")
	if recorder.Code != http.StatusMultiStatus || !response.Success {
		t.Fatalf("部分清理响应错误: %d %s", recorder.Code, recorder.Body.String())
	}
	data := responseMap(t, response)
	if data["succeeded"] != float64(1) || data["failed"] != float64(1) {
		t.Fatalf("逐项清理统计错误: %#v", data)
	}
	var count int
	var purgeError string
	if err := a.db.QueryRow(`SELECT count(*),COALESCE(max(purge_error),'') FROM images WHERE id='failed'`).Scan(&count, &purgeError); err != nil {
		t.Fatal(err)
	}
	if count != 1 || !strings.Contains(purgeError, "simulated failure") {
		t.Fatalf("失败记录未保留清理错误: count=%d error=%q", count, purgeError)
	}
	if err := a.db.QueryRow(`SELECT count(*) FROM images WHERE id='success'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("成功记录未删除: count=%d err=%v", count, err)
	}
	if !contains(storage.deleted, "failed-thumb.png") || !contains(storage.deleted, "failed.png") || !contains(storage.deleted, "success.png") {
		t.Fatalf("未尝试清理全部对象: %#v", storage.deleted)
	}

	delete(storage.failKeys, "failed.png")
	recorder, _ = request(t, handler, http.MethodPost, "/api/v1/trash/purge",
		strings.NewReader(`{"all":true}`), cookie, csrf, "", "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("清空回收站失败: %d %s", recorder.Code, recorder.Body.String())
	}
	if err := a.db.QueryRow(`SELECT count(*) FROM images WHERE deleted_at IS NOT NULL`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("清空后仍有记录: count=%d err=%v", count, err)
	}
}

func TestPurgeRecordsBackendCreationFailure(t *testing.T) {
	a := newTestApp(t)
	initializeTestApp(t, a.Handler())
	deletedAt := nowUTC()
	insertImageForTest(t, a, "failed", deletedAt, deletedAt)
	a.backendFactory = func(StorageRecord) (storageBackend, error) {
		return nil, errors.New("simulated backend failure")
	}

	err := a.permanentlyDeleteImage(context.Background(), "failed")
	if err == nil {
		t.Fatal("创建存储后端失败时永久删除不应成功")
	}
	var purgeError string
	if err := a.db.QueryRow(`SELECT COALESCE(purge_error,'') FROM images WHERE id='failed'`).Scan(&purgeError); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(purgeError, "simulated backend failure") {
		t.Fatalf("存储后端失败未写入 purge_error: %q", purgeError)
	}
}
