package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"
)

type StorageVerificationItem struct {
	StorageID string `json:"storage_id"`
	ImageID   string `json:"image_id,omitempty"`
	Kind      string `json:"kind,omitempty"`
	ObjectKey string `json:"object_key,omitempty"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
}

type StorageVerificationReport struct {
	Storages int                       `json:"storages"`
	Sampled  int                       `json:"sampled"`
	Verified int                       `json:"verified"`
	Failed   int                       `json:"failed"`
	Items    []StorageVerificationItem `json:"items"`
}

type storageObjectCandidate struct {
	ImageID, Kind, ObjectKey, Hash string
	Size                           int64
}

// VerifyStorageObjects reads a bounded sample from each selected storage and
// validates its size. Original images are additionally checked against their
// stored SHA-256 digest. Remote-only is the safe default for scheduled checks.
func (a *App) VerifyStorageObjects(ctx context.Context, storageID string, sample int, includeLocal bool) (StorageVerificationReport, error) {
	if sample < 1 {
		sample = 10
	}
	if sample > 1000 {
		return StorageVerificationReport{}, fmt.Errorf("每个存储的抽样数量不能超过 1000")
	}
	rows, err := a.db.QueryContext(ctx, "SELECT id,type FROM storages ORDER BY created_at")
	if err != nil {
		return StorageVerificationReport{}, err
	}
	type selectedStorage struct{ id, kind string }
	selected := make([]selectedStorage, 0)
	for rows.Next() {
		var item selectedStorage
		if err := rows.Scan(&item.id, &item.kind); err != nil {
			_ = rows.Close()
			return StorageVerificationReport{}, err
		}
		if storageID != "" && item.id != storageID {
			continue
		}
		if !includeLocal && storageID == "" && item.kind == "local" {
			continue
		}
		selected = append(selected, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return StorageVerificationReport{}, err
	}
	_ = rows.Close()
	if storageID != "" && len(selected) == 0 {
		return StorageVerificationReport{}, fmt.Errorf("存储 %s 不存在或被当前筛选排除", storageID)
	}
	report := StorageVerificationReport{Storages: len(selected), Items: make([]StorageVerificationItem, 0)}
	for _, selectedStorage := range selected {
		record, err := a.storageRecord(ctx, selectedStorage.id)
		if err != nil {
			report.Failed++
			report.Items = append(report.Items, StorageVerificationItem{StorageID: selectedStorage.id, Status: "failed", Message: "读取存储配置失败: " + err.Error()})
			continue
		}
		backend, err := a.backend(record)
		if err != nil {
			report.Failed++
			report.Items = append(report.Items, StorageVerificationItem{StorageID: record.ID, Status: "failed", Message: "初始化存储失败: " + err.Error()})
			continue
		}
		candidates, err := a.storageVerificationCandidates(ctx, record.ID, sample)
		if err != nil {
			return report, err
		}
		for _, candidate := range candidates {
			report.Sampled++
			item := StorageVerificationItem{StorageID: record.ID, ImageID: candidate.ImageID, Kind: candidate.Kind, ObjectKey: candidate.ObjectKey, Status: "verified"}
			verifyCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			reader, openErr := backend.Open(verifyCtx, candidate.ObjectKey)
			if openErr != nil {
				cancel()
				item.Status, item.Message = "failed", openErr.Error()
				report.Failed++
				report.Items = append(report.Items, item)
				continue
			}
			hash := sha256.New()
			written, readErr := io.Copy(hash, io.LimitReader(reader, candidate.Size+1))
			closeErr := reader.Close()
			cancel()
			if readErr == nil {
				readErr = closeErr
			}
			switch {
			case readErr != nil:
				item.Status, item.Message = "failed", readErr.Error()
			case written != candidate.Size:
				item.Status, item.Message = "failed", fmt.Sprintf("大小不一致：期望 %d，读取 %d", candidate.Size, written)
			case candidate.Hash != "" && !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), candidate.Hash):
				item.Status, item.Message = "failed", "SHA-256 不一致"
			}
			if item.Status == "failed" {
				report.Failed++
			} else {
				report.Verified++
			}
			report.Items = append(report.Items, item)
		}
	}
	return report, nil
}

func (a *App) storageVerificationCandidates(ctx context.Context, storageID string, sample int) ([]storageObjectCandidate, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT image_id,kind,object_key,size,hash FROM (
		SELECT i.id AS image_id,'original' AS kind,i.object_key,i.size,i.hash,i.created_at AS sort_time
		FROM images i WHERE i.storage_id=?
		UNION ALL
		SELECT i.id AS image_id,v.kind,v.object_key,v.size,'' AS hash,v.created_at AS sort_time
		FROM image_variants v JOIN images i ON i.id=v.image_id WHERE i.storage_id=?
	) ORDER BY sort_time DESC,image_id,kind LIMIT ?`, storageID, storageID, sample)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]storageObjectCandidate, 0, sample)
	for rows.Next() {
		var item storageObjectCandidate
		if err := rows.Scan(&item.ImageID, &item.Kind, &item.ObjectKey, &item.Size, &item.Hash); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
