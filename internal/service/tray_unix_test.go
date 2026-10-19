//go:build !windows

package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrayClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tray.pid")
	if _, ok := trayPID(path); ok {
		t.Fatal("a tray before any claim")
	}
	f, ok := claim(path, 0)
	if f == nil || !ok {
		t.Fatal("first claim failed")
	}
	if pid, ok := trayPID(path); !ok || pid != os.Getpid() {
		t.Fatalf("tray process %d, %v", pid, ok)
	}
	start := time.Now()
	if g, ok := claim(path, 300*time.Millisecond); g != nil || ok {
		t.Fatal("a second claim must fail while the first holds the icon")
	}
	if time.Since(start) < 300*time.Millisecond {
		t.Fatal("the second claim didn't wait")
	}

	f.Close() // the process showing the icon exits
	if _, ok := trayPID(path); ok {
		t.Fatal("a stale ID still counts")
	}
	g, ok := claim(path, 0)
	if g == nil || !ok {
		t.Fatal("claim after release failed")
	}
	g.Close()
}
