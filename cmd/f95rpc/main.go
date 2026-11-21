package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"f95rpc/internal/config"
	"f95rpc/internal/store"
	"f95rpc/internal/traycontrol"
)

const Version = "0.1.0-dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "version", "--version", "-v":
		fmt.Printf("f95rpc v%s\n", Version)

	case "start":
		handleStart(args)

	case "stop":
		handleStop(args)

	case "status":
		handleStatus(args)

	case "run":
		handleRun(args)

	case "config":
		handleConfig(args)

	case "roots":
		handleRoots(args)

	case "import":
		handleImport(args)

	case "scan":
		handleScan(args)

	case "bind":
		handleBind(args)

	case "ignore":
		handleIgnore(args)

	case "list":
		handleList(args)

	case "smoke-test":
		handleSmokeTest(args)

	case "help", "--help", "-h":
		printUsage()

	default:
		fmt.Printf("Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf("f95rpc v%s — Discord Rich Presence for F95zone games\n\n", Version)
	fmt.Println("Usage: f95rpc <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  start                    Launch f95rpc in the Windows system tray")
	fmt.Println("  stop                     Stop background system tray companion")
	fmt.Println("  status                   Show daemon status and registered game counts")
	fmt.Println("  run [--stealth]          Run daemon in foreground console (for debugging)")
	fmt.Println("  roots <list|add|remove>  Manage configured game root folders")
	fmt.Println("  scan [--dry-run]         Scan game roots and interactively approve matches")
	fmt.Println("  import <source>          Import library from F95Checker or Playnite")
	fmt.Println("  bind <folder> <thread_id> Manually bind a folder to an F95 thread ID")
	fmt.Println("  ignore <folder>          Mark a folder as ignored")
	fmt.Println("  list                     List all approved games in f95rpc.db")
	fmt.Println("  smoke-test               Verify f95api, Discord IPC, and SQLite connectivity")
	fmt.Println("  config                   Inspect or open configuration file")
	fmt.Println("  version                  Print version information")
	fmt.Println("  help                     Show this help message")
}

func handleConfig(args []string) {
	cfgPath, isDev, err := config.ResolveConfigPath("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error locating config path: %v\n", err)
		os.Exit(1)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	devNotice := ""
	if isDev {
		devNotice = " (development mode: repository root)"
	}
	fmt.Printf("Config file: %s%s\n\n", cfgPath, devNotice)
	fmt.Printf("  F95 API Key:        %s\n", maskKey(cfg.F95APIKey))
	fmt.Printf("  Discord Client ID:  %s\n", cfg.DiscordClientID)
	fmt.Printf("  Roots:              %v\n", cfg.Roots)
	fmt.Printf("  Exclude Paths:      %v\n", cfg.ExcludePaths)
	fmt.Printf("  Selection Mode:     %s\n", cfg.SelectionMode)
	fmt.Printf("  Poll Interval:      %ds\n", cfg.PollIntervalSeconds)
	fmt.Printf("  Stealth Mode:       %t\n", cfg.StealthMode)
	fmt.Printf("  Default Cover Mode: %s\n", cfg.DefaultCoverMode)
	fmt.Printf("  Default Logo URL:   %s\n", cfg.DefaultLogoURL)
	fmt.Printf("  Show Thread Button: %t\n", cfg.ShowThreadButton)
	fmt.Printf("  Show Repo Button:   %t\n", cfg.ShowRepoButton)
	fmt.Printf("  Repo Button Label:  %s\n", cfg.RepoButtonLabel)
	fmt.Printf("  Repo Button URL:    %s\n", cfg.RepoButtonURL)
	fmt.Printf("  Display Type:       %s\n", cfg.DisplayType)
	fmt.Printf("  Template (Name):    %s\n", cfg.Templates.Name)
	fmt.Printf("  Template (Details): %s\n", cfg.Templates.Details)
	fmt.Printf("  Template (State):   %s\n", cfg.Templates.State)
}

func maskKey(k string) string {
	if k == "" {
		return "(not set)"
	}
	if len(k) <= 8 {
		return "********"
	}
	return k[:4] + "..." + k[len(k)-4:]
}

func handleRoots(args []string) {
	if len(args) == 0 || args[0] == "list" {
		listRoots()
		return
	}

	sub := args[0]
	switch sub {
	case "add":
		if len(args) < 2 {
			fmt.Println("Usage: f95rpc roots add <path>")
			os.Exit(1)
		}
		addRoot(args[1])
	case "remove":
		if len(args) < 2 {
			fmt.Println("Usage: f95rpc roots remove <path>")
			os.Exit(1)
		}
		removeRoot(args[1])
	default:
		fmt.Printf("Unknown roots subcommand: %s\n", sub)
		fmt.Println("Usage: f95rpc roots [list|add|remove]")
		os.Exit(1)
	}
}

func listRoots() {
	cfgPath, isDev, err := config.ResolveConfigPath("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	devNotice := ""
	if isDev {
		devNotice = " [Dev Mode: Repo Root]"
	}
	fmt.Printf("Configured Game Roots%s:\n", devNotice)
	if len(cfg.Roots) == 0 {
		fmt.Println("  (No root folders configured yet. Add one with: f95rpc roots add <path>)")
	} else {
		for i, r := range cfg.Roots {
			exists := "EXISTS"
			if info, err := os.Stat(r); err != nil || !info.IsDir() {
				exists = "NOT FOUND"
			}
			fmt.Printf("  [%d] %s (%s)\n", i+1, r, exists)
		}
	}

	fmt.Printf("\nExcluded patterns: %s\n", strings.Join(cfg.ExcludePaths, ", "))
}

func addRoot(pathStr string) {
	abs, err := filepath.Abs(pathStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving path: %v\n", err)
		os.Exit(1)
	}

	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: path '%s' does not exist or is not a directory.\n", abs)
		os.Exit(1)
	}

	cfgPath, _, err := config.ResolveConfigPath("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	cleaned := filepath.Clean(abs)
	for _, r := range cfg.Roots {
		if strings.EqualFold(r, cleaned) {
			fmt.Printf("Root '%s' is already in config.\n", cleaned)
			return
		}
	}

	cfg.Roots = append(cfg.Roots, cleaned)
	if err := config.Save(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully added root: %s\n", cleaned)
}

func removeRoot(pathStr string) {
	cfgPath, _, err := config.ResolveConfigPath("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	cleaned := filepath.Clean(pathStr)
	foundIndex := -1
	for i, r := range cfg.Roots {
		if strings.EqualFold(r, cleaned) {
			foundIndex = i
			break
		}
	}

	if foundIndex == -1 {
		fmt.Printf("Root '%s' not found in config.\n", cleaned)
		return
	}

	cfg.Roots = append(cfg.Roots[:foundIndex], cfg.Roots[foundIndex+1:]...)
	if err := config.Save(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully removed root: %s\n", cleaned)
}

func handleStart(args []string) {
	selfPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error locating executable: %v\n", err)
		os.Exit(1)
	}

	trayExe := filepath.Join(filepath.Dir(selfPath), "f95rpc-tray.exe")
	if _, err := os.Stat(trayExe); err != nil {
		fmt.Fprintf(os.Stderr, "Error: f95rpc-tray.exe not found at '%s'.\nRun 'go build -ldflags=\"-H windowsgui\" -o f95rpc-tray.exe ./cmd/f95rpc-tray' first.\n", trayExe)
		os.Exit(1)
	}

	cmd := exec.Command(trayExe)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start f95rpc-tray: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("f95rpc started in system tray.")
}

func handleStop(args []string) {
	err := traycontrol.RequestStop(10 * time.Second)
	if errors.Is(err, traycontrol.ErrNotRunning) {
		fmt.Println("f95rpc tray companion is not currently running.")
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to stop f95rpc cleanly: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("f95rpc tray companion stopped.")
}

func handleStatus(args []string) {
	cmd := exec.Command("tasklist", "/FI", "IMAGENAME eq f95rpc-tray.exe", "/NH")
	out, _ := cmd.Output()
	if strings.Contains(string(out), "f95rpc-tray.exe") {
		fmt.Println("Status: Running in System Tray (f95rpc-tray.exe)")
	} else {
		fmt.Println("Status: Stopped")
	}

	cfgPath, isDev, _ := config.ResolveConfigPath("")
	cfg, _ := config.Load(cfgPath)
	if cfg != nil {
		devNote := ""
		if isDev {
			devNote = " (dev mode)"
		}
		fmt.Printf("Config:         %s%s\n", cfgPath, devNote)
		fmt.Printf("Selection Mode: %s\n", cfg.SelectionMode)
		fmt.Printf("Default Cover:  %s\n", cfg.DefaultCoverMode)
	}

	st, _ := store.Open("")
	if st != nil {
		defer st.Close()
		appr, _ := st.ListApprovedFolderPaths()
		pending, _ := st.GetPendingMatchCount()
		fmt.Printf("Approved Games: %d\n", len(appr))
		if pending > 0 {
			fmt.Printf("Pending Approvals: %d (run 'f95rpc scan')\n", pending)
		}
	}
}
