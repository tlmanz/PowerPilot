package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "PowerPilot", "window.json")
	if _, ok := LoadWindow(path); ok {
		t.Fatal("LoadWindow found a size before any was saved")
	}
	want := Window{Width: 1300, Height: 792}
	if err := SaveWindow(path, want); err != nil {
		t.Fatal(err)
	}
	if got, ok := LoadWindow(path); !ok || got != want {
		t.Fatalf("LoadWindow = %+v, %v; want %+v, true", got, ok, want)
	}
}

func TestWindowRejectsBadSizes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "window.json")
	if err := SaveWindow(path, Window{Width: 10, Height: 10}); err == nil {
		t.Error("SaveWindow accepted a 10x10 window")
	}
	for _, data := range []string{`{"width":0,"height":0}`, `not json`} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if w, ok := LoadWindow(path); ok {
			t.Errorf("LoadWindow(%q) = %+v, want none", data, w)
		}
	}
}
