package watcher

import (
	"path/filepath"
	"strings"
)

var defaultNoiseExes = map[string]bool{
	"unitycrashhandler32.exe":     true,
	"unitycrashhandler64.exe":     true,
	"crashreportclient.exe":       true,
	"crashreportclienteditor.exe": true,
	"unrealcefsubprocess.exe":     true,
	"unins000.exe":                true,
	"unins001.exe":                true,
	"uninstall.exe":               true,
	"setup.exe":                   true,
	"dxsetup.exe":                 true,
	"install.exe":                 true,
	"installer.exe":               true,
	"vc_redist.x64.exe":           true,
	"vc_redist.x86.exe":           true,
	"vcredist_x64.exe":            true,
	"vcredist_x86.exe":            true,
	"oalinst.exe":                 true,
	"epicgameslauncher.exe":       true,
	"steam.exe":                   true,
	"gog.exe":                     true,
	"goggalaxy.exe":               true,
	"playnite.desktopapp.exe":     true,
	"playnite.fullscreenapp.exe":  true,
	"f95checker.exe":              true,
	"nwjc.exe":                    true,
	"conhost.exe":                 true,
	"cmd.exe":                     true,
	"powershell.exe":              true,
	"pwsh.exe":                    true,
	"werfault.exe":                true,
	"werfaultsecure.exe":          true,
	"wermgr.exe":                  true,
}

// IsNoiseProcess returns true if the executable name matches known noise/helper binaries.
func IsNoiseProcess(exePath string) bool {
	base := strings.ToLower(filepath.Base(exePath))
	if defaultNoiseExes[base] {
		return true
	}
	// Prefix checks for redistributables and setup files
	if strings.HasPrefix(base, "vc_redist") || strings.HasPrefix(base, "vcredist") ||
		strings.HasPrefix(base, "unitycrashhandler") || strings.HasPrefix(base, "crashreport") {
		return true
	}
	return false
}
