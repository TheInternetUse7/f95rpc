package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/getlantern/systray"
	"golang.org/x/sys/windows"

	"f95rpc/internal/assets"
	"f95rpc/internal/autostart"
	"f95rpc/internal/config"
	"f95rpc/internal/f95api"
	"f95rpc/internal/presence"
	"f95rpc/internal/presence/discord"
	"f95rpc/internal/store"
	"f95rpc/internal/traycontrol"
	"f95rpc/internal/version"
	"f95rpc/internal/watcher"
)

const AppMutexName = traycontrol.MutexName

func main() {
	runtime.LockOSThread()

	// 1. Single instance check using named Win32 mutex
	mutexName, _ := windows.UTF16PtrFromString(AppMutexName)
	hMutex, err := windows.CreateMutex(nil, true, mutexName)
	if err != nil || hMutex == 0 {
		if hMutex != 0 {
			_ = windows.CloseHandle(hMutex)
		}
		return
	}
	defer windows.CloseHandle(hMutex)
	defer windows.ReleaseMutex(hMutex)

	stopListener, err := traycontrol.ListenForStop()
	if err != nil {
		return
	}
	defer stopListener.Close()

	// 2. Start systray loop
	systray.Run(func() { onReady(stopListener.Requested) }, onExit)
}

func onReady(stopRequested <-chan struct{}) {
	appTitle := fmt.Sprintf("f95rpc %s", version.Version)
	systray.SetIcon(assets.IconICO)
	systray.SetTitle(appTitle)
	systray.SetTooltip(appTitle + " — Discord Rich Presence for F95zone")

	// Load configuration
	cfgPath, isDev, err := config.ResolveConfigPath("")
	if err != nil {
		systray.Quit()
		return
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		systray.Quit()
		return
	}

	// Open SQLite store
	dbPath := ""
	if isDev {
		dbPath = "f95rpc.db"
	}
	s, err := store.Open(dbPath)
	if err != nil {
		systray.Quit()
		return
	}

	// Setup Discord provider
	var prov presence.PresenceProvider
	if cfg.DiscordClientID != "" {
		p := discord.NewClient(cfg.DiscordClientID, cfg)
		_ = p.Connect()
		prov = p
	}

	// Setup API client
	var api *f95api.Client
	if cfg.F95APIKey != "" {
		api = f95api.NewClient(cfg.F95APIKey)
	}

	// Setup Watcher
	w := watcher.NewWatcher(cfg, s, api, prov, nil)

	// Menu UI items
	mVersion := systray.AddMenuItem(appTitle, "Build version")
	mVersion.Disable()
	systray.AddSeparator()

	mStatus := systray.AddMenuItem("Status: Idle", "Current Discord RPC status")
	mStatus.Disable()

	systray.AddSeparator()

	mCover := systray.AddMenuItem("Toggle Cover Art (Active Game)", "Switch between default F95 logo and game cover")
	mCover.Disable()

	mStealth := systray.AddMenuItemCheckbox("Stealth Mode (Pause RPC)", "Temporarily hide rich presence from Discord", cfg.StealthMode)

	mPending := systray.AddMenuItem("Pending Matches...", "Review newly detected games")
	mPending.Hide()

	systray.AddSeparator()

	mReload := systray.AddMenuItem("Reload config.yaml", "Reload configuration from disk")
	mOpenConfig := systray.AddMenuItem("Open Config Folder", "Open configuration folder in Windows Explorer")

	isAuto, _ := autostart.IsEnabled("f95rpc")
	mAutostart := systray.AddMenuItemCheckbox("Start with Windows", "Launch f95rpc automatically on Windows startup", isAuto)

	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Exit f95rpc")

	// Update pending matches counter
	updatePending := func() {
		cnt, err := s.GetPendingMatchCount()
		if err == nil && cnt > 0 {
			mPending.SetTitle(fmt.Sprintf("Pending Matches (%d)...", cnt))
			mPending.Show()
		} else {
			mPending.Hide()
		}
	}
	updatePending()

	// Hook events
	w.OnActiveGameChange = func(game *presence.ActiveGame, folder string) {
		if game == nil {
			if w.IsStealthMode() {
				mStatus.SetTitle("Status: Paused (Stealth Mode)")
			} else {
				mStatus.SetTitle("Status: Idle")
			}
			mCover.SetTitle("Toggle Cover Art (No active game)")
			mCover.Disable()
			systray.SetTooltip(appTitle + " — Idle")
		} else {
			mStatus.SetTitle(fmt.Sprintf("Playing: %s", game.Title))
			mCover.Enable()
			if game.ShowCover {
				mCover.SetTitle("Use Default F95 Logo")
			} else {
				mCover.SetTitle("Show Game Cover Art")
			}
			systray.SetTooltip(fmt.Sprintf("%s — Playing: %s", appTitle, game.Title))
		}
	}

	w.OnUnconfirmedDetected = func(folder string, candidates []f95api.SearchHit) {
		updatePending()
	}

	// Start background watcher loop
	w.Start()

	shutdown := func() {
		w.Stop()
		if prov != nil {
			_ = prov.Close()
		}
		_ = s.Close()
		systray.Quit()
	}

	// Handle menu interactions
	go func() {
		for {
			select {
			case <-mCover.ClickedCh:
				newMode, _, err := w.ToggleActiveGameCoverMode()
				if err == nil {
					if newMode == "game_cover" {
						mCover.SetTitle("Use Default F95 Logo")
					} else {
						mCover.SetTitle("Show Game Cover Art")
					}
				}

			case <-mStealth.ClickedCh:
				newStealth := !w.IsStealthMode()
				_ = w.SetStealthMode(newStealth)
				if newStealth {
					mStealth.Check()
					mStatus.SetTitle("Status: Paused (Stealth Mode)")
					systray.SetTooltip(appTitle + " — Paused (Stealth Mode)")
				} else {
					mStealth.Uncheck()
					if ag := w.GetCurrentActiveGame(); ag != nil {
						mStatus.SetTitle(fmt.Sprintf("Playing: %s", ag.Title))
						systray.SetTooltip(fmt.Sprintf("%s — Playing: %s", appTitle, ag.Title))
					} else {
						mStatus.SetTitle("Status: Idle")
						systray.SetTooltip(appTitle + " — Idle")
					}
				}

			case <-mPending.ClickedCh:
				exePath, _ := os.Executable()
				cliExe := filepath.Join(filepath.Dir(exePath), "f95rpc.exe")
				if _, err := os.Stat(cliExe); err == nil {
					_ = exec.Command("cmd.exe", "/c", "start", "cmd.exe", "/k", cliExe, "scan").Start()
				}

			case <-mReload.ClickedCh:
				newCfg, err := config.Load(cfgPath)
				if err == nil {
					w.ReloadConfig(newCfg)
				}

			case <-mOpenConfig.ClickedCh:
				_ = exec.Command("explorer.exe", filepath.Dir(cfgPath)).Start()

			case <-mAutostart.ClickedCh:
				if isAuto {
					if err := autostart.Disable("f95rpc"); err == nil {
						mAutostart.Uncheck()
						isAuto = false
					}
				} else {
					exePath, _ := os.Executable()
					if err := autostart.Enable("f95rpc", exePath); err == nil {
						mAutostart.Check()
						isAuto = true
					}
				}

			case <-mQuit.ClickedCh:
				shutdown()
				return

			case <-stopRequested:
				shutdown()
				return
			}
		}
	}()
}

func onExit() {
	// Cleanup on application termination
}
