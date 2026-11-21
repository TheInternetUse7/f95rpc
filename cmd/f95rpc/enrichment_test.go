package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"f95rpc/internal/config"
	"f95rpc/internal/importers"
	"f95rpc/internal/store"
)

type localAPITestTransport struct {
	base      *url.URL
	transport http.RoundTripper
}

func (t localAPITestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	// Never allow these command tests to contact the real API or transmit a real key.
	if request.URL.Host != "f95api.xyz" || request.Header.Get("X-API-Key") != "test-key" {
		return nil, fmt.Errorf("unexpected outbound request in command test: %s", request.URL)
	}
	copy := request.Clone(request.Context())
	copy.URL.Scheme, copy.URL.Host = t.base.Scheme, t.base.Host
	copy.Host = t.base.Host
	return t.transport.RoundTrip(copy)
}

func setupEnrichmentCommandTest(t *testing.T, handler http.HandlerFunc) (*config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("APPDATA", dir)
	t.Setenv("F95API_KEY", "")
	t.Setenv("DISCORD_CLIENT_ID", "")
	cfg := config.DefaultConfig()
	cfg.F95APIKey = "test-key"
	root := filepath.Join(dir, "games")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	cfg.Roots = []string{root}
	if err := config.Save(filepath.Join(dir, "config.yaml"), cfg); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	oldTransport := http.DefaultTransport
	http.DefaultTransport = localAPITestTransport{base: base, transport: oldTransport}
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	return cfg, root
}

func commandMetadataHandler(detailsCalls *atomic.Int32, failDetails bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/search":
			fmt.Fprint(w, `{"query":"Eternum","hits":[{"thread_id":82285,"title":"Search title","creator":"Search creator","url":"https://f95zone.to/threads/82285","category":"games","score":null,"cover":"https://example.com/search-cover.jpg","banner":null,"previews":[]}]}`)
		case "/v1/threads/batch":
			fmt.Fprint(w, `{"items":[{"thread_id":82285,"title":"Batch title","creator":"Batch creator","url":"https://f95zone.to/threads/82285","version":"0.8.5","category":"games","cover":null,"banner":null,"rating":4.9}],"removed":[]}`)
		case "/v1/f95/handiwork":
			detailsCalls.Add(1)
			if failDetails {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, `{"detail":"temporarily unavailable"}`)
				return
			}
			fmt.Fprint(w, `{"id":82285,"name":"Eternum","overview":"A game","authors":[{"name":"Caribdis","platforms":[]}],"category":"games","prefixes":["Ren'Py","Ongoing"],"engine":"Ren'Py","status":"Ongoing","version":"0.8.5","cover":"https://example.com/cover.jpg","banner":"https://example.com/banner.jpg","rating":{"average":4.9,"best":5,"count":12},"tags":[],"url":"https://f95zone.to/threads/82285","downloads":[],"previews":[]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func runEnrichmentCommand(t *testing.T, input string, command func()) string {
	t.Helper()
	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	if _, err := stdin.WriteString(input); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdout.Close() })
	oldStdin, oldStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdin, stdout
	defer func() { os.Stdin, os.Stdout = oldStdin, oldStdout }()
	command()
	if _, err := stdout.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

func loadCommandGame(t *testing.T, folder string) *store.GameRecord {
	t.Helper()
	s, err := store.Open("f95rpc.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	game, err := s.GetGameByFolder(folder)
	if err != nil || game == nil {
		t.Fatalf("load saved command game: game=%v err=%v", game, err)
	}
	return game
}

func requireFullCommandMetadata(t *testing.T, game *store.GameRecord) {
	t.Helper()
	if game.Title != "Eternum" || game.Creator != "Caribdis" || game.Engine != "Ren'Py" ||
		game.Status != "Ongoing" || game.Rating != 4.9 || game.CatalogVersion != "0.8.5" ||
		game.CoverURL != "https://example.com/cover.jpg" || game.BannerURL != "https://example.com/banner.jpg" ||
		game.LastFetchedAt.Unix() <= 0 || !game.Approved {
		t.Fatalf("command did not fetch full catalog metadata: %+v", game)
	}
}

func TestImportEnrichesApprovedGamesWithFullDetails(t *testing.T) {
	var detailsCalls atomic.Int32
	cfg, root := setupEnrichmentCommandTest(t, commandMetadataHandler(&detailsCalls, false))
	folder := filepath.Join(root, "Eternum")
	games := []importers.ImportedGame{
		{FolderPath: folder, ThreadID: 82285, Approved: true, Source: "f95checker", Name: "Local title", InstalledVersion: "0.4"},
		{FolderPath: filepath.Join(root, "second-install"), ThreadID: 82285, Approved: true, Source: "f95checker", Name: "Local title", InstalledVersion: "0.5"},
		{FolderPath: filepath.Join(root, "unapproved"), ThreadID: 29666, Approved: false, Source: "playnite", Name: "Pending game"},
	}
	runEnrichmentCommand(t, "", func() { saveAndEnrichGames(games, cfg, false, "F95Checker") })
	game := loadCommandGame(t, folder)
	requireFullCommandMetadata(t, game)
	if game.InstalledVersion != "0.4" || detailsCalls.Load() != 1 {
		t.Fatalf("import changed installed version or duplicated lookups: game=%+v calls=%d", game, detailsCalls.Load())
	}
	if pending := loadCommandGame(t, games[2].FolderPath); pending.LastFetchedAt.Unix() != 0 || pending.Approved {
		t.Fatalf("import enriched an unapproved link: %+v", pending)
	}
}

func TestImportKeepsIgnoredGamePreferences(t *testing.T) {
	var detailsCalls atomic.Int32
	cfg, root := setupEnrichmentCommandTest(t, commandMetadataHandler(&detailsCalls, false))
	folder := filepath.Join(root, "Eternum")
	s, err := store.Open("f95rpc.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGame(&store.GameRecord{
		FolderPath: folder, ThreadID: 82285, Approved: true, Ignored: true,
		Title: "Local title", CoverMode: "f95_logo", CustomTitle: "Private title",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	games := []importers.ImportedGame{{FolderPath: folder, ThreadID: 82285, Approved: true, Source: "f95checker", Name: "Local title"}}
	runEnrichmentCommand(t, "", func() { saveAndEnrichGames(games, cfg, false, "F95Checker") })
	game := loadCommandGame(t, folder)
	if !game.Ignored || game.CoverMode != "f95_logo" || game.CustomTitle != "Private title" || detailsCalls.Load() != 0 {
		t.Fatalf("re-import re-enabled/enriched an ignored game: game=%+v calls=%d", game, detailsCalls.Load())
	}
}

func TestScanApprovalFetchesFullDetails(t *testing.T) {
	for _, choice := range []string{"t\n82285\n", "1\n"} {
		t.Run(strings.TrimSpace(choice[:1]), func(t *testing.T) {
			var detailsCalls atomic.Int32
			_, root := setupEnrichmentCommandTest(t, commandMetadataHandler(&detailsCalls, false))
			folder := filepath.Join(root, "Eternum-0.4")
			if err := os.MkdirAll(filepath.Join(folder, "game"), 0755); err != nil {
				t.Fatal(err)
			}
			runEnrichmentCommand(t, choice, func() { handleScan(nil) })
			game := loadCommandGame(t, folder)
			requireFullCommandMetadata(t, game)
			if game.InstalledVersion != "0.4" || detailsCalls.Load() != 1 {
				t.Fatalf("scan did not retain local version: game=%+v calls=%d", game, detailsCalls.Load())
			}
		})
	}
}

func TestScanFailedEnrichmentStillSavesApprovalWithoutFetchTimestamp(t *testing.T) {
	var detailsCalls atomic.Int32
	_, root := setupEnrichmentCommandTest(t, commandMetadataHandler(&detailsCalls, true))
	folder := filepath.Join(root, "Eternum-0.4")
	if err := os.MkdirAll(folder, 0755); err != nil {
		t.Fatal(err)
	}
	output := runEnrichmentCommand(t, "1\n", func() { handleScan(nil) })
	game := loadCommandGame(t, folder)
	if !game.Approved || game.ThreadID != 82285 || game.LastFetchedAt.Unix() != 0 ||
		game.InstalledVersion != "0.4" || detailsCalls.Load() != 1 || !strings.Contains(output, "WARN") {
		t.Fatalf("failed lookup lost approval or prevented backfill: game=%+v calls=%d output=%q", game, detailsCalls.Load(), output)
	}
}

func TestBindDoesNotSetInstalledVersionFromCatalog(t *testing.T) {
	for _, installedVersion := range []string{"", "0.4"} {
		t.Run("installed="+installedVersion, func(t *testing.T) {
			var detailsCalls atomic.Int32
			_, root := setupEnrichmentCommandTest(t, commandMetadataHandler(&detailsCalls, false))
			folder := filepath.Join(root, "Eternum")
			if installedVersion != "" {
				s, err := store.Open("f95rpc.db")
				if err != nil {
					t.Fatal(err)
				}
				if err := s.UpsertGame(&store.GameRecord{
					FolderPath: folder, ThreadID: 82285, Approved: true, Title: "Local title",
					InstalledVersion: installedVersion, CoverMode: "f95_logo", CustomTitle: "Private title",
				}); err != nil {
					t.Fatal(err)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
			runEnrichmentCommand(t, "", func() { handleBind([]string{folder, "82285"}) })
			game := loadCommandGame(t, folder)
			requireFullCommandMetadata(t, game)
			if game.InstalledVersion != installedVersion {
				t.Fatalf("catalog version was stored as the installed version: %+v", game)
			}
			if installedVersion != "" && (game.CustomTitle != "Private title" || game.CoverMode != "f95_logo") {
				t.Fatalf("binding reset local display preferences: %+v", game)
			}
		})
	}
}

func TestBindFailedEnrichmentDoesNotReusePreviousThreadMetadata(t *testing.T) {
	var detailsCalls atomic.Int32
	_, root := setupEnrichmentCommandTest(t, commandMetadataHandler(&detailsCalls, true))
	folder := filepath.Join(root, "Eternum")
	s, err := store.Open("f95rpc.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGame(&store.GameRecord{
		FolderPath: folder, ThreadID: 29666, Approved: true, Title: "Old game", Engine: "Old engine",
		Status: "Completed", CatalogVersion: "old-version", LastFetchedAt: time.Now(), InstalledVersion: "0.4",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	runEnrichmentCommand(t, "", func() { handleBind([]string{folder, "82285"}) })
	game := loadCommandGame(t, folder)
	if game.ThreadID != 82285 || !game.Approved || game.LastFetchedAt.Unix() != 0 || game.Engine != "" ||
		game.Status != "" || game.CatalogVersion != "" || game.InstalledVersion != "0.4" || detailsCalls.Load() != 1 {
		t.Fatalf("failed rebind enrichment left previous thread details fresh: %+v", game)
	}
}

func TestScanMultipleInstallationsReuseFullDetails(t *testing.T) {
	var detailsCalls atomic.Int32
	_, root := setupEnrichmentCommandTest(t, commandMetadataHandler(&detailsCalls, false))
	for _, version := range []string{"0.4", "0.5"} {
		if err := os.MkdirAll(filepath.Join(root, "Eternum-"+version), 0755); err != nil {
			t.Fatal(err)
		}
	}
	runEnrichmentCommand(t, "1\n1\n", func() { handleScan(nil) })
	for _, version := range []string{"0.4", "0.5"} {
		game := loadCommandGame(t, filepath.Join(root, "Eternum-"+version))
		requireFullCommandMetadata(t, game)
		if game.InstalledVersion != version {
			t.Fatalf("cached metadata changed an installation version: %+v", game)
		}
	}
	if detailsCalls.Load() != 1 {
		t.Fatalf("same thread's full details were fetched %d times, want once", detailsCalls.Load())
	}
}

func TestImportDryRunDoesNotEnrichOrCreateDatabase(t *testing.T) {
	var detailsCalls atomic.Int32
	cfg, root := setupEnrichmentCommandTest(t, commandMetadataHandler(&detailsCalls, false))
	games := []importers.ImportedGame{{FolderPath: filepath.Join(root, "Eternum"), ThreadID: 82285, Approved: true, Source: "f95checker", Name: "Eternum"}}
	runEnrichmentCommand(t, "", func() { saveAndEnrichGames(games, cfg, true, "F95Checker") })
	if detailsCalls.Load() != 0 {
		t.Fatal("dry-run import made a metadata request")
	}
	if _, err := os.Stat("f95rpc.db"); !os.IsNotExist(err) {
		t.Fatalf("dry-run import created a database: %v", err)
	}
}

func TestApprovalWithoutAPIKeyStillSavesLocalBinding(t *testing.T) {
	for _, command := range []string{"import", "scan", "bind"} {
		t.Run(command, func(t *testing.T) {
			var detailsCalls atomic.Int32
			cfg, root := setupEnrichmentCommandTest(t, commandMetadataHandler(&detailsCalls, false))
			cfg.F95APIKey = ""
			if err := config.Save("config.yaml", cfg); err != nil {
				t.Fatal(err)
			}
			folder := filepath.Join(root, "Eternum-0.4")
			if err := os.MkdirAll(folder, 0755); err != nil {
				t.Fatal(err)
			}
			runEnrichmentCommand(t, "t\n82285\n", func() {
				switch command {
				case "import":
					games := []importers.ImportedGame{{FolderPath: folder, ThreadID: 82285, Approved: true, Name: "Eternum", Source: "f95checker", InstalledVersion: "0.4"}}
					saveAndEnrichGames(games, cfg, false, "F95Checker")
				case "scan":
					handleScan(nil)
				case "bind":
					handleBind([]string{folder, "82285"})
				}
			})
			game := loadCommandGame(t, folder)
			if !game.Approved || game.ThreadID != 82285 || game.LastFetchedAt.Unix() != 0 ||
				game.InstalledVersion != "0.4" || detailsCalls.Load() != 0 {
				t.Fatalf("optional API key prevented local approval: %+v", game)
			}
		})
	}
}
