package app

import (
	"context"
	"database/sql"
)

type storageURLStore interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (a *App) refreshProxyPublicURLs(ctx context.Context, store storageURLStore, siteURL string) error {
	rows, err := store.QueryContext(ctx, `SELECT id,type,config FROM storages
		WHERE type IN ('s3','telegram')`)
	if err != nil {
		return err
	}
	records := make([]StorageRecord, 0)
	for rows.Next() {
		var record StorageRecord
		var encrypted string
		if err := rows.Scan(&record.ID, &record.Type, &encrypted); err != nil {
			_ = rows.Close()
			return err
		}
		record.Config = map[string]any{}
		if err := decryptJSON(a.masterKey, encrypted, &record.Config); err != nil {
			_ = rows.Close()
			return err
		}
		if record.Type == "telegram" || isCloudflareR2Endpoint(record.Config) {
			records = append(records, record)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, record := range records {
		urlPrefix := publicURL(record, "", siteURL)
		if _, err := store.ExecContext(ctx, `UPDATE images
			SET public_url=? || ltrim(object_key, '/')
			WHERE storage_id=?`, urlPrefix, record.ID); err != nil {
			return err
		}
		if _, err := store.ExecContext(ctx, `UPDATE image_variants
			SET public_url=? || ltrim(object_key, '/')
			WHERE image_id IN (SELECT id FROM images WHERE storage_id=?)`, urlPrefix, record.ID); err != nil {
			return err
		}
	}
	return nil
}
