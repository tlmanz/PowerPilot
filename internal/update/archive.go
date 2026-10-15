package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
)

// copyFromTarGz writes the regular file called name, in any folder, to w.
func copyFromTarGz(archive, name string, w io.Writer) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("the download has no %s", name)
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeReg && path.Base(h.Name) == name {
			_, err := io.Copy(w, io.LimitReader(tr, maxArchive))
			return err
		}
	}
}

// copyFromZip writes the regular file called name, in any folder, to w.
func copyFromZip(archive, name string, w io.Writer) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Mode().IsRegular() && path.Base(f.Name) == name {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			_, err = io.Copy(w, io.LimitReader(rc, maxArchive))
			return err
		}
	}
	return fmt.Errorf("the download has no %s", name)
}

// unzip extracts archive into dir. It refuses anything but plain folders and
// files, and any path that would land outside dir.
func unzip(archive, dir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		name := filepath.FromSlash(f.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("unsafe path in the download: %s", f.Name)
		}
		dst := filepath.Join(dir, name)
		switch {
		case f.Mode().IsDir():
			err = os.MkdirAll(dst, 0o755)
		case f.Mode().IsRegular():
			err = extract(f, dst)
		default:
			err = fmt.Errorf("unexpected file type in the download: %s", f.Name)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func extract(f *zip.File, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, f.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, io.LimitReader(rc, maxArchive))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// writeNew fills exe+".new", next to exe so it can be renamed over it, and
// returns its path.
func writeNew(exe string, fill func(io.Writer) error) (string, error) {
	tmp := exe + ".new"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	err = fill(f)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}
