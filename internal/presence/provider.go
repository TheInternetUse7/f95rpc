package presence

import (
	"time"
)

type ActiveGame struct {
	PID              int       `json:"pid"`
	StartedAt        time.Time `json:"started_at"`
	Title            string    `json:"title"`
	Creator          string    `json:"creator"`
	Engine           string    `json:"engine"`
	Status           string    `json:"status"`
	Rating           float64   `json:"rating"`
	InstalledVersion string    `json:"installed_version"`
	CoverURL         string    `json:"cover_url"`
	ThreadURL        string    `json:"thread_url"`
	ShowCover        bool      `json:"show_cover"`
}

type PresenceProvider interface {
	Sync(game *ActiveGame) error
	Clear() error
	Close() error
}
