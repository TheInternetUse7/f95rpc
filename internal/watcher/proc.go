package watcher

// ProcessEnumerator defines the interface for querying system processes and windows.
type ProcessEnumerator interface {
	EnumerateProcesses() ([]ProcessInfo, uint32, error)
}
