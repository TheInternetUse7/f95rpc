package store

import (
	"context"
	"time"
)

// CatalogMetadata contains only API-owned fields, never local installation state or preferences.
type CatalogMetadata struct {
	Title          string
	Creator        string
	Engine         string
	Status         string
	Rating         float64
	CoverURL       string
	BannerURL      string
	ThreadURL      string
	CatalogVersion string
	LastFetchedAt  time.Time
}

func (s *Store) ListEnrichmentThreadIDs(ctx context.Context, staleBefore time.Time) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT thread_id FROM games
		WHERE approved = 1 AND ignored = 0 AND thread_id > 0
		  AND (last_fetched_at <= 0 OR last_fetched_at <= ?
		       OR COALESCE(title, '') = '' OR COALESCE(engine, '') = '' OR COALESCE(status, '') = '')
		ORDER BY thread_id
	`, staleBefore.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) UpdateCatalogMetadata(ctx context.Context, threadID int64, metadata CatalogMetadata) (int64, error) {
	return s.applyCatalogMetadata(ctx, threadID, metadata, false)
}

// ApplyCachedCatalogMetadata fills new/older installations without rewriting equal or newer fetches.
func (s *Store) ApplyCachedCatalogMetadata(ctx context.Context, threadID int64, metadata CatalogMetadata) (int64, error) {
	return s.applyCatalogMetadata(ctx, threadID, metadata, true)
}

func (s *Store) applyCatalogMetadata(ctx context.Context, threadID int64, metadata CatalogMetadata, onlyOlder bool) (int64, error) {
	olderOnly := 0
	if onlyOlder {
		olderOnly = 1
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE games SET
			title = CASE WHEN ? != '' THEN ? ELSE title END,
			creator = CASE WHEN ? != '' THEN ? ELSE creator END,
			engine = CASE WHEN ? != '' THEN ? ELSE engine END,
			status = CASE WHEN ? != '' THEN ? ELSE status END,
			rating = CASE WHEN ? > 0 THEN ? ELSE rating END,
			cover_url = CASE WHEN ? != '' THEN ? ELSE cover_url END,
			banner_url = CASE WHEN ? != '' THEN ? ELSE banner_url END,
			thread_url = CASE WHEN ? != '' THEN ? ELSE thread_url END,
			catalog_version = CASE WHEN ? != '' THEN ? ELSE catalog_version END,
			last_fetched_at = ?, updated_at = ?
		WHERE thread_id = ? AND thread_id > 0 AND approved = 1 AND ignored = 0
		  AND (? = 0 OR last_fetched_at < ?)
	`,
		metadata.Title, metadata.Title, metadata.Creator, metadata.Creator,
		metadata.Engine, metadata.Engine, metadata.Status, metadata.Status,
		metadata.Rating, metadata.Rating, metadata.CoverURL, metadata.CoverURL,
		metadata.BannerURL, metadata.BannerURL, metadata.ThreadURL, metadata.ThreadURL,
		metadata.CatalogVersion, metadata.CatalogVersion, metadata.LastFetchedAt.Unix(), time.Now().Unix(), threadID,
		olderOnly, metadata.LastFetchedAt.Unix(),
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
