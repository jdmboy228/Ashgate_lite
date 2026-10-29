package lite

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// settings is settings.json. The UI reads only the theme; the rest is the
// backend's own data (design docs/bridge-api.md).
type settings struct {
	Theme          string       `json:"theme"`
	Enabled        bool         `json:"enabled,omitempty"`
	CurrentProxyID string       `json:"currentProxyId,omitempty"`
	OthersProxyID  string       `json:"othersProxyId,omitempty"`
	Proxies        []savedProxy `json:"proxies,omitempty"`
	Apps           []savedApp   `json:"apps,omitempty"`
}

type savedProxy struct {
	ID   string `json:"id"`
	Slot int    `json:"slot"`
	Link string `json:"link"`
}

type savedApp struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	ExePath string `json:"exePath"`
	// Paused sends the app direct until it is switched back («Приостановить»).
	Paused bool `json:"paused,omitempty"`
	// Direct keeps the app off the proxy in all-traffic mode («Пустить напрямую»).
	Direct bool `json:"direct,omitempty"`
}

const (
	themeLight = "light"
	themeDark  = "dark"
)

// SettingsPath is settings.json in the user's configuration directory.
func SettingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Ashgate Lite", "settings.json"), nil
}

// loadSettings reads path. A missing file is a first run: light theme, no
// proxies. The Windows theme is never consulted.
func loadSettings(path string) (settings, error) {
	s := settings{Theme: themeLight}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return settings{Theme: themeLight}, err
	}
	if s.Theme != themeDark {
		s.Theme = themeLight
	}
	return s, nil
}

// save writes the file atomically and readable only by the user: proxy
// links can hold credentials.
func (s settings) save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
