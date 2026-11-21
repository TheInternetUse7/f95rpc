package discord

import (
	"io"
	"net"
	"testing"
	"time"

	"f95rpc/internal/config"
	"f95rpc/internal/presence"
)

func TestCloseWithOpenConnection(t *testing.T) {
	clientConn, peer := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = peer.Close()
	})
	client := NewClient("test-client", config.DefaultConfig())
	client.conn = clientConn
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, peer)
		close(done)
	}()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Errorf("Close panicked instead of completing shutdown: %v", recovered)
		}
	}()

	if err := client.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if client.conn != nil {
		t.Fatal("Close left its IPC connection open")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not disconnect the peer")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("repeated Close failed: %v", err)
	}
}

func TestTruncateString(t *testing.T) {
	if got := truncateString("a", 10); got != "a " {
		t.Errorf("expected min 2 chars padded 'a ', got %q", got)
	}
	if got := truncateString("hello world", 5); got != "hello" {
		t.Errorf("expected truncated 'hello', got %q", got)
	}
	if got := truncateString("", 5); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestMapEngineAsset(t *testing.T) {
	tests := []struct {
		engine   string
		expected string
	}{
		{"Ren'Py", "renpy"},
		{"renpy", "renpy"},
		{"Unity", "unity"},
		{"RPGM", "rpgm"},
		{"RPG Maker MV", "rpgm"},
		{"Unreal Engine", "unreal"},
		{"HTML", "html"},
		{"CustomUnknown", "other"},
	}

	for _, tt := range tests {
		got := mapEngineAsset(tt.engine)
		if got != tt.expected {
			t.Errorf("mapEngineAsset(%q) = %q; want %q", tt.engine, got, tt.expected)
		}
	}
}

func TestBuildActivity(t *testing.T) {
	cfg := config.DefaultConfig()
	client := NewClient("1122867846121271326", cfg)

	game := &presence.ActiveGame{
		PID:              1234,
		StartedAt:        time.Unix(1700000000, 0),
		Title:            "Eternum",
		Creator:          "Caribdis",
		Engine:           "Ren'Py",
		Status:           "Ongoing",
		Rating:           4.9,
		InstalledVersion: "0.8.5",
		CoverURL:         "https://example.com/cover.jpg",
		ThreadURL:        "https://f95zone.to/threads/82285",
		ShowCover:        true,
	}

	act := client.buildActivity(game)

	if act["name"] != "Eternum" {
		t.Errorf("expected name 'Eternum', got %v", act["name"])
	}
	if act["details"] != "v0.8.5 • Caribdis" {
		t.Errorf("expected details 'v0.8.5 • Caribdis', got %v", act["details"])
	}
	if act["state"] != "Engine: Ren'Py" {
		t.Errorf("expected state 'Engine: Ren'Py', got %v", act["state"])
	}

	buttons, ok := act["buttons"].([]map[string]string)
	if !ok || len(buttons) != 2 {
		t.Fatalf("expected 2 buttons, got %v", act["buttons"])
	}
	if buttons[0]["label"] != "View on F95zone" || buttons[0]["url"] != "https://f95zone.to/threads/82285" {
		t.Errorf("unexpected button 1: %v", buttons[0])
	}
	if buttons[1]["label"] != "GitHub" || buttons[1]["url"] != "https://github.com/TheInternetUse7/f95rpc" {
		t.Errorf("unexpected button 2: %v", buttons[1])
	}

	assets, ok := act["assets"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected assets map in activity")
	}
	if assets["large_image"] != "https://example.com/cover.jpg" {
		t.Errorf("expected cover URL as large_image when ShowCover=true, got %v", assets["large_image"])
	}

	// Test fallback to default logo when ShowCover is false
	game.ShowCover = false
	actDefault := client.buildActivity(game)
	assetsDef := actDefault["assets"].(map[string]interface{})
	if assetsDef["large_image"] != cfg.DefaultLogoURL {
		t.Errorf("expected DefaultLogoURL when ShowCover=false, got %v", assetsDef["large_image"])
	}
}
