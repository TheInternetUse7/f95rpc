package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"f95rpc/internal/config"
	"f95rpc/internal/f95api"
	"f95rpc/internal/importers"
	"f95rpc/internal/store"
)

func handleImport(args []string) {
	if len(args) == 0 {
		printImportUsage()
		return
	}

	source := args[0]
	subArgs := args[1:]

	switch source {
	case "f95checker":
		handleImportF95Checker(subArgs)
	case "playnite":
		handleImportPlaynite(subArgs)
	case "help", "--help", "-h":
		printImportUsage()
	default:
		fmt.Printf("Unknown import source: %s\n\n", source)
		printImportUsage()
		os.Exit(1)
	}
}

func printImportUsage() {
	fmt.Println("Usage: f95rpc import <source> [options]")
	fmt.Println("\nAvailable sources:")
	fmt.Println("  f95checker   Import library from F95Checker (db.sqlite3)")
	fmt.Println("  playnite     Import library from Playnite")
	fmt.Println("\nF95Checker options:")
	fmt.Println("  --db <path>       Custom path to F95Checker db.sqlite3")
	fmt.Println("  --roots-only      Only import games located inside configured roots")
	fmt.Println("  --dry-run         Preview matching games without modifying f95rpc.db")
	fmt.Println("\nPlaynite options:")
	fmt.Println("  --path <path>     Custom path to Playnite library directory")
	fmt.Println("  --category <c>    Filter by categories (comma-separated, e.g. \"F95,Visual Novels\")")
	fmt.Println("  --exclude-category <c> Exclude categories")
	fmt.Println("  --list-categories Print all categories and game counts, then exit")
	fmt.Println("  --f95-link-only   Only import games with explicit f95zone.to thread links")
	fmt.Println("  --installed-only  Only import games that have an existing folder on disk (default: true)")
	fmt.Println("  --roots-only      Only import games located inside configured roots")
	fmt.Println("  --include-hidden  Include games marked hidden in Playnite")
	fmt.Println("  --dry-run         Preview matching games without modifying f95rpc.db")
}

func handleImportF95Checker(args []string) {
	fs := flag.NewFlagSet("import f95checker", flag.ExitOnError)
	dbPath := fs.String("db", "", "Path to F95Checker db.sqlite3")
	rootsOnly := fs.Bool("roots-only", false, "Only import games located inside configured roots")
	dryRun := fs.Bool("dry-run", false, "Preview games without writing to database")
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

	effectiveDB := *dbPath
	if effectiveDB == "" && cfg.Importers.F95Checker.DBPath != "" {
		effectiveDB = cfg.Importers.F95Checker.DBPath
	}
	effectiveRootsOnly := *rootsOnly || cfg.Importers.F95Checker.RootsOnly

	fmt.Println("=== F95Checker Library Importer ===")
	if isDev {
		fmt.Printf("Notice: Running in development mode (using %s)\n", cfgPath)
	}

	imp := importers.NewF95CheckerImporter(effectiveDB, cfg.Roots, cfg.ExcludePaths, effectiveRootsOnly)
	games, err := imp.Import()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Import failed: %v\n", err)
		os.Exit(1)
	}

	saveAndEnrichGames(games, cfg, *dryRun, "F95Checker")
}

func handleImportPlaynite(args []string) {
	fs := flag.NewFlagSet("import playnite", flag.ExitOnError)
	libPath := fs.String("path", "", "Path to Playnite library directory")
	categoryStr := fs.String("category", "", "Comma-separated categories to include")
	excludeCatStr := fs.String("exclude-category", "", "Comma-separated categories to exclude")
	listCategories := fs.Bool("list-categories", false, "Print all Playnite categories and exit")
	f95LinkOnly := fs.Bool("f95-link-only", false, "Only import games with explicit F95 links")
	installedOnly := fs.Bool("installed-only", true, "Only import installed games")
	rootsOnly := fs.Bool("roots-only", false, "Only import games inside configured roots")
	includeHidden := fs.Bool("include-hidden", false, "Include games marked hidden")
	dryRun := fs.Bool("dry-run", false, "Preview games without writing to database")
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

	// Resolve config defaults
	effectiveLibPath := *libPath
	if effectiveLibPath == "" {
		effectiveLibPath = cfg.Importers.Playnite.LibraryPath
	}

	var categories []string
	if *categoryStr != "" {
		for _, c := range strings.Split(*categoryStr, ",") {
			if trimmed := strings.TrimSpace(c); trimmed != "" {
				categories = append(categories, trimmed)
			}
		}
	} else {
		categories = cfg.Importers.Playnite.Categories
	}

	var excludeCategories []string
	if *excludeCatStr != "" {
		for _, c := range strings.Split(*excludeCatStr, ",") {
			if trimmed := strings.TrimSpace(c); trimmed != "" {
				excludeCategories = append(excludeCategories, trimmed)
			}
		}
	} else {
		excludeCategories = cfg.Importers.Playnite.ExcludeCategories
	}

	effectiveF95Only := *f95LinkOnly || cfg.Importers.Playnite.F95LinkOnly
	effectiveInstalledOnly := *installedOnly
	effectiveRootsOnly := *rootsOnly || cfg.Importers.Playnite.RootsOnly
	effectiveIncludeHidden := *includeHidden || cfg.Importers.Playnite.IncludeHidden
	autoApproveLinked := cfg.Importers.Playnite.AutoApproveLinked

	imp := importers.NewPlayniteImporter(
		effectiveLibPath,
		cfg.Roots,
		cfg.ExcludePaths,
		categories,
		excludeCategories,
		effectiveF95Only,
		effectiveInstalledOnly,
		effectiveRootsOnly,
		effectiveIncludeHidden,
		autoApproveLinked,
	)

	// If --list-categories is requested
	if *listCategories {
		fmt.Println("=== Playnite Categories ===")
		counts, err := imp.ListCategories()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to list categories: %v\n", err)
			os.Exit(1)
		}
		if len(counts) == 0 {
			fmt.Println("No categories found in Playnite library.")
			return
		}
		for cat, count := range counts {
			fmt.Printf("  • %-30s (%d games)\n", cat, count)
		}
		return
	}

	fmt.Println("=== Playnite Library Importer ===")
	if isDev {
		fmt.Printf("Notice: Running in development mode (using %s)\n", cfgPath)
	}

	games, err := imp.Import()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Import failed: %v\n", err)
		os.Exit(1)
	}

	saveAndEnrichGames(games, cfg, *dryRun, "Playnite")
}

func saveAndEnrichGames(games []importers.ImportedGame, cfg *config.Config, dryRun bool, sourceName string) {
	fmt.Printf("Found %d local games in %s.\n\n", len(games), sourceName)
	if len(games) == 0 {
		return
	}

	if dryRun {
		fmt.Println("[DRY RUN] The following entries would be imported:")
		for i, g := range games {
			linkStatus := "No F95 Link"
			if g.ThreadID > 0 {
				linkStatus = fmt.Sprintf("Thread %d (Auto-Approved)", g.ThreadID)
			}
			fmt.Printf("  [%d] %s (%s)\n", i+1, g.Name, linkStatus)
			fmt.Printf("      Folder:  %s\n", g.FolderPath)
			if g.ExeHint != "" {
				fmt.Printf("      Exe:     %s\n", g.ExeHint)
			}
			if g.InstalledVersion != "" {
				fmt.Printf("      Version: %s\n", g.InstalledVersion)
			}
		}
		fmt.Printf("\n[DRY RUN] Complete. No changes were made to f95rpc.db.\n")
		return
	}

	st, err := store.Open("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open f95rpc database: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	var importedCount int
	var threadIDs []int64

	for _, g := range games {
		rec := &store.GameRecord{
			FolderPath:       g.FolderPath,
			ThreadID:         g.ThreadID,
			Approved:         g.Approved,
			Source:           g.Source,
			Title:            g.Name,
			Creator:          g.Developer,
			InstalledVersion: g.InstalledVersion,
			ExeHint:          g.ExeHint,
			ThreadURL:        fmt.Sprintf("https://f95zone.to/threads/%d", g.ThreadID),
			LastFetchedAt:    time.Time{},
		}
		if g.ThreadID <= 0 {
			rec.ThreadURL = ""
		} else {
			threadIDs = append(threadIDs, g.ThreadID)
		}

		if err := st.UpsertGame(rec); err != nil {
			fmt.Printf("  [WARN] Failed to save %s: %v\n", g.Name, err)
			continue
		}
		importedCount++
	}

	fmt.Printf("Saved %d games to local registry (f95rpc.db).\n", importedCount)

	// Enrich metadata via f95api
	if cfg.F95APIKey != "" && len(threadIDs) > 0 {
		fmt.Printf("Fetching online catalog metadata for %d games from f95api.xyz...\n", len(threadIDs))
		client := f95api.NewClient(cfg.F95APIKey)

		chunkSize := 50
		for i := 0; i < len(threadIDs); i += chunkSize {
			end := i + chunkSize
			if end > len(threadIDs) {
				end = len(threadIDs)
			}
			chunk := threadIDs[i:end]
			batchRes, err := client.GetBatch(chunk)
			if err != nil {
				fmt.Printf("  [WARN] Batch metadata fetch failed for chunk: %v\n", err)
				continue
			}

			for _, summary := range batchRes.Results {
				for _, g := range games {
					if g.ThreadID == summary.ThreadID {
						rec, err := st.GetGameByFolder(g.FolderPath)
						if err == nil && rec != nil {
							if summary.Title != "" {
								rec.Title = summary.Title
							}
							if summary.Creator != "" {
								rec.Creator = summary.Creator
							}
							if summary.Cover != nil && *summary.Cover != "" {
								rec.CoverURL = *summary.Cover
							}
							if summary.Banner != nil && *summary.Banner != "" {
								rec.BannerURL = *summary.Banner
							}
							if summary.Version != "" {
								rec.CatalogVersion = summary.Version
							}
							rec.LastFetchedAt = time.Now()
							_ = st.UpsertGame(rec)
						}
					}
				}
			}
		}
		fmt.Println("Metadata enrichment complete!")
	} else if len(threadIDs) > 0 {
		fmt.Println("Tip: Set f95api_key in config.yaml to automatically fetch cover art and catalog metadata.")
	}

	fmt.Printf("\n%s import finished successfully.\n", sourceName)
}
