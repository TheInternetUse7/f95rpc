//go:build windows

package autostart

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// IsEnabled checks whether appName is registered to run on Windows startup.
func IsEnabled(appName string) (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false, err
	}
	defer k.Close()

	val, _, err := k.GetStringValue(appName)
	if err != nil {
		if err == registry.ErrNotExist {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(val) != "", nil
}

// Enable registers appName to start automatically on Windows logon with exePath.
func Enable(appName, exePath string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open registry run key: %w", err)
	}
	defer k.Close()

	cmd := fmt.Sprintf("\"%s\"", exePath)
	return k.SetStringValue(appName, cmd)
}

// Disable removes appName from Windows startup.
func Disable(appName string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open registry run key: %w", err)
	}
	defer k.Close()

	err = k.DeleteValue(appName)
	if err == registry.ErrNotExist {
		return nil
	}
	return err
}
