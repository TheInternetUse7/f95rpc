//go:build !windows

package autostart

func IsEnabled(appName string) (bool, error) {
	return false, nil
}

func Enable(appName, exePath string) error {
	return nil
}

func Disable(appName string) error {
	return nil
}
