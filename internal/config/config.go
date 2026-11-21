package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

type Config struct {
	F95APIKey           string                  `yaml:"f95api_key"`
	DiscordClientID     string                  `yaml:"discord_client_id"`
	Roots               []string                `yaml:"roots"`
	ExcludePaths        []string                `yaml:"exclude_paths"`
	SelectionMode       string                  `yaml:"selection_mode"`
	PollIntervalSeconds int                     `yaml:"poll_interval_seconds"`
	StealthMode         bool                    `yaml:"stealth_mode"`
	DefaultCoverMode    string                  `yaml:"default_cover_mode"`
	DefaultLogoURL      string                  `yaml:"default_logo_url"`
	ShowThreadButton    bool                    `yaml:"show_thread_button"`
	ShowRepoButton      bool                    `yaml:"show_repo_button"`
	RepoButtonLabel     string                  `yaml:"repo_button_label"`
	RepoButtonURL       string                  `yaml:"repo_button_url"`
	DisplayType         string                  `yaml:"display_type"`
	Templates           TemplatesConfig         `yaml:"templates"`
	Importers           ImportersConfig         `yaml:"importers"`
	Games               map[string]GameOverride `yaml:"games"`
}

type TemplatesConfig struct {
	Name            string `yaml:"name"`
	Details         string `yaml:"details"`
	State           string `yaml:"state"`
	LargeText       string `yaml:"large_text"`
	SmallText       string `yaml:"small_text"`
	ButtonLabel     string `yaml:"button_label"`
	RepoButtonLabel string `yaml:"repo_button_label,omitempty"`
}

type ImportersConfig struct {
	F95Checker F95CheckerImportConfig `yaml:"f95checker"`
	Playnite   PlayniteImportConfig   `yaml:"playnite"`
}

type F95CheckerImportConfig struct {
	DBPath    string `yaml:"db_path"`
	RootsOnly bool   `yaml:"roots_only"`
}

type PlayniteImportConfig struct {
	LibraryPath       string   `yaml:"library_path"`
	Categories        []string `yaml:"categories"`
	ExcludeCategories []string `yaml:"exclude_categories"`
	F95LinkOnly       bool     `yaml:"f95_link_only"`
	InstalledOnly     bool     `yaml:"installed_only"`
	RootsOnly         bool     `yaml:"roots_only"`
	IncludeHidden     bool     `yaml:"include_hidden"`
	AutoApproveLinked bool     `yaml:"auto_approve_linked"`
}

type GameOverride struct {
	Enabled          *bool  `yaml:"enabled,omitempty"`
	CoverMode        string `yaml:"cover_mode,omitempty"`
	CustomTitle      string `yaml:"custom_title,omitempty"`
	InstalledVersion string `yaml:"installed_version,omitempty"`
}

func DefaultConfig() *Config {
	return &Config{
		DiscordClientID:     "", // BYO Discord Application Client ID
		Roots:               []string{},
		ExcludePaths:        []string{"_mods", "backups", "installers", ".git", ".venv"},
		SelectionMode:       "lock_first",
		PollIntervalSeconds: 5,
		StealthMode:         false,
		DefaultCoverMode:    "f95_logo",
		DefaultLogoURL:      "https://f95zone.to/assets/images/logo-sqw192.png",
		ShowThreadButton:    true,
		ShowRepoButton:      true,
		RepoButtonLabel:     "GitHub",
		RepoButtonURL:       "https://github.com/TheInternetUse7/f95rpc",
		DisplayType:         "name",
		Templates: TemplatesConfig{
			Name:            "{title}",
			Details:         "v{installed_version} • {creator}",
			State:           "Engine: {engine}",
			LargeText:       "{title} ({rating}/5)",
			SmallText:       "Engine: {engine}",
			ButtonLabel:     "View on F95zone",
			RepoButtonLabel: "GitHub",
		},
		Importers: ImportersConfig{
			Playnite: PlayniteImportConfig{
				InstalledOnly:     true,
				AutoApproveLinked: true,
			},
		},
		Games: make(map[string]GameOverride),
	}
}

func AppDataDir() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	dir := filepath.Join(appData, "f95rpc")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func DefaultConfigPath() (string, error) {
	dir, err := AppDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// ResolveConfigPath locates the active configuration file.
// Priority:
//  1. explicitPath if non-empty
//  2. ./config.yaml in the current working directory (development mode)
//  3. %APPDATA%\f95rpc\config.yaml (production default)
func ResolveConfigPath(explicitPath string) (path string, isDev bool, err error) {
	if explicitPath != "" {
		abs, err := filepath.Abs(explicitPath)
		if err != nil {
			return "", false, err
		}
		return abs, false, nil
	}

	// Check current directory for config.yaml (repo root in development)
	localPath := "config.yaml"
	if fi, err := os.Stat(localPath); err == nil && !fi.IsDir() {
		abs, err := filepath.Abs(localPath)
		if err == nil {
			return abs, true, nil
		}
	}

	appDataPath, err := DefaultConfigPath()
	if err != nil {
		return "", false, err
	}
	return appDataPath, false, nil
}

func Load(path string) (*Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config file: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse yaml config: %w", err)
	}

	// Environment variable override for API key
	if envKey := os.Getenv("F95API_KEY"); envKey != "" {
		cfg.F95APIKey = envKey
	}
	if envClientID := os.Getenv("DISCORD_CLIENT_ID"); envClientID != "" {
		cfg.DiscordClientID = envClientID
	}

	return cfg, nil
}

func Save(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal yaml config: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

func ExpandTemplate(tmpl string, vars map[string]string) string {
	res := tmpl

	// Format installed_version cleanly if referenced as v{installed_version}
	ver := strings.TrimSpace(vars["installed_version"])
	if strings.Contains(res, "v{installed_version}") {
		if ver == "" {
			res = strings.ReplaceAll(res, "v{installed_version}", "")
		} else if strings.HasPrefix(strings.ToLower(ver), "v") || !unicode.IsDigit(rune(ver[0])) {
			// Already has 'v' prefix or is non-numeric like "Final"
			res = strings.ReplaceAll(res, "v{installed_version}", ver)
		} else {
			res = strings.ReplaceAll(res, "v{installed_version}", "v"+ver)
		}
	}

	for k, v := range vars {
		res = strings.ReplaceAll(res, "{"+k+"}", v)
	}

	// Clean up empty template remnants and dangling separators
	res = strings.ReplaceAll(res, "• •", "•")
	res = strings.TrimSpace(res)
	res = strings.Trim(res, "•- ")
	res = strings.TrimSpace(res)
	if res == "v" || res == "ver." || res == "•" || res == "-" {
		return ""
	}
	return res
}
