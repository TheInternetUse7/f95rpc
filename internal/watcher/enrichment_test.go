package watcher

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"f95rpc/internal/config"
	"f95rpc/internal/f95api"
	"f95rpc/internal/presence"
	"f95rpc/internal/store"
)

func newEnrichmentTestWatcher(t *testing.T, handler http.HandlerFunc) (*Watcher, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "watcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.UpsertGame(&store.GameRecord{
		FolderPath: `C:\Games\Eternum`, ThreadID: 82285, Approved: true, Title: "Local title", InstalledVersion: "0.4",
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	api := f95api.NewClient("test-key")
	api.SetBaseURL(server.URL)
	cfg := config.DefaultConfig()
	cfg.F95APIKey = "test-key"
	cfg.PollIntervalSeconds = 3600
	w := NewWatcher(cfg, s, api, nil, &mockEnumerator{})
	t.Cleanup(w.Stop)
	return w, s
}

func TestWatcherStartupBackfillsApprovedGamesWithoutRunningProcesses(t *testing.T) {
	requested := make(chan struct{}, 1)
	w, s := newEnrichmentTestWatcher(t, func(w http.ResponseWriter, r *http.Request) {
		requested <- struct{}{}
		if r.URL.Path != "/v1/f95/handiwork" || r.URL.Query().Get("thread_id") != "82285" {
			t.Errorf("unexpected background request: %s", r.URL)
		}
		fmt.Fprint(w, `{"id":82285,"name":"Eternum","authors":[{"name":"Caribdis","platforms":[]}],"engine":"Ren'Py","status":"Ongoing","version":"0.8.5","rating":{"average":4.9,"best":5,"count":12},"cover":"https://example.com/cover.jpg","banner":null,"url":"https://f95zone.to/threads/82285"}`)
	})
	w.Start()
	select {
	case <-requested:
	case <-time.After(2 * time.Second):
		t.Fatal("starting the tray/daemon did not backfill an approved existing game")
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(2 * time.Second)
	for {
		game, err := s.GetGameByFolder(`C:\Games\Eternum`)
		if err != nil {
			t.Fatal(err)
		}
		if game.LastFetchedAt.Unix() > 0 {
			if game.Engine != "Ren'Py" || game.Status != "Ongoing" || game.Rating != 4.9 || game.InstalledVersion != "0.4" {
				t.Fatalf("background enrichment did not persist full details: %+v", game)
			}
			return
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatal("background details were fetched but not persisted")
		}
	}
}

func TestWatcherSlowEnrichmentDoesNotBlockPollingOrShutdown(t *testing.T) {
	requested := make(chan struct{})
	release := make(chan struct{})
	w, s := newEnrichmentTestWatcher(t, func(w http.ResponseWriter, r *http.Request) {
		close(requested)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	defer close(release)
	started := make(chan struct{})
	go func() {
		w.Start()
		close(started)
	}()
	select {
	case <-started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("watcher startup waited on the metadata API")
	}
	select {
	case <-requested:
	case <-time.After(2 * time.Second):
		t.Fatal("background metadata request never started")
	}
	if err := pollWatcherWithTimeout(t, w); err != nil {
		t.Fatal(err)
	}
	_ = w.IsStealthMode()
	stopped := make(chan struct{})
	go func() {
		w.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("watcher shutdown did not cancel/join enrichment")
	}
	game, err := s.GetGameByFolder(`C:\Games\Eternum`)
	if err != nil || game.LastFetchedAt.Unix() != 0 {
		t.Fatalf("canceled lookup was persisted: game=%v err=%v", game, err)
	}
}

func TestWatcherLiveSessionReloadsEnrichedMetadataOnNextPoll(t *testing.T) {
	w, _, provider := newCallbackTestWatcher(t)
	game, err := w.store.GetGameByFolder(`C:\Games\Eternum`)
	if err != nil {
		t.Fatal(err)
	}
	game.ThreadID = 82285
	game.InstalledVersion = "0.4"
	if err := w.store.UpsertGame(game); err != nil {
		t.Fatal(err)
	}
	var notifications int
	w.OnActiveGameChange = func(game *presence.ActiveGame, folder string) { notifications++ }
	if err := w.PollOnce(); err != nil {
		t.Fatal(err)
	}
	startedAt := provider.active.StartedAt
	if _, err := w.store.UpdateCatalogMetadata(context.Background(), 82285, store.CatalogMetadata{
		Title: "Updated catalog title", Creator: "Caribdis", Engine: "Ren'Py", Status: "Ongoing", Rating: 4.9,
		CatalogVersion: "0.8.5", CoverURL: "https://example.com/cover.jpg", LastFetchedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.PollOnce(); err != nil {
		t.Fatal(err)
	}
	if provider.active.Title != "Updated catalog title" || provider.active.Engine != "Ren'Py" ||
		provider.active.Status != "Ongoing" || provider.active.Rating != 4.9 || provider.active.InstalledVersion != "0.4" ||
		!provider.active.StartedAt.Equal(startedAt) || notifications != 2 {
		t.Fatalf("live presence/tray kept a stale session snapshot: active=%+v notifications=%d", provider.active, notifications)
	}
}

func TestWatcherEnrichmentErrorCallbackCanReadStateAndStop(t *testing.T) {
	w, _ := newEnrichmentTestWatcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	reported := make(chan error, 1)
	w.OnEnrichmentError = func(err error) {
		_ = w.IsStealthMode()
		_ = w.GetActiveSessions()
		w.Stop()
		reported <- err
	}
	w.Start()
	select {
	case err := <-reported:
		if err == nil {
			t.Fatal("enrichment error callback received nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("metadata error callback was missing or deadlocked reading/stopping the watcher")
	}
}
