package scanner

import (
	"regexp"
	"strings"
)

var (
	// Matches bracketed expressions like [v1.2], [Completed], [RUS], (Final), (2022)
	bracketsRegex = regexp.MustCompile(`\[.*?\]|\(.*?\)|<.*?>`)

	// Matches common release / scene / mod / platform tags
	tagsRegex = regexp.MustCompile(`(?i)(?:[-_]?(?:multi[-_]?mod|mod|cheatmod|galleryunlocker|compressed|final|completed|repack|portable|pc|win(?:64|32)?|linux|mac|extra|extras|public|patreon|demo|steam|by[-_][\w]+))+`)

	// Matches version strings: v1.0, 0.8.5, Ch.4 Up.3, Episode 1-10, Ep.18
	versionRegex = regexp.MustCompile(`(?i)\b(?:v|ver\.?|ch\.?|ch(?:apter)?\.?|ep\.?|episode)?\s*(\d+(?:\.\d+)*(?:[a-z]|[-_]?(?:beta|alpha|hotfix|fix|\d+))?)\b`)
)

type CleanedName struct {
	Title   string
	Version string
}

func CleanFolderName(name string) CleanedName {
	raw := name

	// 1. Try extracting version string before stripping
	var version string
	if vMatches := versionRegex.FindAllString(raw, -1); len(vMatches) > 0 {
		// Pick the most version-like match (usually the last or longest)
		for i := len(vMatches) - 1; i >= 0; i-- {
			candidate := strings.TrimSpace(vMatches[i])
			if len(candidate) > 1 && !strings.EqualFold(candidate, "64") && !strings.EqualFold(candidate, "32") {
				version = candidate
				break
			}
		}
	}

	// 2. Remove tags and brackets
	s := tagsRegex.ReplaceAllString(raw, " ")
	s = bracketsRegex.ReplaceAllString(s, " ")

	// 3. Remove version from title if it was appended
	if version != "" {
		s = strings.ReplaceAll(s, version, " ")
	}

	// 4. Clean up underscores, dashes, multiple spaces
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)

	if s == "" {
		s = name // Fallback to raw folder name if cleaning erased everything
	}

	return CleanedName{
		Title:   s,
		Version: version,
	}
}
