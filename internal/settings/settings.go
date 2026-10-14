// Package settings persists the dashboard's preferences as a small JSON file.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Settings struct {
	LowBatteryPercent    int  `json:"low_battery_percent"`
	Notify               bool `json:"notify"`
	ShutdownMac          bool `json:"shutdown_mac"`
	ShutdownAfterMinutes int  `json:"shutdown_after_minutes"`
	// Amp-hours of each 12 V battery block; only used for the runtime estimate.
	BatteryAh int `json:"battery_ah"`
}

func Default() Settings {
	return Settings{LowBatteryPercent: 20, Notify: true, ShutdownAfterMinutes: 5, BatteryAh: 9}
}

func (s Settings) Validate() error {
	if s.LowBatteryPercent < 5 || s.LowBatteryPercent > 90 {
		return errors.New("low battery threshold must be between 5% and 90%")
	}
	if s.ShutdownAfterMinutes < 1 || s.ShutdownAfterMinutes > 240 {
		return errors.New("shutdown delay must be between 1 and 240 minutes")
	}
	if s.BatteryAh < 1 || s.BatteryAh > 200 {
		return errors.New("battery capacity must be between 1 and 200 Ah")
	}
	return nil
}

type Store struct {
	path string
	mu   sync.Mutex
	cur  Settings
}

// Open loads path, falling back to defaults when the file doesn't exist yet.
func Open(path string) (*Store, error) {
	s := &Store{path: path, cur: Default()}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	loaded := Default()
	if err := json.Unmarshal(data, &loaded); err != nil {
		return s, fmt.Errorf("%s: %w", path, err)
	}
	if err := loaded.Validate(); err != nil {
		return s, fmt.Errorf("%s: %w", path, err)
	}
	s.cur = loaded
	return s, nil
}

func (s *Store) Get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

func (s *Store) Set(v Settings) error {
	if err := v.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.cur = v
	return nil
}
