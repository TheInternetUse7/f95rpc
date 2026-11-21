package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"f95rpc/internal/config"
	"f95rpc/internal/f95api"
	"f95rpc/internal/presence"
	"f95rpc/internal/presence/discord"
	"f95rpc/internal/store"
	"f95rpc/internal/version"
	"f95rpc/internal/watcher"
)

func handleRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgFlag := fs.String("config", "", "Path to custom config.yaml")
	dbFlag := fs.String("db", "", "Path to custom f95rpc.db")
	pollFlag := fs.Int("poll", 0, "Override poll interval in seconds")
	stealthFlag := fs.Bool("stealth", false, "Start in stealth mode (pause Discord RPC)")
	fs.Parse(args)

	cfgPath, isDev, err := config.ResolveConfigPath(*cfgFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error locating configuration: %v\n", err)
		os.Exit(1)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	if *pollFlag > 0 {
		cfg.PollIntervalSeconds = *pollFlag
	}
	if *stealthFlag {
		cfg.StealthMode = true
	}

	// Check if background tray companion is already running
	mutexName, _ := windows.UTF16PtrFromString("Global\\f95rpc_tray_mutex")
	hMutex, err := windows.OpenMutex(windows.SYNCHRONIZE, false, mutexName)
	if err == nil && hMutex != 0 {
		_ = windows.CloseHandle(hMutex)
		fmt.Println("Warning: f95rpc-tray is currently running in the background.")
		fmt.Println("         Run 'f95rpc stop' to terminate it before running in the foreground,")
		fmt.Println("         otherwise both instances may conflict on Discord presence updates.")
		fmt.Println()
	}

	// Validate Discord Client ID
	if cfg.DiscordClientID == "" {
		fmt.Fprintln(os.Stderr, "Error: discord_client_id is not set in config.yaml.")
		fmt.Fprintln(os.Stderr, "Please open the Discord Developer Portal, create an Application,")
		fmt.Fprintln(os.Stderr, "and paste your Application ID into config.yaml (or set DISCORD_CLIENT_ID).")
		os.Exit(1)
	}

	// Open Store
	dbPath := *dbFlag
	if dbPath == "" && isDev {
		dbPath = "f95rpc.db"
	}
	s, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening SQLite database: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	// Print Startup Banner
	fmt.Printf("=== f95rpc Daemon (%s) ===\n", version.Version)
	devNote := ""
	if isDev {
		devNote = " (development mode)"
	}
	fmt.Printf("Configuration:  %s%s\n", cfgPath, devNote)
	fmt.Printf("Discord Client: %s\n", cfg.DiscordClientID)
	fmt.Printf("Selection Mode: %s\n", cfg.SelectionMode)
	fmt.Printf("Poll Interval:  %ds\n", cfg.PollIntervalSeconds)
	if cfg.StealthMode {
		fmt.Println("Stealth Mode:   ON (Presence broadcasting paused)")
	}
	if len(cfg.Roots) > 0 {
		fmt.Println("Configured Roots:")
		for _, r := range cfg.Roots {
			fmt.Printf("  • %s\n", r)
		}
	} else {
		fmt.Println("Configured Roots: (none configured — tracking approved scattered games only)")
	}

	apprCount, _ := s.ListApprovedFolderPaths()
	fmt.Printf("Approved Games in DB: %d\n", len(apprCount))
	fmt.Println()

	// Initialize Discord Presence Client
	prov := discord.NewClient(cfg.DiscordClientID, cfg)
	if err := prov.Connect(); err != nil {
		fmt.Printf("[DISCORD IPC] Discord Desktop is not currently running. Will connect automatically when opened.\n")
	} else {
		fmt.Printf("[DISCORD IPC] Connected to Discord Desktop IPC.\n")
	}

	// Initialize API Client
	var api *f95api.Client
	if cfg.F95APIKey != "" {
		api = f95api.NewClient(cfg.F95APIKey)
	}

	// Initialize Watcher
	w := watcher.NewWatcher(cfg, s, api, prov, nil)

	w.OnActiveGameChange = func(game *presence.ActiveGame, folder string) {
		if game == nil {
			fmt.Printf("[%s] [RPC IDLE] Presence cleared.\n", time.Now().Format("15:04:05"))
			return
		}

		ver := game.InstalledVersion
		if ver != "" {
			ver = " v" + ver
		}
		coverNote := "F95 Logo"
		if game.ShowCover {
			coverNote = "Game Cover"
		}
		fmt.Printf("[%s] [RPC ACTIVE] Playing: %s%s (PID: %d, Image: %s)\n",
			time.Now().Format("15:04:05"), game.Title, ver, game.PID, coverNote)
	}

	w.OnGameStopped = func(folder string) {
		fmt.Printf("[%s] [GAME EXIT] %s\n", time.Now().Format("15:04:05"), filepath.Base(folder))
	}

	w.OnUnconfirmedDetected = func(folder string, candidates []f95api.SearchHit) {
		fmt.Printf("[%s] [NEW GAME DETECTED] %s\n", time.Now().Format("15:04:05"), folder)
		if len(candidates) > 0 {
			top := candidates[0]
			fmt.Printf("    Top Match: %s by %s (Thread %d)\n", top.Title, top.Creator, top.ThreadID)
			fmt.Println("    Run 'f95rpc scan' to review and approve.")
		} else {
			fmt.Println("    Run 'f95rpc scan' to search and approve.")
		}
	}

	w.OnPollError = func(err error) {
		fmt.Fprintf(os.Stderr, "[POLL ERROR] %v\n", err)
	}
	w.OnEnrichmentError = func(err error) {
		fmt.Fprintf(os.Stderr, "[METADATA WARN] %v (will retry)\n", err)
	}

	// Start Watcher Loop
	w.Start()
	fmt.Printf("[%s] Watcher loop running. Press Ctrl+C to stop.\n\n", time.Now().Format("15:04:05"))

	// Listen for shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan
	fmt.Printf("\n[%s] Shutdown signal received. Cleaning up presence...\n", time.Now().Format("15:04:05"))
	w.Stop()
	_ = prov.Close()
	fmt.Printf("[%s] f95rpc daemon stopped cleanly.\n", time.Now().Format("15:04:05"))
}
