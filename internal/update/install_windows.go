package update

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

func assetName(version string) string {
	return fmt.Sprintf("PowerPilot_%s_windows_%s.zip", version, runtime.GOARCH)
}

func installDir(exe string) (string, error) {
	return filepath.Dir(exe), nil
}

// install moves the running program aside, which Windows allows (unlike
// overwriting it), and puts the new one in its place. Cleanup removes the old
// one after the restart.
func install(archive, exe string) error {
	tmp, err := writeNew(exe, func(w io.Writer) error { return copyFromZip(archive, "PowerPilot.exe", w) })
	if err != nil {
		return err
	}
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Rename(old, exe)
		os.Remove(tmp)
		return err
	}
	return nil
}
