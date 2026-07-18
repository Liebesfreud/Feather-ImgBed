package app

import (
	"context"
	"net/http"
	"time"
)

type uploadStatistics struct {
	ImageCount   int64 `json:"image_count"`
	StorageBytes int64 `json:"storage_bytes"`
	TrafficBytes int64 `json:"traffic_bytes"`
}

func (a *App) getStatistics(w http.ResponseWriter, r *http.Request) {
	var statistics uploadStatistics
	err := a.db.QueryRowContext(r.Context(), `
		SELECT
			(SELECT COUNT(*) FROM images WHERE deleted_at IS NULL),
			(SELECT COALESCE(SUM(size),0) FROM images) +
				(SELECT COALESCE(SUM(size),0) FROM image_variants),
			COALESCE((SELECT value FROM usage_stats WHERE key='traffic_bytes'),0)
	`).Scan(&statistics.ImageCount, &statistics.StorageBytes, &statistics.TrafficBytes)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "STATISTICS_READ_FAILED", "统计数据暂时无法读取")
		return
	}
	writeData(w, r, http.StatusOK, statistics)
}

func (a *App) recordTraffic(bytes int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := a.db.ExecContext(ctx, `
		INSERT INTO usage_stats(key,value) VALUES('traffic_bytes',?)
		ON CONFLICT(key) DO UPDATE SET value=value+excluded.value
	`, bytes)
	return err
}
