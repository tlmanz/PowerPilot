package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func assetName(version string) string {
	return fmt.Sprintf("PowerPilot_%s_macOS_universal.zip", version)
}

// appBundle returns the .app that contains exe.
func appBundle(exe string) (string, error) {
	macOS := filepath.Dir(exe)
	app := filepath.Dir(filepath.Dir(macOS))
	if filepath.Base(macOS) != "MacOS" || filepath.Ext(app) != ".app" {
		return "", errors.New("PowerPilot isn't running from PowerPilot.app, so it can't update itself")
	}
	return app, nil
}

func installDir(exe string) (string, error) {
	app, err := appBundle(exe)
	if err != nil {
		return "", err
	}
	return filepath.Dir(app), nil
}

// install swaps the whole app bundle; the running service keeps the old
// program open until it restarts.
func install(archive, exe string) error {
	app, err := appBundle(exe)
	if err != nil {
		return err
	}
	// Unpacked next to the bundle, so the swap is a rename on one volume.
	work, err := os.MkdirTemp(filepath.Dir(app), ".PowerPilot-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := unzip(archive, work); err != nil {
		return err
	}
	fresh := filepath.Join(work, "PowerPilot.app")
	if _, err := os.Stat(filepath.Join(fresh, "Contents", "MacOS", filepath.Base(exe))); err != nil {
		return errors.New("the download has no PowerPilot.app")
	}
	old := filepath.Join(work, "previous.app")
	if err := os.Rename(app, old); err != nil {
		return err
	}
	if err := os.Rename(fresh, app); err != nil {
		os.Rename(old, app)
		return err
	}
	return nil
}
