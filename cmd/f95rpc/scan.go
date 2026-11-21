package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"f95rpc/internal/config"
	"f95rpc/internal/enrichment"
	"f95rpc/internal/f95api"
	"f95rpc/internal/importers"
	"f95rpc/internal/scanner"
	"f95rpc/internal/store"
)

func handleScan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	allFlag := fs.Bool("all", false, "Scan all folders, including already-approved games")
	dryRun := fs.Bool("dry-run", false, "Preview detected folders and candidates without saving")
	_ = fs.Parse(args)

	cfgPath, isDev, err := config.ResolveConfigPath("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error locating config: %v\n", err)
		os.Exit(1)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	if len(cfg.Roots) == 0 {
		fmt.Println("No game roots configured yet. Add one first:")
		fmt.Println("  f95rpc roots add <path>")
		return
	}

	fmt.Println("=== Folder Scanner ===")
	if isDev {
		fmt.Printf("Notice: Running in development mode (using %s)\n", cfgPath)
	}
	fmt.Printf("Scanning %d configured root(s)...\n", len(cfg.Roots))

	games, err := scanner.ScanRoots(cfg.Roots, cfg.ExcludePaths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Scan failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d game folder(s).\n\n", len(games))
	if len(games) == 0 {
		return
	}

	st, err := store.Open("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	var client *f95api.Client
	var enricher *enrichment.Enricher
	if cfg.F95APIKey != "" {
		client = f95api.NewClient(cfg.F95APIKey)
		enricher = enrichment.New(st, client)
	}

	reader := bufio.NewReader(os.Stdin)

	for i, g := range games {
		existing, _ := st.GetGameByFolder(g.FolderPath)
		if existing != nil {
			if existing.Ignored {
				continue
			}
			if existing.Approved && !*allFlag {
				continue
			}
		}

		fmt.Printf("------------------------------------------------------------\n")
		fmt.Printf("[%d/%d] Game Folder: %s\n", i+1, len(games), g.FolderPath)
		fmt.Printf("      Detected Title:  %s\n", g.CleanTitle)
		fmt.Printf("      Detected Engine: %s\n", g.Engine)
		if g.Version != "" {
			fmt.Printf("      Detected Ver:    %s\n", g.Version)
		}
		if g.ExeHint != "" {
			fmt.Printf("      Primary Exe:     %s\n", g.ExeHint)
		}

		if *dryRun {
			continue
		}

		var hits []f95api.SearchHit
		if client != nil {
			fmt.Printf("      Searching f95api.xyz for '%s'...\n", g.CleanTitle)
			res, err := client.Search(g.CleanTitle, 5)
			if err == nil && len(res.Hits) > 0 {
				hits = res.Hits
			}
		}

		if len(hits) > 0 {
			fmt.Println("\n      F95zone Candidates:")
			for idx, h := range hits {
				scoreStr := ""
				if h.Score != nil {
					scoreStr = fmt.Sprintf(" (Rating: %.1f)", *h.Score)
				}
				fmt.Printf("        [%d] %s by %s [Thread %d]%s\n", idx+1, h.Title, h.Creator, h.ThreadID, scoreStr)
			}
		} else {
			fmt.Println("      No online search candidates found (or API key not configured).")
		}

		fmt.Println("\n      Actions: [1-N] Select candidate | [s] Skip | [i] Ignore folder | [t] Enter Thread ID")
		fmt.Print("      Choice: ")
		choice, _ := reader.ReadString('\n')
		choice = strings.TrimSpace(choice)

		switch strings.ToLower(choice) {
		case "s", "":
			fmt.Println("      Skipped.")
			continue

		case "i":
			rec := &store.GameRecord{
				FolderPath: g.FolderPath,
				Title:      g.CleanTitle,
				Engine:     g.Engine,
				Ignored:    true,
				Source:     "scanner",
			}
			_ = st.UpsertGame(rec)
			fmt.Println("      Folder marked as ignored.")

		case "t":
			fmt.Print("      Enter numerical F95 Thread ID: ")
			tInput, _ := reader.ReadString('\n')
			tInput = strings.TrimSpace(tInput)
			threadID, err := strconv.ParseInt(tInput, 10, 64)
			if err != nil || threadID <= 0 {
				fmt.Println("      Invalid thread ID. Skipping.")
				continue
			}

			rec := &store.GameRecord{
				FolderPath:       g.FolderPath,
				ThreadID:         threadID,
				Approved:         true,
				Source:           "manual_scan",
				Title:            g.CleanTitle,
				InstalledVersion: g.Version,
				DetectedEngine:   g.Engine,
				ExeHint:          g.ExeHint,
				ThreadURL:        fmt.Sprintf("https://f95zone.to/threads/%d", threadID),
			}
			if _, err := saveAndEnrichApprovedGame(st, rec, enricher); err != nil {
				fmt.Printf("      [WARN] Failed to approve game: %v\n", err)
				continue
			}
			fmt.Printf("      Successfully bound to thread %d and approved!\n", threadID)

		default:
			selectedIdx, err := strconv.Atoi(choice)
			if err == nil && selectedIdx >= 1 && selectedIdx <= len(hits) {
				hit := hits[selectedIdx-1]
				rec := &store.GameRecord{
					FolderPath:       g.FolderPath,
					ThreadID:         hit.ThreadID,
					Approved:         true,
					Source:           "scanner_search",
					Title:            hit.Title,
					Creator:          hit.Creator,
					InstalledVersion: g.Version,
					DetectedEngine:   g.Engine,
					ExeHint:          g.ExeHint,
					ThreadURL:        fmt.Sprintf("https://f95zone.to/threads/%d", hit.ThreadID),
				}
				if hit.Cover != nil {
					rec.CoverURL = *hit.Cover
				}
				if hit.Banner != nil {
					rec.BannerURL = *hit.Banner
				}
				if hit.Score != nil {
					rec.Rating = *hit.Score
				}
				saved, err := saveAndEnrichApprovedGame(st, rec, enricher)
				if err != nil {
					fmt.Printf("      [WARN] Failed to approve game: %v\n", err)
					continue
				}
				fmt.Printf("      Successfully approved '%s' (Thread %d)!\n", saved.Title, hit.ThreadID)
			} else {
				fmt.Println("      Invalid selection. Skipped.")
			}
		}
	}

	fmt.Println("\nScan finished.")
}

func handleBind(args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: f95rpc bind <folder_path> <thread_id_or_url>")
		return
	}

	folderPath := args[0]
	tStr := args[1]

	var threadID int64
	if id, err := strconv.ParseInt(tStr, 10, 64); err == nil && id > 0 {
		threadID = id
	} else if id := importers.ExtractThreadID(tStr); id > 0 {
		threadID = id
	} else {
		fmt.Fprintf(os.Stderr, "Error: Invalid thread ID or URL '%s'.\n", tStr)
		os.Exit(1)
	}

	absFolder, err := filepath.Abs(folderPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving folder path: %v\n", err)
		os.Exit(1)
	}

	st, err := store.Open("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	cleaned := scanner.CleanFolderName(filepath.Base(absFolder))
	fp := scanner.FingerprintFolder(absFolder)

	rec := &store.GameRecord{
		FolderPath:       absFolder,
		ThreadID:         threadID,
		Approved:         true,
		Source:           "manual_bind",
		Title:            cleaned.Title,
		InstalledVersion: cleaned.Version,
		DetectedEngine:   fp.Engine,
		ExeHint:          fp.ExeHint,
		ThreadURL:        fmt.Sprintf("https://f95zone.to/threads/%d", threadID),
	}

	var enricher *enrichment.Enricher
	cfgPath, _, _ := config.ResolveConfigPath("")
	cfg, _ := config.Load(cfgPath)
	if cfg != nil && cfg.F95APIKey != "" {
		fmt.Printf("Fetching metadata for F95 Thread %d from f95api.xyz...\n", threadID)
		client := f95api.NewClient(cfg.F95APIKey)
		enricher = enrichment.New(st, client)
	}

	rec, err = saveAndEnrichApprovedGame(st, rec, enricher)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to bind: %v\n", err)
		os.Exit(1)
	}

	verStr := rec.InstalledVersion
	if verStr == "" {
		verStr = rec.CatalogVersion
	}
	if verStr != "" {
		verStr = ", Version: " + verStr
	}
	creatorStr := ""
	if rec.Creator != "" {
		creatorStr = " by " + rec.Creator
	}

	fmt.Printf("Successfully bound '%s' to Thread %d (%s%s%s) [Approved]\n",
		absFolder, threadID, rec.Title, creatorStr, verStr)
}

func handleIgnore(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: f95rpc ignore <folder_path>")
		return
	}

	absFolder, err := filepath.Abs(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving folder path: %v\n", err)
		os.Exit(1)
	}

	st, err := store.Open("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	rec, _ := st.GetGameByFolder(absFolder)
	if rec == nil {
		rec = &store.GameRecord{
			FolderPath: absFolder,
			Title:      filepath.Base(absFolder),
			Source:     "manual_ignore",
		}
	}
	rec.Ignored = true

	if err := st.UpsertGame(rec); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to ignore: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Folder '%s' marked as ignored.\n", absFolder)
}

func handleList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	allFlag := fs.Bool("all", false, "Include ignored and unapproved games")
	_ = fs.Parse(args)

	st, err := store.Open("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	// Query all games
	paths, err := st.ListApprovedFolderPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Query failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== f95rpc Registered Games ===")
	if len(paths) == 0 {
		fmt.Println("No games registered in f95rpc.db yet.")
		fmt.Println("Run 'f95rpc scan' or 'f95rpc import <source>' to add games.")
		return
	}

	for i, p := range paths {
		rec, err := st.GetGameByFolder(p)
		if err != nil || rec == nil {
			continue
		}
		if !*allFlag && rec.Ignored {
			continue
		}
		status := "[Approved]"
		if !rec.Approved {
			status = "[Pending Approval]"
		}
		if rec.Ignored {
			status = "[Ignored]"
		}

		fmt.Printf("[%d] %s %s\n", i+1, rec.Title, status)
		fmt.Printf("    Folder:  %s\n", rec.FolderPath)
		fmt.Printf("    Thread:  %d (%s)\n", rec.ThreadID, rec.Source)
		if rec.InstalledVersion != "" {
			fmt.Printf("    Version: %s\n", rec.InstalledVersion)
		}
		if rec.Creator != "" {
			fmt.Printf("    Creator: %s\n", rec.Creator)
		}
	}
}
