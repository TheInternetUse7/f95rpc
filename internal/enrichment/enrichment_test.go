package enrichment

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"f95rpc/internal/f95api"
	"f95rpc/internal/store"
)

// Mirrors the full-details response, including null/optional and otherwise unused fields.
const handiworkJSON = `{
	"id":%d,"name":"Eternum","overview":"A game overview",
	"authors":[{"name":"Caribdis","platforms":[]},{"name":"Artist","platforms":[]}],
	"category":"games","changelog":[],"cover":"https://example.com/cover.jpg",
	"banner":"https://example.com/banner.jpg","previews":[],"last_thread_update":null,
	"thread_publishing_date":null,"last_promoted":null,"promotion_banned":false,
	"prefixes":["Ren'Py","Ongoing"],"rating":{"average":4.9,"best":5,"count":12},
	"tags":["story rich"],"url":"https://f95zone.to/threads/%d","downloads":[],
	"censored":false,"engine":"Ren'Py","genre":[],"installation":"","language":["English"],
	"last_release":null,"os":["Windows"],"status":"Ongoing","version":"0.8.5",
	"pages":"","resolution":[],"length":"","asset_link":"","associated_assets":[],
	"compatible_software":"","included_assets":[],"official_links":[],"sku":""
}`

func writeHandiwork(w http.ResponseWriter, id int64) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, handiworkJSON, id, id)
}

func newTestEnricher(t *testing.T, handler http.HandlerFunc) (*Enricher, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	api := f95api.NewClient("test-key")
	api.SetBaseURL(server.URL)
	return New(s, api), s
}

func saveTestGame(t *testing.T, s *store.Store, game *store.GameRecord) {
	t.Helper()
	if err := s.UpsertGame(game); err != nil {
		t.Fatal(err)
	}
}

func loadTestGame(t *testing.T, s *store.Store, folder string) *store.GameRecord {
	t.Helper()
	game, err := s.GetGameByFolder(folder)
	if err != nil || game == nil {
		t.Fatalf("load game %q: game=%v err=%v", folder, game, err)
	}
	return game
}

func TestEnrichFoldersFetchesFullDetailsOncePerApprovedThread(t *testing.T) {
	var calls atomic.Int32
	e, s := newTestEnricher(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/f95/handiwork" || r.URL.Query().Get("thread_id") != "82285" ||
			r.Header.Get("X-API-Key") != "test-key" {
			t.Errorf("unexpected metadata request: %s", r.URL)
		}
		writeHandiwork(w, 82285)
	})
	var folders []string
	for i, test := range []struct {
		threadID int64
		approved bool
		ignored  bool
	}{
		{82285, true, false},
		{82285, true, false},
		{29666, false, false},
		{29667, true, true},
		{0, true, false},
	} {
		folder := filepath.Join(t.TempDir(), strconv.Itoa(i))
		folders = append(folders, folder)
		saveTestGame(t, s, &store.GameRecord{
			FolderPath:       folder,
			ThreadID:         test.threadID,
			Approved:         test.approved,
			Ignored:          test.ignored,
			Source:           "f95checker",
			Title:            "Local title",
			InstalledVersion: "0.4-installed",
			DetectedEngine:   "Local engine",
			ExeHint:          "game.exe",
			CoverMode:        "f95_logo",
			CustomTitle:      "My title",
		})
	}
	updated, err := e.EnrichFolders(context.Background(), folders)
	if err != nil || updated != 2 || calls.Load() != 1 {
		t.Fatalf("expected two installs enriched by one lookup: updated=%d calls=%d err=%v", updated, calls.Load(), err)
	}
	for _, folder := range folders[:2] {
		game := loadTestGame(t, s, folder)
		if game.Title != "Eternum" || game.Creator != "Caribdis, Artist" || game.Engine != "Ren'Py" ||
			game.Status != "Ongoing" || game.Rating != 4.9 || game.CatalogVersion != "0.8.5" ||
			game.CoverURL != "https://example.com/cover.jpg" || game.BannerURL != "https://example.com/banner.jpg" ||
			game.ThreadURL != "https://f95zone.to/threads/82285" || game.LastFetchedAt.Unix() <= 0 {
			t.Errorf("full API metadata missing: %+v", game)
		}
		if game.InstalledVersion != "0.4-installed" || game.DetectedEngine != "Local engine" ||
			game.ExeHint != "game.exe" || game.CoverMode != "f95_logo" || game.CustomTitle != "My title" ||
			game.Source != "f95checker" || !game.Approved || game.Ignored {
			t.Errorf("local installation state changed: %+v", game)
		}
	}
	for _, folder := range folders[2:] {
		game := loadTestGame(t, s, folder)
		if game.Title != "Local title" || game.LastFetchedAt.Unix() != 0 {
			t.Errorf("ineligible game was enriched: %+v", game)
		}
	}
}

func TestRefreshBackfillsExistingMissingAndStaleMetadata(t *testing.T) {
	var calls atomic.Int32
	e, s := newTestEnricher(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		id, err := strconv.ParseInt(r.URL.Query().Get("thread_id"), 10, 64)
		if err != nil || id == 4 {
			t.Errorf("unexpected thread ID in backfill: %s", r.URL)
		}
		writeHandiwork(w, id)
	})
	now := time.Now().Truncate(time.Second)
	for _, test := range []struct {
		id      int64
		fetched time.Time
		engine  string
	}{
		{1, time.Time{}, "Ren'Py"},
		{2, now.Add(-48 * time.Hour), "Ren'Py"},
		{3, now, ""}, // Previously stamped by a partial search/batch lookup.
		{4, now, "Ren'Py"},
	} {
		saveTestGame(t, s, &store.GameRecord{
			FolderPath:    filepath.Join(t.TempDir(), strconv.FormatInt(test.id, 10)),
			ThreadID:      test.id,
			Approved:      true,
			Title:         "Existing title",
			Engine:        test.engine,
			Status:        "Ongoing",
			LastFetchedAt: test.fetched,
		})
	}
	updated, err := e.Refresh(context.Background())
	if err != nil || updated != 3 || calls.Load() != 3 {
		t.Fatalf("existing DB was not backfilled: updated=%d calls=%d err=%v", updated, calls.Load(), err)
	}
}

func TestEnrichFoldersUsesCachedDetailsForNewInstallation(t *testing.T) {
	var calls atomic.Int32
	e, s := newTestEnricher(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeHandiwork(w, 82285)
	})
	first := filepath.Join(t.TempDir(), "first")
	saveTestGame(t, s, &store.GameRecord{FolderPath: first, ThreadID: 82285, Approved: true, Title: "First install"})
	if updated, err := e.EnrichFolders(context.Background(), []string{first}); err != nil || updated != 1 {
		t.Fatalf("initial lookup failed: updated=%d err=%v", updated, err)
	}
	second := filepath.Join(t.TempDir(), "second")
	saveTestGame(t, s, &store.GameRecord{
		FolderPath: second, ThreadID: 82285, Approved: true, Title: "Second install", InstalledVersion: "0.4",
	})
	if updated, err := e.EnrichFolders(context.Background(), []string{second}); err != nil || updated != 1 || calls.Load() != 1 {
		t.Fatalf("cached thread left a new install unenriched: updated=%d calls=%d err=%v", updated, calls.Load(), err)
	}
	if game := loadTestGame(t, s, second); game.Engine != "Ren'Py" || game.Status != "Ongoing" ||
		game.InstalledVersion != "0.4" || game.LastFetchedAt.Unix() <= 0 {
		t.Fatalf("cached details were not applied to the new install: %+v", game)
	}
}

func TestRefreshRetriesFailuresWithBackoffWithoutStampingSuccess(t *testing.T) {
	var calls atomic.Int32
	e, s := newTestEnricher(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"detail":"temporarily unavailable"}`)
			return
		}
		writeHandiwork(w, 82285)
	})
	now := time.Now().Truncate(time.Second)
	e.now = func() time.Time { return now }
	folder := filepath.Join(t.TempDir(), "game")
	saveTestGame(t, s, &store.GameRecord{FolderPath: folder, ThreadID: 82285, Approved: true, Title: "Local title"})

	if updated, err := e.Refresh(context.Background()); updated != 0 || err == nil {
		t.Fatalf("expected initial API failure: updated=%d err=%v", updated, err)
	}
	if game := loadTestGame(t, s, folder); game.Title != "Local title" || game.LastFetchedAt.Unix() != 0 {
		t.Fatalf("failed lookup was saved as successful: %+v", game)
	}
	now = now.Add(4 * time.Minute)
	if _, err := e.Refresh(context.Background()); err != nil || calls.Load() != 1 {
		t.Fatalf("failed thread was retried too soon: calls=%d err=%v", calls.Load(), err)
	}
	now = now.Add(time.Minute)
	if _, err := e.Refresh(context.Background()); err == nil || calls.Load() != 2 {
		t.Fatalf("expected first retry at five minutes: calls=%d err=%v", calls.Load(), err)
	}
	now = now.Add(9 * time.Minute)
	if _, err := e.Refresh(context.Background()); err != nil || calls.Load() != 2 {
		t.Fatalf("second failure did not increase retry delay: calls=%d err=%v", calls.Load(), err)
	}
	now = now.Add(time.Minute)
	if updated, err := e.Refresh(context.Background()); err != nil || updated != 1 || calls.Load() != 3 {
		t.Fatalf("expected successful retry at ten minutes: updated=%d calls=%d err=%v", updated, calls.Load(), err)
	}
	if game := loadTestGame(t, s, folder); game.Title != "Eternum" || !game.LastFetchedAt.Equal(now) {
		t.Fatalf("retry did not persist fresh metadata: %+v", game)
	}
}

func TestRefreshDoesNotRepeatedlyFetchLegitimatelyMissingFields(t *testing.T) {
	var calls atomic.Int32
	e, s := newTestEnricher(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"id":82285,"name":"Eternum","authors":[],"engine":"","status":"","version":"","cover":null,"banner":null,"rating":{"average":0,"best":5,"count":0},"url":"https://f95zone.to/threads/82285"}`)
	})
	now := time.Now().Truncate(time.Second)
	e.now = func() time.Time { return now }
	folder := filepath.Join(t.TempDir(), "game")
	saveTestGame(t, s, &store.GameRecord{FolderPath: folder, ThreadID: 82285, Approved: true, Title: "Local title"})
	if updated, err := e.Refresh(context.Background()); err != nil || updated != 1 {
		t.Fatalf("initial full-details lookup failed: updated=%d err=%v", updated, err)
	}
	if updated, err := e.Refresh(context.Background()); err != nil || updated != 0 || calls.Load() != 1 {
		t.Fatalf("optional missing API fields caused repeated requests: updated=%d calls=%d err=%v", updated, calls.Load(), err)
	}
	now = now.Add(24 * time.Hour)
	if updated, err := e.Refresh(context.Background()); err != nil || updated != 1 || calls.Load() != 2 {
		t.Fatalf("missing fields were not refreshed after becoming stale: updated=%d calls=%d err=%v", updated, calls.Load(), err)
	}
}

func TestRefreshPausesPassOnAuthenticationOrRateLimitErrors(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		retryAfter string
		delay      time.Duration
	}{
		{"unauthorized", http.StatusUnauthorized, "", 5 * time.Minute},
		{"forbidden", http.StatusForbidden, "", 5 * time.Minute},
		{"rate limited", http.StatusTooManyRequests, "600", 10 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			e, s := newTestEnricher(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.Header().Set("Retry-After", test.retryAfter)
					w.WriteHeader(test.status)
					fmt.Fprint(w, `{"detail":"request failed"}`)
					return
				}
				id, _ := strconv.ParseInt(r.URL.Query().Get("thread_id"), 10, 64)
				writeHandiwork(w, id)
			})
			now := time.Now().Truncate(time.Second)
			e.now = func() time.Time { return now }
			for _, id := range []int64{1, 2} {
				saveTestGame(t, s, &store.GameRecord{
					FolderPath: filepath.Join(t.TempDir(), strconv.FormatInt(id, 10)),
					ThreadID:   id,
					Approved:   true,
					Title:      "Local title",
				})
			}
			if updated, err := e.Refresh(context.Background()); err == nil || updated != 0 || calls.Load() != 1 {
				t.Fatalf("continued fetching after HTTP %d: updated=%d calls=%d err=%v", test.status, updated, calls.Load(), err)
			}
			now = now.Add(test.delay - time.Second)
			if _, err := e.Refresh(context.Background()); err != nil || calls.Load() != 1 {
				t.Fatalf("ignored API cooldown/Retry-After: calls=%d err=%v", calls.Load(), err)
			}
			now = now.Add(time.Second)
			if updated, err := e.Refresh(context.Background()); err != nil || updated != 2 || calls.Load() != 3 {
				t.Fatalf("did not resume after cooldown: updated=%d calls=%d err=%v", updated, calls.Load(), err)
			}
		})
	}
}

func TestRefreshRejectsEmptyDetailsWithoutStampingSuccess(t *testing.T) {
	e, s := newTestEnricher(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":82285,"name":"","authors":[],"engine":"","status":"","version":"","cover":null,"banner":null,"rating":{"average":0,"best":5,"count":0}}`)
	})
	folder := filepath.Join(t.TempDir(), "game")
	saveTestGame(t, s, &store.GameRecord{FolderPath: folder, ThreadID: 82285, Approved: true, Title: "Local title"})
	if updated, err := e.Refresh(context.Background()); updated != 0 || err == nil {
		t.Fatalf("empty detail payload was treated as successful: updated=%d err=%v", updated, err)
	}
	if game := loadTestGame(t, s, folder); game.Title != "Local title" || game.LastFetchedAt.Unix() != 0 {
		t.Fatalf("empty payload was persisted: %+v", game)
	}
}

func TestEnrichmentPreservesEditsMadeWhileRequestIsInFlight(t *testing.T) {
	for _, action := range []string{"preferences", "rebind", "ignore", "unapprove"} {
		t.Run(action, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			e, s := newTestEnricher(t, func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-release
				writeHandiwork(w, 82285)
			})
			defer close(release)
			folder := filepath.Join(t.TempDir(), "game")
			saveTestGame(t, s, &store.GameRecord{
				FolderPath: folder,
				ThreadID:   82285,
				Approved:   true,
				Title:      "Local title",
			})
			type result struct {
				updated int
				err     error
			}
			done := make(chan result, 1)
			go func() {
				updated, err := e.EnrichFolders(context.Background(), []string{folder})
				done <- result{updated, err}
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("metadata request never started")
			}
			game := loadTestGame(t, s, folder)
			game.Source = "edited locally"
			game.InstalledVersion = "my-installed-version"
			game.CoverMode = "f95_logo"
			game.CustomTitle = "my custom title"
			switch action {
			case "rebind":
				game.ThreadID = 29666
			case "ignore":
				game.Ignored = true
			case "unapprove":
				game.Approved = false
			}
			saveTestGame(t, s, game)
			// Release without closing twice during deferred cleanup.
			release <- struct{}{}
			select {
			case result := <-done:
				wantUpdated := 0
				if action == "preferences" {
					wantUpdated = 1
				}
				if result.err != nil || result.updated != wantUpdated {
					t.Fatalf("unexpected enrichment result: %+v", result)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("enrichment did not finish")
			}
			after := loadTestGame(t, s, folder)
			if after.ThreadID != game.ThreadID || after.Approved != game.Approved || after.Ignored != game.Ignored ||
				after.Source != game.Source || after.InstalledVersion != game.InstalledVersion ||
				after.CoverMode != game.CoverMode || after.CustomTitle != game.CustomTitle {
				t.Fatalf("in-flight metadata overwrote a local edit: before=%+v after=%+v", game, after)
			}
			if action != "preferences" && (after.Title != "Local title" || after.LastFetchedAt.Unix() != 0) {
				t.Fatalf("stale fetch crossed a rebind/disable: %+v", after)
			}
		})
	}
}

func TestRunStartsImmediatelyAndCancelsPendingRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	e, s := newTestEnricher(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	defer close(release)
	folder := filepath.Join(t.TempDir(), "game")
	saveTestGame(t, s, &store.GameRecord{FolderPath: folder, ThreadID: 82285, Approved: true, Title: "Local title"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var errorsReported atomic.Int32
	done := make(chan struct{})
	go func() {
		e.Run(ctx, func(error) { errorsReported.Add(1) })
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("background backfill did not start immediately")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("background enrichment did not exit on shutdown")
	}
	if errorsReported.Load() != 0 || loadTestGame(t, s, folder).LastFetchedAt.Unix() != 0 {
		t.Fatal("shutdown cancellation was reported/saved as a completed lookup")
	}
}

func TestRunDiscoversGamesAddedAfterStartup(t *testing.T) {
	requests := make(chan int64, 4)
	e, s := newTestEnricher(t, func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.URL.Query().Get("thread_id"), 10, 64)
		requests <- id
		writeHandiwork(w, id)
	})
	e.interval = 10 * time.Millisecond
	first := filepath.Join(t.TempDir(), "first")
	saveTestGame(t, s, &store.GameRecord{FolderPath: first, ThreadID: 1, Approved: true, Title: "First"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.Run(ctx, nil)
		close(done)
	}()
	defer func() { cancel(); <-done }()
	select {
	case id := <-requests:
		if id != 1 {
			t.Fatalf("first lookup = %d, want 1", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("initial lookup did not run")
	}
	// Let the initial write finish before adding a new library entry.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(2 * time.Second)
	for loadTestGame(t, s, first).LastFetchedAt.Unix() == 0 {
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatal("initial metadata was not saved")
		}
	}
	second := filepath.Join(t.TempDir(), "second")
	saveTestGame(t, s, &store.GameRecord{FolderPath: second, ThreadID: 2, Approved: true, Title: "Second"})
	select {
	case id := <-requests:
		if id != 2 {
			t.Fatalf("periodic lookup = %d, want 2", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("background backfill ignored a game added after startup")
	}
}
