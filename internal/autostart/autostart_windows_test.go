//go:build windows

package autostart

import (
	"crypto/rand"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func TestAutostart(t *testing.T) {
	isolateUserRegistry(t)
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

func TestIsEnabledWithoutRunKey(t *testing.T) {
	isolateUserRegistry(t)
	enabled, err := IsEnabled("f95rpc_test_autostart")
	if err != nil || enabled {
		t.Fatalf("IsEnabled with a missing Run key = (%v, %v), want (false, nil)", enabled, err)
	}
	assertRunKeyMissing(t)
}

func TestDisableWithoutRunKey(t *testing.T) {
	isolateUserRegistry(t)
	if err := Disable("f95rpc_test_autostart"); err != nil {
		t.Fatalf("Disable with a missing Run key should succeed: %v", err)
	}
	assertRunKeyMissing(t)
}

func TestEnableCreatesMissingRunKey(t *testing.T) {
	root := isolateUserRegistry(t)
	const appName = "f95rpc_test_autostart"
	const exePath = `C:\Program Files\f95rpc\f95rpc-tray.exe`
	if err := Enable(appName, exePath); err != nil {
		t.Fatalf("Enable should create a missing Run key: %v", err)
	}
	k, err := registry.OpenKey(root, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("open the newly created Run key: %v", err)
	}
	defer k.Close()
	value, _, err := k.GetStringValue(appName)
	if err != nil {
		t.Fatalf("read the startup command: %v", err)
	}
	if want := `"` + exePath + `"`; value != want {
		t.Fatalf("startup command = %q, want %q", value, want)
	}
}

// RegOverridePredefKey is process-wide: these tests must not run in parallel.
// Redirect HKCU in this test process so the public API never writes real startup entries.
func isolateUserRegistry(t *testing.T) registry.Key {
	t.Helper()
	override := windows.NewLazySystemDLL("advapi32.dll").NewProc("RegOverridePredefKey")
	if err := override.Find(); err != nil {
		t.Fatalf("find registry isolation API: %v", err)
	}
	parent, _, err := registry.CreateKey(registry.CURRENT_USER, "Software", registry.ALL_ACCESS)
	if err != nil {
		t.Fatalf("open the test registry parent: %v", err)
	}
	t.Cleanup(func() {
		if err := parent.Close(); err != nil {
			t.Errorf("close the test registry parent: %v", err)
		}
	})
	name := "f95rpc-autostart-test-" + rand.Text()
	root, exists, err := registry.CreateKey(parent, name, registry.ALL_ACCESS)
	if err != nil {
		t.Fatalf("create an isolated user registry: %v", err)
	}
	if exists {
		_ = root.Close()
		t.Fatal("the randomly named test registry already exists; do not modify it")
	}
	// Win32 defines predefined HKEYs as signed LONGs widened to pointer size.
	currentUser := registry.CURRENT_USER
	hCurrentUser := uintptr(int64(int32(currentUser)))
	overridden := false
	t.Cleanup(func() {
		if overridden {
			status, _, _ := override.Call(hCurrentUser, 0)
			if status != 0 {
				t.Errorf("restore the user registry: %v", syscall.Errno(status))
			}
		}
		// Delete only the private tree created by this test, deepest key first.
		for path := runKeyPath; path != ""; {
			if err := registry.DeleteKey(root, path); err != nil && !errors.Is(err, registry.ErrNotExist) {
				t.Errorf("remove the isolated registry key %q: %v", path, err)
			}
			separator := strings.LastIndexByte(path, '\\')
			if separator < 0 {
				break
			}
			path = path[:separator]
		}
		if err := root.Close(); err != nil {
			t.Errorf("close the isolated user registry: %v", err)
		}
		if err := registry.DeleteKey(parent, name); err != nil {
			t.Errorf("remove the isolated user registry: %v", err)
		}
	})
	status, _, _ := override.Call(hCurrentUser, uintptr(root))
	if status != 0 {
		t.Fatalf("isolate the user registry: %v", syscall.Errno(status))
	}
	overridden = true
	assertRunKeyMissing(t)
	return root
}

func assertRunKeyMissing(t *testing.T) {
	t.Helper()
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err == nil {
		_ = k.Close()
		t.Fatal("the Run key should not exist")
	}
	if !errors.Is(err, registry.ErrNotExist) {
		t.Fatalf("check for a missing Run key: %v", err)
	}
}
