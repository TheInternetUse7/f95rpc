package enrichment

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"f95rpc/internal/f95api"
	"f95rpc/internal/store"
)

// MaxAge is the refresh period for successfully fetched catalog details.
const MaxAge = 24 * time.Hour

const (
	retryDelay    = 5 * time.Minute
	maxRetryDelay = time.Hour
)

type attempt struct {
	next       time.Time
	retryDelay time.Duration
	metadata   *store.CatalogMetadata
}

type Enricher struct {
	store    *store.Store
	api      *f95api.Client
	now      func() time.Time
	interval time.Duration

	mu           sync.Mutex // Serialize passes and deduplicate requests across callers.
	attempts     map[int64]attempt
	blockedUntil time.Time
}

func New(s *store.Store, api *f95api.Client) *Enricher {
	return &Enricher{store: s, api: api, now: time.Now, interval: time.Minute, attempts: make(map[int64]attempt)}
}

// Run backfills immediately, then checks for missing/stale metadata every minute.
// The caller owns the goroutine and must cancel and join it before closing the store.
func (e *Enricher) Run(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_, err := e.Refresh(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil && onError != nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (e *Enricher) EnrichFolders(ctx context.Context, folders []string) (int, error) {
	var ids []int64
	var failures []error
	for _, folder := range folders {
		if err := ctx.Err(); err != nil {
			return 0, errors.Join(append(failures, err)...)
		}
		game, err := e.store.GetGameByFolder(folder)
		if err != nil {
			failures = append(failures, fmt.Errorf("load game %q for enrichment: %w", folder, err))
			continue
		}
		if game != nil && game.Approved && !game.Ignored && game.ThreadID > 0 {
			ids = append(ids, game.ThreadID)
		}
	}
	updated, err := e.enrichThreadIDs(ctx, ids)
	return updated, errors.Join(append(failures, err)...)
}

func (e *Enricher) Refresh(ctx context.Context) (int, error) {
	ids, err := e.store.ListEnrichmentThreadIDs(ctx, e.now().Add(-MaxAge))
	if err != nil {
		return 0, fmt.Errorf("list metadata enrichment candidates: %w", err)
	}
	return e.enrichThreadIDs(ctx, ids)
}

func (e *Enricher) enrichThreadIDs(ctx context.Context, ids []int64) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.now().Before(e.blockedUntil) {
		return 0, nil
	}
	seen := make(map[int64]bool)
	var updated int
	var failures []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return updated, errors.Join(append(failures, err)...)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		state := e.attempts[id]
		if e.now().Before(state.next) {
			if state.metadata != nil {
				count, err := e.store.ApplyCachedCatalogMetadata(ctx, id, *state.metadata)
				if err != nil {
					failures = append(failures, fmt.Errorf("apply cached thread %d metadata: %w", id, err))
				} else {
					updated += int(count)
				}
			}
			continue
		}

		handiwork, err := e.api.GetHandiworkContext(ctx, id)
		if err == nil && strings.TrimSpace(handiwork.Name) == "" {
			err = fmt.Errorf("handiwork response has no title")
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("enrich thread %d: %w", id, err))
			if ctx.Err() != nil {
				return updated, errors.Join(failures...)
			}
			if e.recordFailure(id, err) {
				break
			}
			continue
		}
		var authors []string
		for _, author := range handiwork.Authors {
			if name := strings.TrimSpace(author.Name); name != "" {
				authors = append(authors, name)
			}
		}
		metadata := store.CatalogMetadata{
			Title:          strings.TrimSpace(handiwork.Name),
			Creator:        strings.Join(authors, ", "),
			Engine:         strings.TrimSpace(handiwork.Engine),
			Status:         strings.TrimSpace(handiwork.Status),
			Rating:         handiwork.Rating.Average,
			ThreadURL:      handiwork.URL,
			CatalogVersion: strings.TrimSpace(handiwork.Version),
			LastFetchedAt:  e.now(),
		}
		if handiwork.Cover != nil {
			metadata.CoverURL = *handiwork.Cover
		}
		if handiwork.Banner != nil {
			metadata.BannerURL = *handiwork.Banner
		}
		count, err := e.store.UpdateCatalogMetadata(ctx, id, metadata)
		if err != nil {
			failures = append(failures, fmt.Errorf("save thread %d metadata: %w", id, err))
			if ctx.Err() != nil {
				return updated, errors.Join(failures...)
			}
			e.recordFailure(id, err)
			continue
		}
		updated += int(count)
		// Some catalog entries genuinely lack engine/status/media. Don't refetch them on every scan.
		e.attempts[id] = attempt{next: e.now().Add(MaxAge), metadata: &metadata}
	}
	return updated, errors.Join(failures...)
}

// recordFailure is called with e.mu held. True means the entire pass must pause.
func (e *Enricher) recordFailure(id int64, err error) bool {
	state := e.attempts[id]
	state.retryDelay = min(maxRetryDelay, max(retryDelay, state.retryDelay*2))
	delay := state.retryDelay
	var httpErr *f95api.HTTPError
	pause := false
	if errors.As(err, &httpErr) {
		delay = max(delay, httpErr.RetryAfter)
		pause = httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden ||
			httpErr.StatusCode == http.StatusTooManyRequests
	}
	state.next = e.now().Add(delay)
	e.attempts[id] = state
	if pause {
		e.blockedUntil = state.next
	}
	return pause
}
