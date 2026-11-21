package watcher

import (
	"path/filepath"
	"strings"

	"f95rpc/internal/importers"
)

// MatchProcess evaluates whether a running executable belongs to an approved game or configured root.
// Returns:
//   - matchedFolder: the canonical folder path of the game
//   - isApproved: true if matched an already-approved game in the store
//   - isRoot: true if matched a configured root directory (unconfirmed candidate)
func MatchProcess(exePath string, roots []string, excludePaths []string, approvedFolders map[string]string) (string, bool, bool) {
	normExe := importers.NormalizePath(exePath)

	// 1. Check approved folders first (dual-scope priority)
	// Find longest matching approved folder in case of nested structures
	var bestApprovedNorm string
	var bestApprovedCanonical string
	for normAppr, canonicalAppr := range approvedFolders {
		if importers.IsInsideRoot(normExe, normAppr) {
			if len(normAppr) > len(bestApprovedNorm) {
				bestApprovedNorm = normAppr
				bestApprovedCanonical = canonicalAppr
			}
		}
	}
	if bestApprovedCanonical != "" {
		return bestApprovedCanonical, true, false
	}

	// 2. Check configured roots
	if importers.IsInsideRoots(normExe, roots, excludePaths) {
		owningRoot := importers.FindLongestPrefixRoot(normExe, roots)
		if owningRoot != "" {
			rel, err := filepath.Rel(owningRoot, normExe)
			if err == nil {
				parts := strings.Split(rel, string(filepath.Separator))
				if len(parts) > 1 {
					// Game folder is the direct subdirectory under the root
					gameFolder := filepath.Join(owningRoot, parts[0])
					return gameFolder, false, true
				}
				return owningRoot, false, true
			}
		}
	}

	return "", false, false
}
