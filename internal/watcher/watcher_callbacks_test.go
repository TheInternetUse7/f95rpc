package watcher

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"f95rpc/internal/config"
	"f95rpc/internal/presence"
	"f95rpc/internal/store"
)

func newCallbackTestWatcher(t *testing.T) (*Watcher, *mockEnumerator, *mockPresenceProvider) {
	t.Helper()

	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.UpsertGame(&store.GameRecord{
		FolderPath: `C:\Games\Eternum`,
		Title:      "Eternum",
		Approved:   true,
	}); err != nil {
		t.Fatalf("upsert game: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Roots = []string{`C:\Games`}
	enumerator := &mockEnumerator{
		procs: []ProcessInfo{{PID: 1234, ExePath: `C:\Games\Eternum\Eternum.exe`}},
	}
	provider := &mockPresenceProvider{}
	return NewWatcher(cfg, s, nil, provider, enumerator), enumerator, provider
}

func pollWatcherWithTimeout(t *testing.T, w *Watcher) error {
	t.Helper()

	done := make(chan error, 1)
	go func() { done <- w.PollOnce() }()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("PollOnce deadlocked while invoking a watcher callback")
		return nil
	}
}

func TestWatcher_GameExitCallbackCanReadState(t *testing.T) {
	w, enumerator, provider := newCallbackTestWatcher(t)
	if err := pollWatcherWithTimeout(t, w); err != nil {
		t.Fatalf("initial poll: %v", err)
	}

	var called, stealth bool
	var currentGame *presence.ActiveGame
	var sessionCount int
	w.OnActiveGameChange = func(game *presence.ActiveGame, folder string) {
		if game != nil {
			return
		}
		// The tray reads stealth mode here when switching its menu to Idle.
		stealth = w.IsStealthMode()
		currentGame = w.GetCurrentActiveGame()
		sessionCount = len(w.GetActiveSessions())
		called = true
	}

	enumerator.procs = nil
	if err := pollWatcherWithTimeout(t, w); err != nil {
		t.Fatalf("game exit poll: %v", err)
	}
	if !called {
		t.Fatal("expected an idle callback after the game exited")
	}
	if stealth || currentGame != nil || sessionCount != 0 {
		t.Fatalf("expected idle state, got stealth=%v, game=%v, sessions=%d", stealth, currentGame, sessionCount)
	}
	if provider.active != nil || provider.clears != 1 {
		t.Fatalf("expected presence cleared once, got active=%v, clears=%d", provider.active, provider.clears)
	}

	// The same watcher must remain usable when another game session starts.
	enumerator.procs = []ProcessInfo{{PID: 5678, ExePath: `C:\Games\Eternum\Eternum.exe`}}
	if err := pollWatcherWithTimeout(t, w); err != nil {
		t.Fatalf("game restart poll: %v", err)
	}
	if provider.active == nil || provider.active.PID != 5678 {
		t.Fatalf("expected restarted game presence, got %v", provider.active)
	}
}

func TestWatcher_GameStartedCallbackCanReadState(t *testing.T) {
	w, _, _ := newCallbackTestWatcher(t)
	var called bool
	var currentGame *presence.ActiveGame
	var sessionCount int
	w.OnActiveGameChange = func(game *presence.ActiveGame, folder string) {
		_ = w.IsStealthMode()
		currentGame = w.GetCurrentActiveGame()
		sessionCount = len(w.GetActiveSessions())
		called = true
	}

	if err := pollWatcherWithTimeout(t, w); err != nil {
		t.Fatalf("game start poll: %v", err)
	}
	if !called || currentGame == nil || currentGame.Title != "Eternum" || sessionCount != 1 {
		t.Fatalf("expected active state in callback, got called=%v, game=%v, sessions=%d", called, currentGame, sessionCount)
	}
}

func TestWatcher_GameStoppedCallbackCanReadState(t *testing.T) {
	w, enumerator, _ := newCallbackTestWatcher(t)
	if err := pollWatcherWithTimeout(t, w); err != nil {
		t.Fatalf("initial poll: %v", err)
	}

	var stoppedFolder string
	var currentGame *presence.ActiveGame
	var sessionCount int
	w.OnGameStopped = func(folder string) {
		stoppedFolder = folder
		currentGame = w.GetCurrentActiveGame()
		sessionCount = len(w.GetActiveSessions())
	}

	enumerator.procs = nil
	if err := pollWatcherWithTimeout(t, w); err != nil {
		t.Fatalf("game exit poll: %v", err)
	}
	if stoppedFolder != store.NormalizePath(`C:\Games\Eternum`) || currentGame != nil || sessionCount != 0 {
		t.Fatalf("expected ended session in callback, got folder=%q, game=%v, sessions=%d", stoppedFolder, currentGame, sessionCount)
	}
}

func TestWatcher_IdleCallbackRunsAfterPresenceCleared(t *testing.T) {
	w, enumerator, provider := newCallbackTestWatcher(t)
	if err := pollWatcherWithTimeout(t, w); err != nil {
		t.Fatalf("initial poll: %v", err)
	}

	var events []string
	var clearedBeforeIdle bool
	w.OnGameStopped = func(folder string) { events = append(events, "stopped") }
	w.OnActiveGameChange = func(game *presence.ActiveGame, folder string) {
		if game == nil {
			clearedBeforeIdle = provider.active == nil && provider.clears == 1
			events = append(events, "idle")
		}
	}

	enumerator.procs = nil
	if err := pollWatcherWithTimeout(t, w); err != nil {
		t.Fatalf("game exit poll: %v", err)
	}
	if !clearedBeforeIdle {
		t.Fatal("reported Idle before clearing Discord presence")
	}
	if len(events) != 2 || events[0] != "stopped" || events[1] != "idle" {
		t.Fatalf("expected stopped then idle callbacks, got %v", events)
	}
}

func TestWatcher_FailedClearDoesNotReportIdle(t *testing.T) {
	w, enumerator, provider := newCallbackTestWatcher(t)
	if err := pollWatcherWithTimeout(t, w); err != nil {
		t.Fatalf("initial poll: %v", err)
	}

	var idleEvents int
	w.OnActiveGameChange = func(game *presence.ActiveGame, folder string) {
		if game == nil {
			idleEvents++
		}
	}
	clearErr := errors.New("Discord clear failed")
	provider.clearErr = clearErr
	enumerator.procs = nil
	if err := pollWatcherWithTimeout(t, w); !errors.Is(err, clearErr) {
		t.Fatalf("expected clear failure, got %v", err)
	}
	if idleEvents != 0 {
		t.Fatal("reported Idle despite a failed presence clear")
	}

	provider.clearErr = nil
	if err := pollWatcherWithTimeout(t, w); err != nil {
		t.Fatalf("retry clear: %v", err)
	}
	if idleEvents != 1 || provider.active != nil || provider.clears != 2 {
		t.Fatalf("expected successful retry to report Idle once, got events=%d, active=%v, clears=%d", idleEvents, provider.active, provider.clears)
	}
}
