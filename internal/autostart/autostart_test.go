package autostart

import (
	"os"
	"testing"
)

func TestAutostart(t *testing.T) {
	appName := "f95rpc_test_autostart"
	exePath, _ := os.Executable()

	// Ensure clean state
	_ = Disable(appName)

	// Check initially disabled
	enabled, err := IsEnabled(appName)
	if err != nil {
		t.Fatalf("IsEnabled failed: %v", err)
	}
	if enabled {
		t.Errorf("expected disabled initially, got %v", enabled)
	}

	// Enable
	if err := Enable(appName, exePath); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}

	// Check enabled
	enabled, err = IsEnabled(appName)
	if err != nil {
		t.Fatalf("IsEnabled after enable failed: %v", err)
	}
	if !enabled {
		t.Errorf("expected enabled after Enable, got %v", enabled)
	}

	// Disable
	if err := Disable(appName); err != nil {
		t.Fatalf("Disable failed: %v", err)
	}

	// Check disabled
	enabled, err = IsEnabled(appName)
	if err != nil {
		t.Fatalf("IsEnabled after disable failed: %v", err)
	}
	if enabled {
		t.Errorf("expected disabled after Disable, got %v", enabled)
	}
}
