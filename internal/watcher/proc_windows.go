//go:build windows

package watcher

import (
	"os"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
)

type WindowsProcessEnumerator struct{}

func NewProcessEnumerator() ProcessEnumerator {
	return &WindowsProcessEnumerator{}
}

func (e *WindowsProcessEnumerator) EnumerateProcesses() ([]ProcessInfo, uint32, error) {
	// 1. Get foreground window PID and visible window titles
	titles, foregroundPID := getWindowTitlesAndForeground()

	// 2. Take process snapshot
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, 0, err
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	var procs []ProcessInfo
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		pid := entry.ProcessID
		if pid <= 4 { // Skip System Idle and System
			continue
		}

		exePath := getProcessImagePath(pid)
		if exePath == "" {
			continue
		}

		procs = append(procs, ProcessInfo{
			PID:          pid,
			ParentPID:    entry.ParentProcessID,
			ExePath:      exePath,
			WindowTitle:  titles[pid],
			IsForeground: pid == foregroundPID,
		})
	}

	return procs, foregroundPID, nil
}

func getProcessImagePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, 1024)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

func getWindowTitlesAndForeground() (map[uint32]string, uint32) {
	titles := make(map[uint32]string)

	fgHwnd, _, _ := procGetForegroundWindow.Call()
	var fgPID uint32
	if fgHwnd != 0 {
		procGetWindowThreadProcessId.Call(fgHwnd, uintptr(unsafe.Pointer(&fgPID)))
	}

	selfPID := uint32(os.Getpid())
	cb := syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
		vis, _, _ := procIsWindowVisible.Call(hwnd)
		if vis == 0 {
			return 1 // continue
		}

		var pid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid <= 4 || pid == selfPID {
			return 1
		}

		lenRet, _, _ := procGetWindowTextLengthW.Call(hwnd)
		if lenRet == 0 {
			return 1
		}

		buf := make([]uint16, lenRet+1)
		procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(lenRet+1))
		title := strings.TrimSpace(windows.UTF16ToString(buf))
		if title == "" {
			return 1
		}

		if existing, ok := titles[pid]; !ok || len(title) > len(existing) {
			titles[pid] = title
		}

		return 1
	})

	procEnumWindows.Call(cb, 0)
	return titles, fgPID
}
