package watcher

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"f95rpc/internal/config"
	"f95rpc/internal/f95api"
	"f95rpc/internal/importers"
	"f95rpc/internal/presence"
	"f95rpc/internal/scanner"
	"f95rpc/internal/store"
)

// Watcher manages background process polling, session tracking, and Discord Rich Presence synchronization.
type Watcher struct {
	cfg                   *config.Config
	store                 *store.Store
	api                   *f95api.Client
	presence              presence.PresenceProvider
	enumerator            ProcessEnumerator
	activeSessions        map[string]*ActiveSession // key: normalized folder path
	lockedFolder          string                    // folder currently locked for "lock_first" mode
	currentActiveGame     *presence.ActiveGame
	currentActiveFolder   string
	presenceActive        bool
	stealthMode           bool
	discoveredUnconfirmed map[string]bool // prevents re-searching the same unconfirmed folder repeatedly

	mu       sync.Mutex
	stopChan chan struct{}
	running  bool

	// Event hooks (useful for CLI logging or UI integration).
	// Poll callbacks run synchronously after releasing the watcher lock.
	OnActiveGameChange    func(game *presence.ActiveGame, folder string)
	OnGameStopped         func(folder string)
	OnUnconfirmedDetected func(folder string, candidates []f95api.SearchHit)
	OnPollError           func(err error)
}

// NewWatcher initializes a new process watcher instance.
func NewWatcher(
	cfg *config.Config,
	s *store.Store,
	api *f95api.Client,
	prov presence.PresenceProvider,
	enumerator ProcessEnumerator,
) *Watcher {
	if enumerator == nil {
		enumerator = NewProcessEnumerator()
	}
	return &Watcher{
		cfg:                   cfg,
		store:                 s,
		api:                   api,
		presence:              prov,
		enumerator:            enumerator,
		activeSessions:        make(map[string]*ActiveSession),
		stealthMode:           cfg.StealthMode,
		discoveredUnconfirmed: make(map[string]bool),
		stopChan:              make(chan struct{}),
	}
}

// SetStealthMode toggles stealth mode at runtime.
func (w *Watcher) SetStealthMode(enabled bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.stealthMode = enabled
	if enabled {
		w.currentActiveGame = nil
		w.currentActiveFolder = ""
		if w.presence != nil {
			err := w.presence.Clear()
			if err == nil {
				w.presenceActive = false
			}
			return err
		}
	}
	return nil
}

// IsStealthMode returns whether stealth mode is currently active.
func (w *Watcher) IsStealthMode() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stealthMode
}

// GetActiveSessions returns a copy of currently tracked active game sessions.
func (w *Watcher) GetActiveSessions() map[string]ActiveSession {
	w.mu.Lock()
	defer w.mu.Unlock()

	out := make(map[string]ActiveSession, len(w.activeSessions))
	for k, v := range w.activeSessions {
		out[k] = *v
	}
	return out
}

// Start initiates the background polling loop.
func (w *Watcher) Start() {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.stopChan = make(chan struct{})
	interval := time.Duration(w.cfg.PollIntervalSeconds) * time.Second
	if interval < 1*time.Second {
		interval = 5 * time.Second
	}
	w.mu.Unlock()

	// Initial poll immediately
	if err := w.PollOnce(); err != nil && w.OnPollError != nil {
		w.OnPollError(err)
	}

	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-w.stopChan:
				return
			case <-ticker.C:
				if err := w.PollOnce(); err != nil && w.OnPollError != nil {
					w.OnPollError(err)
				}
			}
		}
	}()
}

// Stop cleanly stops the background watcher and clears presence.
func (w *Watcher) Stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	w.running = false
	close(w.stopChan)
	w.currentActiveFolder = ""
	w.currentActiveGame = nil
	w.presenceActive = false
	w.mu.Unlock()

	if w.presence != nil {
		_ = w.presence.Clear()
	}
}

// PollOnce executes a single process enumeration, matching, and presence update cycle.
func (w *Watcher) PollOnce() error {
	w.mu.Lock()
	var callbacks []func()
	defer func() {
		w.mu.Unlock()
		// Callbacks may read watcher state, so never invoke them under w.mu.
		for _, callback := range callbacks {
			callback()
		}
	}()

	// 1. Load approved folders from store
	approvedList, err := w.store.ListApprovedFolderPaths()
	if err != nil {
		return fmt.Errorf("list approved folders: %w", err)
	}
	approvedMap := make(map[string]string, len(approvedList))
	for _, p := range approvedList {
		approvedMap[importers.NormalizePath(p)] = p
	}

	// 2. Query running processes and foreground window
	procs, foregroundPID, err := w.enumerator.EnumerateProcesses()
	if err != nil {
		return fmt.Errorf("enumerate processes: %w", err)
	}

	// 3. Filter candidates
	activePIDs := make(map[string]uint32, len(w.activeSessions))
	for f, s := range w.activeSessions {
		activePIDs[f] = s.PID
	}

	var candidateProcs []ProcessInfo
	procFolderMap := make(map[uint32]string)
	folderIsApproved := make(map[string]bool)
	folderIsRoot := make(map[string]bool)

	for _, p := range procs {
		if IsNoiseProcess(p.ExePath) {
			continue
		}

		matchedFolder, isApproved, isRoot := MatchProcess(p.ExePath, w.cfg.Roots, w.cfg.ExcludePaths, approvedMap)
		if matchedFolder == "" {
			continue
		}

		candidateProcs = append(candidateProcs, p)
		procFolderMap[p.PID] = matchedFolder
		if isApproved {
			folderIsApproved[matchedFolder] = true
		}
		if isRoot {
			folderIsRoot[matchedFolder] = true
		}
	}

	// 4. Deduplicate processes per game folder
	folderBestProc := DeduplicateProcesses(candidateProcs, procFolderMap, activePIDs)

	// 5. Update active sessions for approved games
	now := time.Now()
	seenFolders := make(map[string]bool)

	for matchedFolder, bestProc := range folderBestProc {
		normFolder := importers.NormalizePath(matchedFolder)

		if folderIsApproved[matchedFolder] {
			game, err := w.store.GetGameByFolder(normFolder)
			if err != nil || game == nil || !game.Approved || game.Ignored {
				continue
			}

			// Check config-level game override
			if override, ok := w.cfg.Games[normFolder]; ok && override.Enabled != nil && !*override.Enabled {
				continue
			}

			seenFolders[normFolder] = true
			if session, exists := w.activeSessions[normFolder]; exists {
				session.PID = bestProc.PID
				session.LastSeenAt = now
				if bestProc.WindowTitle != "" {
					session.WindowTitle = bestProc.WindowTitle
				}
			} else {
				w.activeSessions[normFolder] = &ActiveSession{
					Game:        game,
					PID:         bestProc.PID,
					ExePath:     bestProc.ExePath,
					WindowTitle: bestProc.WindowTitle,
					StartedAt:   now,
					LastSeenAt:  now,
				}
			}
		} else if folderIsRoot[matchedFolder] {
			// Unconfirmed candidate inside configured roots
			if !w.discoveredUnconfirmed[normFolder] {
				w.discoveredUnconfirmed[normFolder] = true
				go w.handleUnconfirmedGame(matchedFolder, bestProc)
			}
		}
	}

	// 6. Clean up ended sessions
	for normFolder, session := range w.activeSessions {
		if !seenFolders[normFolder] {
			delete(w.activeSessions, normFolder)
			if w.lockedFolder == normFolder {
				w.lockedFolder = ""
			}
			if w.OnGameStopped != nil {
				onStopped := w.OnGameStopped
				folder := session.Game.FolderPath
				callbacks = append(callbacks, func() { onStopped(folder) })
			}
		}
	}

	// 7. Presence Synchronization & Conflict Resolution
	return w.syncPresenceLocked(foregroundPID, &callbacks)
}

func (w *Watcher) syncPresenceLocked(foregroundPID uint32, callbacks *[]func()) error {
	if w.presence == nil {
		return nil
	}

	// If in stealth mode, clear presence
	if w.stealthMode {
		w.currentActiveGame = nil
		w.currentActiveFolder = ""
		if w.presenceActive {
			if err := w.presence.Clear(); err == nil {
				w.presenceActive = false
			} else {
				return err
			}
		}
		return nil
	}

	// If no approved games are running, clear presence
	if len(w.activeSessions) == 0 {
		w.lockedFolder = ""
		if w.presenceActive || w.currentActiveFolder != "" {
			gameChanged := w.currentActiveFolder != ""
			if err := w.presence.Clear(); err != nil {
				return err
			}
			w.currentActiveFolder = ""
			w.currentActiveGame = nil
			w.presenceActive = false
			if gameChanged && w.OnActiveGameChange != nil {
				onChanged := w.OnActiveGameChange
				*callbacks = append(*callbacks, func() { onChanged(nil, "") })
			}
		}
		return nil
	}

	// Select which game to broadcast
	var selectedSession *ActiveSession
	var selectedFolder string

	if len(w.activeSessions) == 1 {
		for f, s := range w.activeSessions {
			selectedFolder = f
			selectedSession = s
			break
		}
		w.lockedFolder = selectedFolder
	} else {
		// Multi-game conflict resolution
		if strings.EqualFold(w.cfg.SelectionMode, "foreground") && foregroundPID > 0 {
			for f, s := range w.activeSessions {
				if s.PID == foregroundPID {
					selectedFolder = f
					selectedSession = s
					break
				}
			}
		}

		// Fallback to lock_first if foreground didn't match or selection_mode is lock_first
		if selectedSession == nil {
			if w.lockedFolder != "" {
				if s, ok := w.activeSessions[w.lockedFolder]; ok {
					selectedFolder = w.lockedFolder
					selectedSession = s
				}
			}

			if selectedSession == nil {
				// Pick earliest started session
				var earliestTime time.Time
				for f, s := range w.activeSessions {
					if selectedSession == nil || s.StartedAt.Before(earliestTime) {
						selectedFolder = f
						selectedSession = s
						earliestTime = s.StartedAt
					}
				}
				w.lockedFolder = selectedFolder
			}
		}
	}

	if selectedSession == nil {
		return nil
	}

	// Build ActiveGame presence payload
	activeGame := w.buildActiveGame(selectedSession, selectedFolder)

	// Check if active game changed
	gameChanged := w.currentActiveFolder != selectedFolder
	w.currentActiveFolder = selectedFolder
	w.currentActiveGame = activeGame

	if err := w.presence.Sync(activeGame); err != nil {
		return err
	}
	w.presenceActive = true

	if gameChanged && w.OnActiveGameChange != nil {
		onChanged := w.OnActiveGameChange
		gameSnapshot := *activeGame
		*callbacks = append(*callbacks, func() { onChanged(&gameSnapshot, selectedFolder) })
	}

	return nil
}

func (w *Watcher) buildActiveGame(s *ActiveSession, normFolder string) *presence.ActiveGame {
	g := s.Game

	// Resolve title
	title := g.Title
	if g.CustomTitle != "" {
		title = g.CustomTitle
	}
	if override, ok := w.cfg.Games[normFolder]; ok && override.CustomTitle != "" {
		title = override.CustomTitle
	}

	// Resolve version
	version := g.InstalledVersion
	if version == "" {
		version = g.CatalogVersion
	}
	if override, ok := w.cfg.Games[normFolder]; ok && override.InstalledVersion != "" {
		version = override.InstalledVersion
	}

	// Resolve cover mode
	coverMode := g.CoverMode
	if coverMode == "" || coverMode == "inherit" {
		coverMode = w.cfg.DefaultCoverMode
	}
	if override, ok := w.cfg.Games[normFolder]; ok && override.CoverMode != "" {
		coverMode = override.CoverMode
	}
	showCover := strings.EqualFold(coverMode, "game_cover") && g.CoverURL != ""

	return &presence.ActiveGame{
		PID:              int(s.PID),
		StartedAt:        s.StartedAt,
		Title:            title,
		Creator:          g.Creator,
		Engine:           g.Engine,
		Status:           g.Status,
		Rating:           g.Rating,
		InstalledVersion: version,
		CoverURL:         g.CoverURL,
		ThreadURL:        g.ThreadURL,
		ShowCover:        showCover,
	}
}

func (w *Watcher) handleUnconfirmedGame(folder string, proc ProcessInfo) {
	norm := importers.NormalizePath(folder)

	// Check if already in store
	existing, _ := w.store.GetGameByFolder(norm)
	if existing != nil {
		return
	}

	// Clue extraction from folder
	clues := scanner.FingerprintFolder(folder)

	// Search query preference:
	// 1. TitleHint from engine metadata (System.json, Game.ini, app.info, etc.)
	// 2. Cleaned base folder name
	// 3. Cleaned window title if available
	searchQuery := clues.TitleHint
	if searchQuery == "" {
		cleaned := scanner.CleanFolderName(filepath.Base(folder))
		searchQuery = cleaned.Title
	}
	if searchQuery == "" && proc.WindowTitle != "" {
		cleaned := scanner.CleanFolderName(proc.WindowTitle)
		searchQuery = cleaned.Title
	}
	if searchQuery == "" {
		return
	}

	// Query API for candidate thread matches if API is configured
	var candidates []f95api.SearchHit
	if w.api != nil && w.cfg.F95APIKey != "" {
		resp, err := w.api.Search(searchQuery, 5)
		if err == nil && len(resp.Hits) > 0 {
			candidates = resp.Hits
			_ = w.store.SavePendingMatch(norm, proc.WindowTitle, clues.Engine, candidates)
		}
	}

	// Fire unconfirmed callback (triggers Windows toast or notification)
	if w.OnUnconfirmedDetected != nil {
		w.OnUnconfirmedDetected(folder, candidates)
	}
}

// GetCurrentActiveGame returns the currently broadcasted game (or nil if idle).
func (w *Watcher) GetCurrentActiveGame() *presence.ActiveGame {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.currentActiveGame
}

// ToggleActiveGameCoverMode flips the active game between "game_cover" and "f95_logo", saving the preference in SQLite and re-syncing presence.
func (w *Watcher) ToggleActiveGameCoverMode() (string, string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.currentActiveFolder == "" || w.currentActiveGame == nil {
		return "", "", fmt.Errorf("no game is currently active")
	}

	game, err := w.store.GetGameByFolder(w.currentActiveFolder)
	if err != nil || game == nil {
		return "", "", fmt.Errorf("could not load active game record: %w", err)
	}

	newShowCover := !w.currentActiveGame.ShowCover
	w.currentActiveGame.ShowCover = newShowCover

	newMode := "f95_logo"
	if newShowCover {
		newMode = "game_cover"
	}
	game.CoverMode = newMode
	_ = w.store.UpsertGame(game)

	if w.presence != nil && !w.stealthMode {
		_ = w.presence.Sync(w.currentActiveGame)
	}

	return newMode, game.Title, nil
}

// ReloadConfig dynamically updates the watcher configuration.
func (w *Watcher) ReloadConfig(newCfg *config.Config) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cfg = newCfg
	w.stealthMode = newCfg.StealthMode
}
