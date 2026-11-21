package watcher

import (
	"strings"
)

// DeduplicateProcesses groups processes by folder and picks the most representative primary process for each folder.
// Priority:
// 1. If existing active PID is still running for this folder, keep it.
// 2. Process with a visible WindowTitle.
// 3. Parent process if parent/child relationship exists between the candidates.
// 4. Lowest PID (earliest launched).
func DeduplicateProcesses(procs []ProcessInfo, procFolderMap map[uint32]string, activePIDs map[string]uint32) map[string]ProcessInfo {
	folderProcs := make(map[string][]ProcessInfo)
	for _, p := range procs {
		folder, ok := procFolderMap[p.PID]
		if !ok || folder == "" {
			continue
		}
		folderProcs[folder] = append(folderProcs[folder], p)
	}

	result := make(map[string]ProcessInfo)
	for folder, list := range folderProcs {
		if len(list) == 1 {
			result[folder] = list[0]
			continue
		}

		// 1. Check if the previously active PID for this folder is still among the list
		if prevPID, exists := activePIDs[strings.ToLower(folder)]; exists {
			var found *ProcessInfo
			for i := range list {
				if list[i].PID == prevPID {
					found = &list[i]
					break
				}
			}
			if found != nil {
				// Update window title if it gained one
				result[folder] = *found
				continue
			}
		}

		// 2. Find parent-child relationships
		pidSet := make(map[uint32]bool)
		for _, p := range list {
			pidSet[p.PID] = true
		}

		best := list[0]
		for i := 1; i < len(list); i++ {
			cand := list[i]

			// If current best is a child of candidate, prefer the candidate (parent)
			if best.ParentPID == cand.PID {
				best = cand
				continue
			}
			// If candidate is child of best, keep best (parent)
			if cand.ParentPID == best.PID {
				continue
			}

			// Prefer process with non-empty window title
			if cand.WindowTitle != "" && best.WindowTitle == "" {
				best = cand
				continue
			}
			if best.WindowTitle != "" && cand.WindowTitle == "" {
				continue
			}

			// Prefer foreground window if either is foreground
			if cand.IsForeground && !best.IsForeground {
				best = cand
				continue
			}
			if best.IsForeground && !cand.IsForeground {
				continue
			}

			// Fallback: lowest PID
			if cand.PID < best.PID {
				best = cand
			}
		}

		result[folder] = best
	}

	return result
}
