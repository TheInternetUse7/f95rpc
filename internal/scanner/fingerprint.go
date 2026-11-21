package scanner

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type DetectedInfo struct {
	Engine      string
	TitleHint   string
	VersionHint string
	ExeHint     string
	SteamAppID  string
}

var ignoredBinaries = map[string]bool{
	"unitycrashhandler64.exe": true,
	"unitycrashhandler32.exe": true,
	"crashreportclient.exe":   true,
	"unins000.exe":            true,
	"uninstall.exe":           true,
	"dxsetup.exe":             true,
	"vc_redist.x64.exe":       true,
	"vc_redist.x86.exe":       true,
}

func FingerprintFolder(folderPath string) DetectedInfo {
	info := DetectedInfo{
		Engine: "Other",
	}

	entries, err := os.ReadDir(folderPath)
	if err != nil {
		return info
	}

	var exeCandidates []string

	for _, entry := range entries {
		name := entry.Name()
		lower := strings.ToLower(name)

		if !entry.IsDir() {
			if strings.HasSuffix(lower, ".exe") && !ignoredBinaries[lower] {
				exeCandidates = append(exeCandidates, filepath.Join(folderPath, name))
			}
			if lower == "steam_appid.txt" {
				if data, err := os.ReadFile(filepath.Join(folderPath, name)); err == nil {
					info.SteamAppID = strings.TrimSpace(string(data))
				}
			}
			if lower == "game.ini" {
				// RPG Maker XP/VX/Ace
				info.Engine = "RPGM"
				if title := parseGameIni(filepath.Join(folderPath, name)); title != "" {
					info.TitleHint = title
				}
			}
			continue
		}

		// Directory checks
		switch {
		case strings.HasSuffix(lower, "_data"):
			// Unity engine
			info.Engine = "Unity"
			appInfoPath := filepath.Join(folderPath, name, "app.info")
			if prodName := parseUnityAppInfo(appInfoPath); prodName != "" {
				info.TitleHint = prodName
			}

		case lower == "renpy":
			info.Engine = "Ren'Py"

		case lower == "game":
			// Ren'Py game folder check
			if fi, err := os.Stat(filepath.Join(folderPath, name)); err == nil && fi.IsDir() {
				if gEntries, err := os.ReadDir(filepath.Join(folderPath, name)); err == nil {
					for _, ge := range gEntries {
						gLower := strings.ToLower(ge.Name())
						if strings.HasSuffix(gLower, ".rpa") || strings.HasSuffix(gLower, ".rpy") || strings.HasSuffix(gLower, ".rpyc") {
							info.Engine = "Ren'Py"
							break
						}
					}
				}
			}

		case lower == "www" || lower == "data":
			// RPG Maker MV / MZ
			sysJSONPath := filepath.Join(folderPath, name, "data", "System.json")
			if lower == "data" {
				sysJSONPath = filepath.Join(folderPath, "data", "System.json")
			}
			if data, err := os.ReadFile(sysJSONPath); err == nil {
				var sys struct {
					GameTitle string `json:"gameTitle"`
				}
				if json.Unmarshal(data, &sys) == nil && sys.GameTitle != "" {
					info.Engine = "RPGM"
					info.TitleHint = sys.GameTitle
				}
			}

		case lower == "engine" || lower == "binaries":
			info.Engine = "Unreal"
		}
	}

	// Pick primary executable
	if len(exeCandidates) > 0 {
		info.ExeHint = exeCandidates[0]
	}

	return info
}

func parseUnityAppInfo(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, strings.TrimSpace(scanner.Text()))
	}
	if len(lines) >= 2 && lines[1] != "" {
		return lines[1] // Line 2 is ProductName
	}
	if len(lines) >= 1 && lines[0] != "" {
		return lines[0]
	}
	return ""
}

func parseGameIni(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(strings.ToLower(line), "title=") {
			return strings.TrimSpace(line[6:])
		}
	}
	return ""
}
