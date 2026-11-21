package main

import (
	"context"
	"fmt"

	"f95rpc/internal/enrichment"
	"f95rpc/internal/store"
)

func enrichSavedGames(enricher *enrichment.Enricher, folders []string) {
	if enricher == nil || len(folders) == 0 {
		return
	}
	updated, err := enricher.EnrichFolders(context.Background(), folders)
	if err != nil {
		fmt.Printf("  [WARN] Catalog enrichment failed: %v\n", err)
		fmt.Println("         Local games remain saved; the running tray/daemon will retry approved games.")
	}
	if updated > 0 {
		fmt.Printf("Enriched full catalog metadata for %d game(s).\n", updated)
	}
}

// Save approval before making network requests, so API failures never lose the local binding.
func saveAndEnrichApprovedGame(st *store.Store, game *store.GameRecord, enricher *enrichment.Enricher) (*store.GameRecord, error) {
	if err := st.UpsertGame(game); err != nil {
		return nil, err
	}
	enrichSavedGames(enricher, []string{game.FolderPath})
	return st.GetGameByFolder(game.FolderPath)
}
