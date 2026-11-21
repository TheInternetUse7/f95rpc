package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func openMetadataTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestListEnrichmentThreadIDsSelectsMissingAndStaleApprovedGames(t *testing.T) {
	s := openMetadataTestStore(t)
	now := time.Now().Truncate(time.Second)
	for _, test := range []struct {
		folder   string
		threadID int64
		approved bool
		ignored  bool
		fetched  time.Time
		engine   string
	}{
		{"never-fetched", 1, true, false, time.Time{}, "Ren'Py"},
		{"stale", 2, true, false, now.Add(-48 * time.Hour), "Ren'Py"},
		{"fresh-partial-search", 3, true, false, now, ""},
		{"duplicate-thread", 3, true, false, time.Time{}, ""},
		{"fresh-complete", 4, true, false, now, "Ren'Py"},
		{"unapproved", 5, false, false, time.Time{}, ""},
		{"ignored", 6, true, true, time.Time{}, ""},
		{"no-thread", 0, true, false, time.Time{}, ""},
		{"invalid-thread", -1, true, false, time.Time{}, ""},
	} {
		if err := s.UpsertGame(&GameRecord{
			FolderPath:     filepath.Join(t.TempDir(), test.folder),
			ThreadID:       test.threadID,
			Approved:       test.approved,
			Ignored:        test.ignored,
			Title:          "Local title",
			Engine:         test.engine,
			Status:         "Ongoing",
			CatalogVersion: "1.0",
			LastFetchedAt:  test.fetched,
		}); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := s.ListEnrichmentThreadIDs(context.Background(), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{1, 2, 3}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("enrichment candidates = %v, want %v", ids, want)
	}
}

func TestUpdateCatalogMetadataPreservesLocalFields(t *testing.T) {
	s := openMetadataTestStore(t)
	original := GameRecord{
		FolderPath:       filepath.Join(t.TempDir(), "game"),
		ThreadID:         82285,
		Approved:         true,
		Source:           "f95checker",
		Title:            "Local title",
		InstalledVersion: "0.4-installed",
		DetectedEngine:   "Ren'Py-local",
		ExeHint:          "game.exe",
		CoverMode:        "f95_logo",
		CustomTitle:      "My private title",
	}
	if err := s.UpsertGame(&original); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetGameByFolder(original.FolderPath)
	if err != nil {
		t.Fatal(err)
	}
	fetchedAt := time.Now().Truncate(time.Second)
	metadata := CatalogMetadata{
		Title:          "Eternum",
		Creator:        "Caribdis",
		Engine:         "Ren'Py",
		Status:         "Ongoing",
		Rating:         4.9,
		CoverURL:       "https://example.com/cover.jpg",
		BannerURL:      "https://example.com/banner.jpg",
		ThreadURL:      "https://f95zone.to/threads/82285",
		CatalogVersion: "0.8.5",
		LastFetchedAt:  fetchedAt,
	}
	count, err := s.UpdateCatalogMetadata(context.Background(), 82285, metadata)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.GetGameByFolder(original.FolderPath)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || after.Title != metadata.Title || after.Creator != metadata.Creator ||
		after.Engine != metadata.Engine || after.Status != metadata.Status || after.Rating != metadata.Rating ||
		after.CoverURL != metadata.CoverURL || after.BannerURL != metadata.BannerURL ||
		after.ThreadURL != metadata.ThreadURL || after.CatalogVersion != metadata.CatalogVersion ||
		!after.LastFetchedAt.Equal(fetchedAt) {
		t.Fatalf("catalog details not persisted: count=%d game=%+v", count, after)
	}
	if after.ID != before.ID || after.FolderPath != before.FolderPath || after.ThreadID != before.ThreadID ||
		after.Approved != before.Approved || after.Ignored != before.Ignored || after.Source != before.Source ||
		after.InstalledVersion != before.InstalledVersion || after.DetectedEngine != before.DetectedEngine ||
		after.ExeHint != before.ExeHint || after.CoverMode != before.CoverMode || after.CustomTitle != before.CustomTitle ||
		!after.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("enrichment overwrote local state: before=%+v after=%+v", before, after)
	}
}

func TestUpdateCatalogMetadataOnlyTouchesCurrentApprovedThread(t *testing.T) {
	s := openMetadataTestStore(t)
	for _, test := range []struct {
		folder   string
		threadID int64
		approved bool
		ignored  bool
	}{
		{"first-install", 82285, true, false},
		{"second-install", 82285, true, false},
		{"rebound", 29666, true, false},
		{"unapproved", 82285, false, false},
		{"ignored", 82285, true, true},
	} {
		folder := filepath.Join(t.TempDir(), test.folder)
		if err := s.UpsertGame(&GameRecord{
			FolderPath: folder,
			ThreadID:   test.threadID,
			Approved:   test.approved,
			Ignored:    test.ignored,
			Title:      "Local title",
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			game, err := s.GetGameByFolder(folder)
			if err != nil {
				t.Fatal(err)
			}
			want := "Local title"
			if test.threadID == 82285 && test.approved && !test.ignored {
				want = "Eternum"
			}
			if game.Title != want {
				t.Errorf("%s title = %q, want %q", test.folder, game.Title, want)
			}
		})
	}
	count, err := s.UpdateCatalogMetadata(context.Background(), 82285, CatalogMetadata{
		Title:         "Eternum",
		LastFetchedAt: time.Now(),
	})
	if err != nil || count != 2 {
		t.Fatalf("expected both approved installs updated, got count=%d err=%v", count, err)
	}
}

func TestUpdateCatalogMetadataRetainsKnownFieldsForEmptyAPIValues(t *testing.T) {
	s := openMetadataTestStore(t)
	original := GameRecord{
		FolderPath:     filepath.Join(t.TempDir(), "game"),
		ThreadID:       82285,
		Approved:       true,
		Title:          "Eternum",
		Creator:        "Caribdis",
		Engine:         "Ren'Py",
		Status:         "Ongoing",
		Rating:         4.9,
		CoverURL:       "https://example.com/cover.jpg",
		BannerURL:      "https://example.com/banner.jpg",
		ThreadURL:      "https://f95zone.to/threads/82285",
		CatalogVersion: "0.8.5",
	}
	if err := s.UpsertGame(&original); err != nil {
		t.Fatal(err)
	}
	fetchedAt := time.Now().Truncate(time.Second)
	count, err := s.UpdateCatalogMetadata(context.Background(), 82285, CatalogMetadata{LastFetchedAt: fetchedAt})
	if err != nil {
		t.Fatal(err)
	}
	game, err := s.GetGameByFolder(original.FolderPath)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || game.Title != original.Title || game.Creator != original.Creator ||
		game.Engine != original.Engine || game.Status != original.Status || game.Rating != original.Rating ||
		game.CoverURL != original.CoverURL || game.BannerURL != original.BannerURL ||
		game.ThreadURL != original.ThreadURL || game.CatalogVersion != original.CatalogVersion ||
		!game.LastFetchedAt.Equal(fetchedAt) {
		t.Fatalf("partial API data cleared known fields: %+v", game)
	}
}

func TestApplyCachedCatalogMetadataDoesNotOverwriteNewerDetails(t *testing.T) {
	s := openMetadataTestStore(t)
	fetchedAt := time.Now().Truncate(time.Second)
	for _, test := range []struct {
		folder  string
		fetched time.Time
	}{
		{"new-install", time.Time{}},
		{"same-fetch", fetchedAt},
		{"newer-fetch", fetchedAt.Add(time.Hour)},
	} {
		folder := filepath.Join(t.TempDir(), test.folder)
		if err := s.UpsertGame(&GameRecord{
			FolderPath: folder, ThreadID: 82285, Approved: true, Title: test.folder, LastFetchedAt: test.fetched,
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			game, err := s.GetGameByFolder(folder)
			if err != nil {
				t.Fatal(err)
			}
			want := test.folder
			if test.folder == "new-install" {
				want = "Cached title"
			}
			if game.Title != want {
				t.Errorf("%s title = %q, want %q", test.folder, game.Title, want)
			}
		})
	}
	count, err := s.ApplyCachedCatalogMetadata(context.Background(), 82285, CatalogMetadata{
		Title: "Cached title", LastFetchedAt: fetchedAt,
	})
	if err != nil || count != 1 {
		t.Fatalf("cached metadata must update only older records: count=%d err=%v", count, err)
	}
}

func TestUpsertGameClearsPreviousThreadCatalogMetadataOnRebind(t *testing.T) {
	s := openMetadataTestStore(t)
	folder := filepath.Join(t.TempDir(), "game")
	if err := s.UpsertGame(&GameRecord{
		FolderPath: folder, ThreadID: 29666, Approved: true, Source: "old binding", Title: "Old catalog title",
		Creator: "Old creator", Engine: "Old engine", Status: "Completed", Rating: 4.5,
		CoverURL: "https://example.com/old-cover.jpg", BannerURL: "https://example.com/old-banner.jpg",
		ThreadURL: "https://f95zone.to/threads/29666", CatalogVersion: "old-catalog-version", LastFetchedAt: time.Now(),
		InstalledVersion: "my-installed-version", DetectedEngine: "Local engine", ExeHint: "game.exe",
		CoverMode: "f95_logo", CustomTitle: "My custom title",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGame(&GameRecord{
		FolderPath: folder, ThreadID: 82285, Approved: true, Source: "manual_bind", Title: "New local title",
		ThreadURL: "https://f95zone.to/threads/82285",
	}); err != nil {
		t.Fatal(err)
	}
	game, err := s.GetGameByFolder(folder)
	if err != nil {
		t.Fatal(err)
	}
	if game.Title != "New local title" || game.Creator != "" || game.Engine != "" || game.Status != "" ||
		game.Rating != 0 || game.CoverURL != "" || game.BannerURL != "" || game.CatalogVersion != "" ||
		game.LastFetchedAt.Unix() != 0 || game.ThreadURL != "https://f95zone.to/threads/82285" {
		t.Fatalf("rebind retained another thread's catalog/fetch timestamp: %+v", game)
	}
	if game.InstalledVersion != "my-installed-version" || game.DetectedEngine != "Local engine" ||
		game.ExeHint != "game.exe" || game.CoverMode != "f95_logo" || game.CustomTitle != "My custom title" {
		t.Fatalf("rebind reset local installation preferences: %+v", game)
	}
}
