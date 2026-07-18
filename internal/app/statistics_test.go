package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

func TestStatisticsAndPublicTraffic(t *testing.T) {
	a := newTestApp(t)
	handler := a.Handler()
	cookie, csrf := initializeTestApp(t, handler)

	content := pngBytes(t)
	body, contentType := uploadBody(t, content)
	recorder, response := request(t, handler, http.MethodPost, "/api/v1/images", body, cookie, csrf, "", contentType)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("上传测试图片失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var image Image
	if err := json.Unmarshal(response.Data, &image); err != nil {
		t.Fatal(err)
	}
	publicURL, err := url.Parse(image.PublicURL)
	if err != nil {
		t.Fatal(err)
	}
	recorder, _ = request(t, handler, http.MethodGet, publicURL.RequestURI(), nil, nil, "", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取公开图片失败: %d %s", recorder.Code, recorder.Body.String())
	}
	servedBytes := recorder.Body.Len()

	recorder, response = request(t, handler, http.MethodGet, "/api/v1/statistics", nil, cookie, "", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取统计失败: %d %s", recorder.Code, recorder.Body.String())
	}
	var statistics uploadStatistics
	if err := json.Unmarshal(response.Data, &statistics); err != nil {
		t.Fatal(err)
	}
	if statistics.ImageCount != 1 {
		t.Fatalf("图片数量错误: %d", statistics.ImageCount)
	}
	if statistics.StorageBytes < int64(len(content)) {
		t.Fatalf("存储占用过小: %d", statistics.StorageBytes)
	}
	if statistics.TrafficBytes != int64(servedBytes) {
		t.Fatalf("累计流量错误: %d", statistics.TrafficBytes)
	}
}
