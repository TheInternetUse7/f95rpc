package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"f95rpc/internal/config"
	"f95rpc/internal/f95api"
	"f95rpc/internal/presence"
	"f95rpc/internal/presence/discord"
	"f95rpc/internal/store"
)

func handleSmokeTest(args []string) {
	fs := flag.NewFlagSet("smoke-test", flag.ExitOnError)
	apiKey := fs.String("api-key", "", "Optional F95Api key to test")
	clientID := fs.String("client-id", "", "Optional Discord Client ID to test")
	testPresence := fs.Bool("send-presence", false, "Send a live test presence to Discord for 10 seconds")
	_ = fs.Parse(args)

	fmt.Println("=== Phase 1 Smoke Test ===")

	// 1. SQLite Store Test
	fmt.Print("\n[1/3] Testing SQLite Registry Database... ")
	st, err := store.Open("")
	if err != nil {
		fmt.Printf("FAILED: %v\n", err)
	} else {
		testPath := `C:\TestGames\TestGame1`
		err := st.UpsertGame(&store.GameRecord{
			FolderPath:       testPath,
			ThreadID:         82285,
			Approved:         true,
			Source:           "smoke_test",
			Title:            "Eternum",
			Creator:          "Caribdis",
			Engine:           "Ren'Py",
			Status:           "Ongoing",
			Rating:           4.9,
			InstalledVersion: "0.8.5",
			CoverURL:         "https://f95api.xyz/static/cover.jpg",
			ThreadURL:        "https://f95zone.to/threads/82285",
		})
		if err != nil {
			fmt.Printf("FAILED Upsert: %v\n", err)
		} else {
			rec, err := st.GetGameByFolder(testPath)
			if err != nil || rec == nil || rec.Title != "Eternum" {
				fmt.Printf("FAILED Retrieval\n")
			} else {
				fmt.Printf("PASSED (WAL enabled, schema initialized, upsert & retrieve OK)\n")
			}
		}
		st.Close()
	}

	// 2. F95API.xyz Connectivity Test
	fmt.Print("\n[2/3] Testing f95api.xyz Connectivity... ")
	cfgPath, isDev, _ := config.ResolveConfigPath("")
	cfg, _ := config.Load(cfgPath)
	effectiveKey := cfg.F95APIKey
	if *apiKey != "" {
		effectiveKey = *apiKey
	}

	if isDev {
		fmt.Printf("(using local dev config: %s)\n", cfgPath)
	}

	client := f95api.NewClient(effectiveKey)
	if effectiveKey == "" {
		fmt.Printf("SKIPPED API search (no F95API_KEY provided; set via config.yaml or -api-key)\n")
	} else {
		res, err := client.Search("Eternum", 3)
		if err != nil {
			fmt.Printf("FAILED: %v\n", err)
		} else {
			fmt.Printf("PASSED (Found %d hits for 'Eternum')\n", len(res.Hits))
			for i, h := range res.Hits {
				fmt.Printf("      [%d] %s by %s (Thread %d)\n", i+1, h.Title, h.Creator, h.ThreadID)
			}
		}
	}

	// 3. Discord IPC Connection Test
	fmt.Print("\n[3/3] Testing Discord Desktop IPC Connection... ")
	effectiveClientID := cfg.DiscordClientID
	if *clientID != "" {
		effectiveClientID = *clientID
	}

	if effectiveClientID == "" {
		fmt.Printf("SKIPPED (no discord_client_id configured)\n")
		fmt.Println("      Set discord_client_id in config.yaml (or pass -client-id <id>).")
		fmt.Println("      See the 3-step BYO Discord App ID guide in README.md.")
	} else {
		dClient := discord.NewClient(effectiveClientID, cfg)
		err = dClient.Connect()
		if err != nil {
			fmt.Printf("NOTE: Discord IPC connection failed (%v)\n", err)
			fmt.Println("      (Ensure Discord Desktop is running to test live Rich Presence)")
		} else {
			fmt.Printf("PASSED (Successfully connected to \\\\.\\pipe\\discord-ipc-* with Client ID: %s)\n", effectiveClientID)
			if *testPresence {
				fmt.Println("      Broadcasting test Rich Presence for 10 seconds...")
				err = dClient.Sync(&presence.ActiveGame{
					PID:              os.Getpid(),
					StartedAt:        time.Now(),
					Title:            "Eternum",
					Creator:          "Caribdis",
					Engine:           "Ren'Py",
					Status:           "Ongoing",
					Rating:           4.9,
					InstalledVersion: "0.8.5",
					CoverURL:         "https://f95api.xyz/static/cover.jpg",
					ThreadURL:        "https://f95zone.to/threads/82285",
					ShowCover:        true,
				})
				if err != nil {
					fmt.Printf("      FAILED Sync: %v\n", err)
				} else {
					fmt.Println("      Presence sent! Check your Discord profile status now.")
					time.Sleep(10 * time.Second)
					dClient.Clear()
					fmt.Println("      Presence cleared.")
				}
			}
			dClient.Close()
		}
	}

	fmt.Println("\n=== Smoke Test Complete ===")
}
