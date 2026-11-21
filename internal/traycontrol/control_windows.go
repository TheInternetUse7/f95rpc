//go:build windows

package traycontrol

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const MutexName = `Global\f95rpc_tray_mutex`
const stopEventName = `Global\f95rpc_tray_stop`

var ErrNotRunning = errors.New("tray companion is not running")

// StopListener receives shutdown requests from the CLI without killing the tray.
type StopListener struct {
	Requested <-chan struct{}
	handle    windows.Handle
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func ListenForStop() (*StopListener, error) {
	name, err := windows.UTF16PtrFromString(stopEventName)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateEvent(nil, 1, 0, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, fmt.Errorf("create tray stop event: %w", err)
	}
	requested := make(chan struct{})
	l := &StopListener{Requested: requested, handle: handle, done: make(chan struct{})}
	go func() {
		defer close(l.done)
		result, err := windows.WaitForSingleObject(handle, windows.INFINITE)
		if err == nil && result == windows.WAIT_OBJECT_0 {
			close(requested)
		}
	}()
	return l, nil
}

// Close wakes the waiter before closing its handle, including on a menu Quit.
func (l *StopListener) Close() error {
	l.closeOnce.Do(func() {
		if err := windows.SetEvent(l.handle); err != nil {
			l.closeErr = err
			return
		}
		<-l.done
		l.closeErr = windows.CloseHandle(l.handle)
	})
	return l.closeErr
}

// RequestStop waits until the tray releases its instance mutex after cleanup.
// It deliberately does not fall back to forced termination on failure.
func RequestStop(timeout time.Duration) error {
	mutexName, err := windows.UTF16PtrFromString(MutexName)
	if err != nil {
		return err
	}
	mutex, err := windows.OpenMutex(windows.SYNCHRONIZE|windows.MUTEX_MODIFY_STATE, false, mutexName)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return ErrNotRunning
	}
	if err != nil {
		return fmt.Errorf("open tray mutex: %w", err)
	}
	defer windows.CloseHandle(mutex)
	stopName, err := windows.UTF16PtrFromString(stopEventName)
	if err != nil {
		return err
	}
	event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, stopName)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return fmt.Errorf("tray is still starting or uses an older build; use Quit from its tray menu and restart")
	}
	if err != nil {
		return fmt.Errorf("open tray stop event: %w", err)
	}
	defer windows.CloseHandle(event)
	if err := windows.SetEvent(event); err != nil {
		return fmt.Errorf("request tray shutdown: %w", err)
	}

	// Win32 mutex ownership belongs to an OS thread, not a Go goroutine.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	result, err := windows.WaitForSingleObject(mutex, uint32(timeout/time.Millisecond))
	if err != nil {
		return fmt.Errorf("wait for tray shutdown: %w", err)
	}
	switch result {
	case windows.WAIT_OBJECT_0, windows.WAIT_ABANDONED:
		return windows.ReleaseMutex(mutex)
	case uint32(windows.WAIT_TIMEOUT):
		return fmt.Errorf("timed out waiting for tray cleanup; the tray was not force-killed")
	default:
		return fmt.Errorf("unexpected tray shutdown wait result: %d", result)
	}
}
