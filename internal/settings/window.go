package settings

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
)

// Window is the size of PowerPilot's own dashboard window on Linux and
// Windows, in device-independent pixels as the browser's --window-size takes
// it. macOS remembers its native window's frame by itself.
type Window struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (w Window) Validate() error {
	if w.Width < 400 || w.Width > 16384 || w.Height < 300 || w.Height > 16384 {
		return errors.New("window size out of range")
	}
	return nil
}

var windowMu sync.Mutex

// LoadWindow returns the size saved at path; ok is false when there is none
// or it's unusable.
func LoadWindow(path string) (w Window, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &w) != nil || w.Validate() != nil {
		return Window{}, false
	}
	return w, true
}

func SaveWindow(path string, w Window) error {
	if err := w.Validate(); err != nil {
		return err
	}
	windowMu.Lock()
	defer windowMu.Unlock()
	return writeJSON(path, w)
}
