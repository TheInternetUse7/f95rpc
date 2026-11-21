package importers

import (
	"path/filepath"
	"strings"
)

type ImportedGame struct {
	FolderPath       string `json:"folder_path"`
	ExeHint          string `json:"exe_hint"`
	ThreadID         int64  `json:"thread_id"`
	Name             string `json:"name"`
	InstalledVersion string `json:"installed_version"`
	Developer        string `json:"developer"`
	Source           string `json:"source"`
	HasF95Link       bool   `json:"has_f95_link"`
	Approved         bool   `json:"approved"`
}

func NormalizePath(p string) string {
	cleaned := filepath.Clean(p)
	return strings.ToLower(cleaned)
}

func IsInsideRoot(folder, root string) bool {
	normF := NormalizePath(folder)
	normR := NormalizePath(root)
	if normF == normR {
		return true
	}
	sep := string(filepath.Separator)
	prefix := normR
	if !strings.HasSuffix(prefix, sep) {
		prefix += sep
	}
	return strings.HasPrefix(normF, prefix)
}

func IsInsideRoots(folder string, roots []string, excludePaths []string) bool {
	normF := NormalizePath(folder)

	matched := false
	var owningRoot string
	for _, r := range roots {
		if IsInsideRoot(normF, r) {
			if len(r) > len(owningRoot) {
				owningRoot = r
			}
			matched = true
		}
	}
	if !matched {
		return false
	}

	// Check exclusion patterns
	for _, excl := range excludePaths {
		normExcl := strings.ToLower(strings.TrimSpace(excl))
		if normExcl == "" {
			continue
		}
		// Check if any path segment matches the exclusion
		parts := strings.Split(normF, string(filepath.Separator))
		for _, part := range parts {
			if part == normExcl {
				return false
			}
		}
	}

	return true
}

func FindLongestPrefixRoot(folder string, roots []string) string {
	normF := NormalizePath(folder)
	var best string
	for _, r := range roots {
		if IsInsideRoot(normF, r) {
			if len(r) > len(best) {
				best = r
			}
		}
	}
	return best
}
