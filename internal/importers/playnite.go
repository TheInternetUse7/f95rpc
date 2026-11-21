package importers

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var f95ThreadRegex = regexp.MustCompile(`(?i)f95zone\.to/threads/(?:[^/\s]+\.)?(\d+)`)

type PlayniteImporter struct {
	libraryPath       string
	roots             []string
	excludePaths      []string
	categories        []string
	excludeCategories []string
	f95LinkOnly       bool
	installedOnly     bool
	rootsOnly         bool
	includeHidden     bool
	autoApproveLinked bool
}

type PlayniteGameJSON struct {
	ID               string               `json:"Id"`
	Name             string               `json:"Name"`
	InstallDirectory string               `json:"InstallDirectory"`
	Version          string               `json:"Version"`
	Hidden           bool                 `json:"Hidden"`
	Categories       []PlayniteNamedItem  `json:"Categories"`
	CategoryIds      []string             `json:"CategoryIds"`
	Tags             []PlayniteNamedItem  `json:"Tags"`
	TagIds           []string             `json:"TagIds"`
	Links            []PlayniteLink       `json:"Links"`
	GameActions      []PlayniteGameAction `json:"GameActions"`
}

type PlayniteNamedItem struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

type PlayniteLink struct {
	Name string `json:"Name"`
	URL  string `json:"Url"`
}

type PlayniteGameAction struct {
	Name       string `json:"Name"`
	Path       string `json:"Path"`
	WorkingDir string `json:"WorkingDir"`
	IsHandled  bool   `json:"IsHandledByPlugin"`
}

func NewPlayniteImporter(
	libraryPath string,
	roots []string,
	excludePaths []string,
	categories []string,
	excludeCategories []string,
	f95LinkOnly bool,
	installedOnly bool,
	rootsOnly bool,
	includeHidden bool,
	autoApproveLinked bool,
) *PlayniteImporter {
	if libraryPath == "" {
		libraryPath = AutoDiscoverPlayniteLibrary()
	}
	return &PlayniteImporter{
		libraryPath:       libraryPath,
		roots:             roots,
		excludePaths:      excludePaths,
		categories:        categories,
		excludeCategories: excludeCategories,
		f95LinkOnly:       f95LinkOnly,
		installedOnly:     installedOnly,
		rootsOnly:         rootsOnly,
		includeHidden:     includeHidden,
		autoApproveLinked: autoApproveLinked,
	}
}

// AutoDiscoverPlayniteLibrary resolves the active Playnite library path.
func AutoDiscoverPlayniteLibrary() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, _ := os.UserHomeDir()
		appData = filepath.Join(home, "AppData", "Roaming")
	}

	// 1. Check %APPDATA%\Playnite\config.json
	configFile := filepath.Join(appData, "Playnite", "config.json")
	if data, err := os.ReadFile(configFile); err == nil {
		var cfg struct {
			DatabasePath string `json:"DatabasePath"`
		}
		if err := json.Unmarshal(data, &cfg); err == nil && cfg.DatabasePath != "" {
			expanded := os.ExpandEnv(cfg.DatabasePath)
			// Handle Windows %AppData% if ExpandEnv missed it
			if strings.HasPrefix(strings.ToLower(expanded), "%appdata%") {
				expanded = filepath.Join(appData, expanded[9:])
			}
			if fi, err := os.Stat(expanded); err == nil && fi.IsDir() {
				return expanded
			}
		}
	}

	// 2. Default fallback
	defaultPath := filepath.Join(appData, "Playnite", "library")
	return defaultPath
}

// ExtractThreadID parses an F95zone thread URL to find the numerical thread ID.
func ExtractThreadID(url string) int64 {
	matches := f95ThreadRegex.FindStringSubmatch(url)
	if len(matches) >= 2 {
		if id, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
			return id
		}
	}
	return 0
}

// IsPlayniteLocked checks if Playnite's database files are currently locked by a running process.
func IsPlayniteLocked(libraryPath string) bool {
	dbPath := filepath.Join(libraryPath, "games.db")
	if _, err := os.Stat(dbPath); err != nil {
		return false
	}
	// Attempt to open the file to test for sharing violations
	f, err := os.OpenFile(dbPath, os.O_RDONLY, 0)
	if err != nil {
		return true // File is exclusively locked by Playnite.DesktopApp.exe
	}
	f.Close()
	return false
}

func (imp *PlayniteImporter) Import() ([]ImportedGame, error) {
	if _, err := os.Stat(imp.libraryPath); err != nil {
		return nil, fmt.Errorf("Playnite library directory not found at '%s': %w", imp.libraryPath, err)
	}

	if IsPlayniteLocked(imp.libraryPath) {
		return nil, fmt.Errorf("Playnite is currently running and holding a lock on its database.\nPlease close Playnite Desktop to proceed with the import")
	}

	rawGames, categoryMap, err := imp.loadRawGamesAndCategories()
	if err != nil {
		return nil, err
	}

	var imported []ImportedGame

	for _, g := range rawGames {
		// Hygiene filter: Hidden games
		if g.Hidden && !imp.includeHidden {
			continue
		}

		// Hygiene filter: Installed only
		folder := g.InstallDirectory
		if imp.installedOnly {
			if folder == "" {
				continue
			}
			if fi, err := os.Stat(folder); err != nil || !fi.IsDir() {
				continue
			}
		}
		if folder == "" {
			continue
		}

		// Filter: Roots only
		if imp.rootsOnly {
			if !IsInsideRoots(folder, imp.roots, imp.excludePaths) {
				continue
			}
		}

		// Resolve category names
		gameCats := imp.resolveCategories(g, categoryMap)

		// Filter: Categories inclusion
		if len(imp.categories) > 0 {
			matchedCat := false
			for _, required := range imp.categories {
				for _, gc := range gameCats {
					if strings.EqualFold(gc, strings.TrimSpace(required)) {
						matchedCat = true
						break
					}
				}
				if matchedCat {
					break
				}
			}
			if !matchedCat {
				continue
			}
		}

		// Filter: Categories exclusion
		if len(imp.excludeCategories) > 0 {
			excludedCat := false
			for _, excluded := range imp.excludeCategories {
				for _, gc := range gameCats {
					if strings.EqualFold(gc, strings.TrimSpace(excluded)) {
						excludedCat = true
						break
					}
				}
				if excludedCat {
					break
				}
			}
			if excludedCat {
				continue
			}
		}

		// Find F95 Thread ID from Links
		var threadID int64
		for _, l := range g.Links {
			if id := ExtractThreadID(l.URL); id > 0 {
				threadID = id
				break
			}
		}

		// Filter: F95 Link Only
		if imp.f95LinkOnly && threadID == 0 {
			continue
		}

		// Resolve Primary Executable Hint from GameActions
		exeHint := ""
		for _, act := range g.GameActions {
			actPath := act.Path
			if strings.Contains(actPath, "{InstallDir}") {
				actPath = strings.ReplaceAll(actPath, "{InstallDir}", folder)
			}
			actPath = filepath.Clean(actPath)
			if filepath.IsAbs(actPath) && strings.HasSuffix(strings.ToLower(actPath), ".exe") {
				exeHint = actPath
				break
			}
		}

		approved := false
		if threadID > 0 && imp.autoApproveLinked {
			approved = true
		}

		imported = append(imported, ImportedGame{
			FolderPath:       folder,
			ExeHint:          exeHint,
			ThreadID:         threadID,
			Name:             g.Name,
			InstalledVersion: g.Version,
			Source:           "playnite",
			HasF95Link:       threadID > 0,
			Approved:         approved,
		})
	}

	return imported, nil
}

func (imp *PlayniteImporter) resolveCategories(g PlayniteGameJSON, catMap map[string]string) []string {
	var cats []string
	for _, c := range g.Categories {
		if c.Name != "" {
			cats = append(cats, c.Name)
		}
	}
	for _, id := range g.CategoryIds {
		if name, ok := catMap[id]; ok {
			cats = append(cats, name)
		}
	}
	return cats
}

func (imp *PlayniteImporter) loadRawGamesAndCategories() ([]PlayniteGameJSON, map[string]string, error) {
	catMap := make(map[string]string)
	var games []PlayniteGameJSON

	// 1. Check for JSON format in games/ and categories/
	gamesDir := filepath.Join(imp.libraryPath, "games")
	if fi, err := os.Stat(gamesDir); err == nil && fi.IsDir() {
		// Read categories
		catsDir := filepath.Join(imp.libraryPath, "categories")
		if cEntries, err := os.ReadDir(catsDir); err == nil {
			for _, entry := range cEntries {
				if strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
					data, err := os.ReadFile(filepath.Join(catsDir, entry.Name()))
					if err == nil {
						var item PlayniteNamedItem
						if json.Unmarshal(data, &item) == nil && item.ID != "" && item.Name != "" {
							catMap[item.ID] = item.Name
						}
					}
				}
			}
		}

		// Read games
		gEntries, err := os.ReadDir(gamesDir)
		if err == nil {
			for _, entry := range gEntries {
				if strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
					data, err := os.ReadFile(filepath.Join(gamesDir, entry.Name()))
					if err == nil {
						var g PlayniteGameJSON
						if json.Unmarshal(data, &g) == nil && g.Name != "" {
							games = append(games, g)
						}
					}
				}
			}
		}
		if len(games) > 0 {
			return games, catMap, nil
		}
	}

	// 2. Check for LiteDB v4 binary files (games.db & categories.db)
	gamesDB := filepath.Join(imp.libraryPath, "games.db")
	if fi, err := os.Stat(gamesDB); err == nil && !fi.IsDir() {
		catDB := filepath.Join(imp.libraryPath, "categories.db")
		if catData, err := os.ReadFile(catDB); err == nil {
			catMap = parseLiteDBCategories(catData)
		}

		var tagMap map[string]string
		tagDB := filepath.Join(imp.libraryPath, "tags.db")
		if tagData, err := os.ReadFile(tagDB); err == nil {
			tagMap = parseLiteDBCategories(tagData)
		}

		gamesData, err := os.ReadFile(gamesDB)
		if err != nil {
			return nil, nil, fmt.Errorf("read games.db: %w", err)
		}
		games = parseLiteDBGames(gamesData, tagMap)
		return games, catMap, nil
	}

	return nil, nil, fmt.Errorf("no games found in Playnite library at '%s'", imp.libraryPath)
}

const (
	liteDBPageSize       = 4096
	liteDBPageHeaderSize = 25
	pageTypeData         = 4
	pageTypeExtend       = 5
)

type liteDBPage struct {
	id        uint32
	pageType  byte
	prevID    uint32
	nextID    uint32
	itemCount uint16
	data      []byte
}

func readLiteDBDocuments(data []byte) [][]byte {
	numPages := len(data) / liteDBPageSize
	pages := make(map[uint32]*liteDBPage, numPages)
	var dataPages []*liteDBPage

	for p := 0; p < numPages; p++ {
		pData := data[p*liteDBPageSize : (p+1)*liteDBPageSize]
		pID := binary.LittleEndian.Uint32(pData[0:4])
		pType := pData[4]
		prevID := binary.LittleEndian.Uint32(pData[5:9])
		nextID := binary.LittleEndian.Uint32(pData[9:13])
		itemCount := binary.LittleEndian.Uint16(pData[13:15])
		page := &liteDBPage{
			id:        pID,
			pageType:  pType,
			prevID:    prevID,
			nextID:    nextID,
			itemCount: itemCount,
			data:      pData,
		}
		pages[pID] = page
		if pType == pageTypeData {
			dataPages = append(dataPages, page)
		}
	}

	readExtendData := func(firstPageID uint32) []byte {
		var buf []byte
		currID := firstPageID
		for currID != 0xFFFFFFFF {
			p, ok := pages[currID]
			if !ok || p.pageType != pageTypeExtend {
				break
			}
			cnt := int(p.itemCount)
			if liteDBPageHeaderSize+cnt > len(p.data) {
				cnt = len(p.data) - liteDBPageHeaderSize
			}
			if cnt > 0 {
				buf = append(buf, p.data[liteDBPageHeaderSize:liteDBPageHeaderSize+cnt]...)
			}
			currID = p.nextID
		}
		return buf
	}

	var docs [][]byte
	for _, dp := range dataPages {
		pos := liteDBPageHeaderSize
		for i := 0; i < int(dp.itemCount); i++ {
			if pos+8 > len(dp.data) {
				break
			}
			_ = binary.LittleEndian.Uint16(dp.data[pos : pos+2]) // index
			extendPageID := binary.LittleEndian.Uint32(dp.data[pos+2 : pos+6])
			dataLen := int(binary.LittleEndian.Uint16(dp.data[pos+6 : pos+8]))
			pos += 8
			if extendPageID != 0xFFFFFFFF {
				docBytes := readExtendData(extendPageID)
				if len(docBytes) > 0 {
					docs = append(docs, docBytes)
				}
			} else {
				if pos+dataLen <= len(dp.data) {
					docs = append(docs, dp.data[pos:pos+dataLen])
					pos += dataLen
				}
			}
		}
	}
	return docs
}

func parseLiteDBCategories(data []byte) map[string]string {
	catMap := make(map[string]string)
	docs := readLiteDBDocuments(data)
	for _, docBytes := range docs {
		doc, _ := parseBSONDocument(docBytes, 0)
		if doc != nil {
			id, _ := doc["_id"].(string)
			name, _ := doc["Name"].(string)
			if id != "" && name != "" {
				catMap[id] = name
			}
		}
	}
	return catMap
}

func parseLiteDBGames(data []byte, tagMap map[string]string) []PlayniteGameJSON {
	var games []PlayniteGameJSON
	docs := readLiteDBDocuments(data)
	seen := make(map[string]bool)

	for _, docBytes := range docs {
		doc, _ := parseBSONDocument(docBytes, 0)
		if doc == nil {
			continue
		}
		name, _ := doc["Name"].(string)
		id, _ := doc["_id"].(string)
		if name != "" && !seen[id+name] {
			seen[id+name] = true
			var g PlayniteGameJSON
			g.Name = name
			g.ID = id
			if dir, ok := doc["InstallDirectory"].(string); ok {
				g.InstallDirectory = dir
			}
			if ver, ok := doc["Version"].(string); ok {
				g.Version = ver
			}
			if hid, ok := doc["Hidden"].(bool); ok {
				g.Hidden = hid
			}

			// CategoryIds
			if cids, ok := doc["CategoryIds"].([]interface{}); ok {
				for _, cid := range cids {
					if s, ok := cid.(string); ok {
						g.CategoryIds = append(g.CategoryIds, s)
					}
				}
			}

			// TagIds & Tags
			if tids, ok := doc["TagIds"].([]interface{}); ok {
				for _, tid := range tids {
					if s, ok := tid.(string); ok {
						g.TagIds = append(g.TagIds, s)
						if tagMap != nil {
							if tagName, found := tagMap[s]; found {
								g.Tags = append(g.Tags, PlayniteNamedItem{ID: s, Name: tagName})
							}
						}
					}
				}
			}

			// Links
			if links, ok := doc["Links"].([]interface{}); ok {
				for _, l := range links {
					if lm, ok := l.(map[string]interface{}); ok {
						var pl PlayniteLink
						if n, ok := lm["Name"].(string); ok {
							pl.Name = n
						}
						if u, ok := lm["Url"].(string); ok {
							pl.URL = u
						}
						g.Links = append(g.Links, pl)
					}
				}
			}

			// GameActions
			if actions, ok := doc["GameActions"].([]interface{}); ok {
				for _, a := range actions {
					if am, ok := a.(map[string]interface{}); ok {
						var pa PlayniteGameAction
						if p, ok := am["Path"].(string); ok {
							pa.Path = p
						}
						if w, ok := am["WorkingDir"].(string); ok {
							pa.WorkingDir = w
						}
						g.GameActions = append(g.GameActions, pa)
					}
				}
			}

			games = append(games, g)
		}
	}
	return games
}

func parseBSONDocument(buf []byte, offset int) (map[string]interface{}, int) {
	if offset+4 > len(buf) {
		return nil, offset
	}
	docLen := int(binary.LittleEndian.Uint32(buf[offset : offset+4]))
	if docLen < 5 || offset+docLen > len(buf) || buf[offset+docLen-1] != 0 {
		return nil, offset
	}

	doc := make(map[string]interface{})
	pos := offset + 4
	end := offset + docLen - 1

	for pos < end {
		elemType := buf[pos]
		pos++

		keyEnd := -1
		for i := pos; i < end; i++ {
			if buf[i] == 0 {
				keyEnd = i
				break
			}
		}
		if keyEnd == -1 {
			break
		}
		key := string(buf[pos:keyEnd])
		pos = keyEnd + 1

		switch elemType {
		case 0x01: // double (8 bytes)
			pos += 8
		case 0x02: // string (4-byte length + bytes)
			if pos+4 > end {
				return nil, offset
			}
			sLen := int(binary.LittleEndian.Uint32(buf[pos : pos+4]))
			pos += 4
			if pos+sLen > end+1 || sLen < 1 {
				return nil, offset
			}
			doc[key] = string(buf[pos : pos+sLen-1])
			pos += sLen
		case 0x03: // embedded document
			sub, next := parseBSONDocument(buf, pos)
			if sub == nil {
				return nil, offset
			}
			doc[key] = sub
			pos = next
		case 0x04: // array
			arr, next := parseBSONArray(buf, pos)
			if arr == nil {
				return nil, offset
			}
			doc[key] = arr
			pos = next
		case 0x05: // binary
			if pos+4 > end {
				return nil, offset
			}
			bLen := int(binary.LittleEndian.Uint32(buf[pos : pos+4]))
			pos += 4
			if pos >= end {
				return nil, offset
			}
			subType := buf[pos]
			pos++
			if pos+bLen > end+1 {
				return nil, offset
			}
			binData := buf[pos : pos+bLen]
			pos += bLen
			if (subType == 0x03 || subType == 0x04) && bLen == 16 {
				d1 := binary.LittleEndian.Uint32(binData[0:4])
				d2 := binary.LittleEndian.Uint16(binData[4:6])
				d3 := binary.LittleEndian.Uint16(binData[6:8])
				guidStr := fmt.Sprintf("%08x-%04x-%04x-%02x%02x-%02x%02x%02x%02x%02x%02x",
					d1, d2, d3,
					binData[8], binData[9], binData[10], binData[11],
					binData[12], binData[13], binData[14], binData[15])
				doc[key] = guidStr
			} else {
				doc[key] = binData
			}
		case 0x07: // ObjectId (12 bytes)
			if pos+12 > end {
				return nil, offset
			}
			doc[key] = fmt.Sprintf("%x", buf[pos:pos+12])
			pos += 12
		case 0x08: // boolean (1 byte)
			if pos >= end {
				return nil, offset
			}
			doc[key] = buf[pos] != 0
			pos++
		case 0x09: // int64 datetime
			pos += 8
		case 0x0A: // null
			doc[key] = nil
		case 0x10: // int32 (4 bytes)
			pos += 4
		case 0x11: // uint64 (8 bytes)
			pos += 8
		case 0x12: // int64 (8 bytes)
			pos += 8
		case 0x13: // decimal128 (16 bytes)
			pos += 16
		default:
			return nil, offset
		}
	}

	return doc, offset + docLen
}

func parseBSONArray(buf []byte, offset int) ([]interface{}, int) {
	doc, next := parseBSONDocument(buf, offset)
	if doc == nil {
		return nil, offset
	}
	var arr []interface{}
	for i := 0; ; i++ {
		idxKey := strconv.Itoa(i)
		if val, ok := doc[idxKey]; ok {
			arr = append(arr, val)
		} else {
			break
		}
	}
	return arr, next
}

// ListCategories returns a map of category names and the count of games in each category.
func (imp *PlayniteImporter) ListCategories() (map[string]int, error) {
	rawGames, catMap, err := imp.loadRawGamesAndCategories()
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int)
	for _, name := range catMap {
		counts[name] = 0
	}
	for _, g := range rawGames {
		cats := imp.resolveCategories(g, catMap)
		for _, c := range cats {
			counts[c]++
		}
	}
	return counts, nil
}
