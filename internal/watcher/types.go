package watcher

import (
	"time"

	"f95rpc/internal/store"
)

// ProcessInfo holds runtime information about an inspected process.
type ProcessInfo struct {
	PID          uint32
	ParentPID    uint32
	ExePath      string
	WindowTitle  string
	IsForeground bool
}

// ActiveSession represents an actively tracked running game.
type ActiveSession struct {
	Game        *store.GameRecord
	PID         uint32
	ExePath     string
	WindowTitle string
	StartedAt   time.Time
	LastSeenAt  time.Time
}
