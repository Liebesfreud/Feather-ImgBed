package app

import (
	"context"
	"database/sql"
)

func listImageVariants(ctx context.Context, db *sql.DB, imageID string) ([]ImageVariant, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,image_id,kind,object_key,public_url,mime_type,size,width,height,created_at
		FROM image_variants WHERE image_id=? ORDER BY kind`, imageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ImageVariant, 0)
	for rows.Next() {
		var item ImageVariant
		if err := rows.Scan(&item.ID, &item.ImageID, &item.Kind, &item.ObjectKey, &item.PublicURL, &item.MIMEType, &item.Size, &item.Width, &item.Height, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
