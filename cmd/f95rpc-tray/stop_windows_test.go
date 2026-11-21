//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestTrayStopRequestClosesSQLite(t *testing.T) {
	lockShutdownTests(t)
	mutexName, _ := windows.UTF16PtrFromString(AppMutexName)
	mutex, err := windows.OpenMutex(windows.SYNCHRONIZE, false, mutexName)
	if err == nil {
		_ = windows.CloseHandle(mutex)
		t.Skip("a real tray or another shutdown test is running")
	}
	if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		t.Fatalf("check tray mutex: %v", err)
	}
	eventName, _ := windows.UTF16PtrFromString(`Global\f95rpc_tray_stop`)
	event, err := windows.CreateEvent(nil, 1, 0, eventName)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if event != 0 {
			_ = windows.CloseHandle(event)
		}
		t.Skip("a tray shutdown event already exists")
	}
	if err != nil {
		t.Fatalf("create test stop event: %v", err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(event) })

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("discord_client_id: \"\"\nf95api_key: \"\"\nroots: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestTrayStopHelperProcess$")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "F95RPC_TEST_TRAY_HELPER=1", "DISCORD_CLIENT_ID=", "F95API_KEY=")
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})

	dbPath := filepath.Join(dir, "f95rpc.db")
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	startup := time.NewTimer(5 * time.Second)
	defer startup.Stop()
	for {
		if _, err := os.Stat(dbPath + "-wal"); err == nil {
			break
		}
		select {
		case <-done:
			t.Fatalf("tray exited during startup: %v", waitErr)
		case <-startup.C:
			t.Fatal("tray did not open its SQLite database")
		case <-ticker.C:
		}
	}
	if err := windows.SetEvent(event); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		if waitErr != nil {
			t.Fatalf("tray did not exit gracefully: %v", waitErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tray ignored the graceful stop request")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(dbPath + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("tray did not close SQLite: %s still exists (err=%v)", suffix, err)
		}
	}
}

func TestTrayStopHelperProcess(t *testing.T) {
	if os.Getenv("F95RPC_TEST_TRAY_HELPER") == "1" {
		main()
	}
}

func lockShutdownTests(t *testing.T) {
	t.Helper()
	runtime.LockOSThread()
	name, _ := windows.UTF16PtrFromString(`Global\f95rpc_shutdown_test_mutex`)
	mutex, err := windows.CreateMutex(nil, false, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		runtime.UnlockOSThread()
		t.Fatalf("create shutdown test lock: %v", err)
	}
	result, err := windows.WaitForSingleObject(mutex, 10000)
	if err != nil || (result != windows.WAIT_OBJECT_0 && result != windows.WAIT_ABANDONED) {
		_ = windows.CloseHandle(mutex)
		runtime.UnlockOSThread()
		t.Fatalf("acquire shutdown test lock: result=%d, err=%v", result, err)
	}
	t.Cleanup(func() {
		_ = windows.ReleaseMutex(mutex)
		_ = windows.CloseHandle(mutex)
		runtime.UnlockOSThread()
	})
}
