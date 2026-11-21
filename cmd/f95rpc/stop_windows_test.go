//go:build windows

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"f95rpc/internal/store"
)

func TestMain(m *testing.M) {
	// Never let an old stop implementation force-kill a real tray during tests.
	if os.Getenv("F95RPC_TEST_TASKKILL_STUB") == "1" && strings.EqualFold(filepath.Base(os.Args[0]), "taskkill.exe") {
		fmt.Println("Forced termination suppressed by the regression test.")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestStopGracefullyClosesSQLite(t *testing.T) {
	lockShutdownTests(t)
	mutexName, _ := windows.UTF16PtrFromString(`Global\f95rpc_tray_mutex`)
	mutex, err := windows.CreateMutex(nil, false, mutexName)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if mutex != 0 {
			_ = windows.CloseHandle(mutex)
		}
		t.Skip("a real tray is running; do not send it test shutdown requests")
	}
	if err != nil {
		t.Fatalf("reserve tray mutex: %v", err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(mutex) })

	eventName, _ := windows.UTF16PtrFromString(`Global\f95rpc_tray_stop`)
	event, err := windows.CreateEvent(nil, 1, 0, eventName)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if event != 0 {
			_ = windows.CloseHandle(event)
		}
		t.Skip("a tray shutdown event already exists")
	}
	if err != nil {
		t.Fatalf("create test shutdown event: %v", err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(event) })

	dir := t.TempDir()
	t.Chdir(dir)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	testBinary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "taskkill.exe"), testBinary, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("F95RPC_TEST_TASKKILL_STUB", "1")

	dbPath := filepath.Join(dir, "test.db")
	cmd := exec.Command(executable, "-test.run=^TestStopHelperProcess$")
	cmd.Env = append(os.Environ(), "F95RPC_TEST_STOP_HELPER=1", "F95RPC_TEST_DB="+dbPath)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err == nil && strings.TrimSpace(line) != "READY" {
			err = fmt.Errorf("unexpected helper output: %q", line)
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("helper startup: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not open its database")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(dbPath + suffix); err != nil {
			t.Fatalf("expected live SQLite sidecar %s: %v", suffix, err)
		}
	}

	handleStop(nil)

	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(dbPath + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stop returned without clean SQLite shutdown: %s still exists (err=%v)", suffix, err)
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper did not exit gracefully: %v", err)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	game, err := s.GetGameByFolder(`C:\Games\Eternum`)
	if err != nil || game == nil || game.Title != "Eternum" {
		t.Fatalf("database changes were not preserved: game=%v, err=%v", game, err)
	}
}

func TestStopHelperProcess(t *testing.T) {
	if os.Getenv("F95RPC_TEST_STOP_HELPER") != "1" {
		return
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	mutexName, _ := windows.UTF16PtrFromString(`Global\f95rpc_tray_mutex`)
	mutex, err := windows.OpenMutex(windows.MUTEX_ALL_ACCESS, false, mutexName)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(mutex)
	if result, err := windows.WaitForSingleObject(mutex, 5000); err != nil || result != windows.WAIT_OBJECT_0 {
		t.Fatalf("acquire test tray mutex: result=%d, err=%v", result, err)
	}
	defer windows.ReleaseMutex(mutex)

	eventName, _ := windows.UTF16PtrFromString(`Global\f95rpc_tray_stop`)
	event, err := windows.OpenEvent(windows.SYNCHRONIZE, false, eventName)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(event)
	s, err := store.Open(os.Getenv("F95RPC_TEST_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.UpsertGame(&store.GameRecord{
		FolderPath: `C:\Games\Eternum`,
		Title:      "Eternum",
		Approved:   true,
	}); err != nil {
		t.Fatal(err)
	}
	fmt.Println("READY")
	if result, err := windows.WaitForSingleObject(event, 30000); err != nil || result != windows.WAIT_OBJECT_0 {
		t.Fatalf("receive graceful stop: result=%d, err=%v", result, err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close SQLite: %v", err)
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
