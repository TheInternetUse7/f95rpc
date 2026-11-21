package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.DiscordClientID != "" {
		t.Fatal("expected empty default discord client id (BYO Application ID)")
	}
	if cfg.SelectionMode != "lock_first" {
		t.Fatalf("expected lock_first selection mode, got %s", cfg.SelectionMode)
	}
	if cfg.DefaultLogoURL == "" {
		t.Fatal("expected default logo url")
	}
	if cfg.PollIntervalSeconds != 5 {
		t.Fatalf("expected 5s poll interval, got %d", cfg.PollIntervalSeconds)
	}
}

func TestExpandTemplate(t *testing.T) {
	vars := map[string]string{
		"title":             "Eternum",
		"creator":           "Caribdis",
		"installed_version": "0.8.5",
		"rating":            "4.9",
		"engine":            "Ren'Py",
	}

	tests := []struct {
		tmpl     string
		expected string
	}{
		{"{title}", "Eternum"},
		{"v{installed_version} • {creator}", "v0.8.5 • Caribdis"},
		{"{title} ({rating}/5)", "Eternum (4.9/5)"},
		{"Engine: {engine}", "Engine: Ren'Py"},
	}

	for _, tt := range tests {
		got := ExpandTemplate(tt.tmpl, vars)
		if got != tt.expected {
			t.Errorf("ExpandTemplate(%q) = %q; want %q", tt.tmpl, got, tt.expected)
		}
	}

	// Test edge cases with empty version, non-numeric version ("Final"), and empty creator
	edgeCases := []struct {
		vars     map[string]string
		tmpl     string
		expected string
	}{
		{map[string]string{"installed_version": "", "creator": "Caribdis"}, "v{installed_version} • {creator}", "Caribdis"},
		{map[string]string{"installed_version": "Final", "creator": "vatosgames"}, "v{installed_version} • {creator}", "Final • vatosgames"},
		{map[string]string{"installed_version": "v1.2", "creator": "Caribdis"}, "v{installed_version} • {creator}", "v1.2 • Caribdis"},
		{map[string]string{"installed_version": "", "creator": ""}, "v{installed_version} • {creator}", ""},
	}

	for _, tc := range edgeCases {
		got := ExpandTemplate(tc.tmpl, tc.vars)
		if got != tc.expected {
			t.Errorf("ExpandTemplate(%q, %v) = %q; want %q", tc.tmpl, tc.vars, got, tc.expected)
		}
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "config.yaml")

	cfg := DefaultConfig()
	cfg.Roots = []string{`C:\Games\VNs`, `D:\RenPy`}
	cfg.F95APIKey = "test_key_123"

	if err := Save(testPath, cfg); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	loaded, err := Load(testPath)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if loaded.F95APIKey != "test_key_123" {
		t.Errorf("expected API key test_key_123, got %s", loaded.F95APIKey)
	}
	if len(loaded.Roots) != 2 || loaded.Roots[0] != `C:\Games\VNs` {
		t.Errorf("unexpected roots: %v", loaded.Roots)
	}
}

func TestResolveConfigPath(t *testing.T) {
	// Test explicit path
	tmpDir := t.TempDir()
	explicit := filepath.Join(tmpDir, "custom.yaml")
	path, isDev, err := ResolveConfigPath(explicit)
	if err != nil {
		t.Fatalf("ResolveConfigPath explicit failed: %v", err)
	}
	if path != explicit || isDev {
		t.Errorf("expected explicit path %s (isDev=false), got %s (isDev=%t)", explicit, path, isDev)
	}
}
