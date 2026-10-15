package update

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

func assetName(version string) string {
	return fmt.Sprintf("PowerPilot_%s_linux_%s.tar.gz", version, runtime.GOARCH)
}

func installDir(exe string) (string, error) {
	return filepath.Dir(exe), nil
}

// install renames the new program over the old one; the running service keeps
// the old file open until it restarts.
func install(archive, exe string) error {
	tmp, err := writeNew(exe, func(w io.Writer) error { return copyFromTarGz(archive, "powerpilot", w) })
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
