package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v1.0.3", "v1.0.2", true},
		{"v1.10.0", "v1.9.9", true},
		{"v2.0.0", "v1.99.99", true},
		{"v1.0.2", "v1.0.2", false},
		{"v1.0.1", "v1.0.2", false},
		{"v1.0.3", "dev", false},
		{"1.0.3", "v1.0.2", false},
		{"v1.0.3-rc1", "v1.0.2", false},
	}
	for _, c := range cases {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestChecksum(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	sums := []byte("0000  other.zip\n" + sum + "  PowerPilot_v1_linux_amd64.tar.gz\n" + strings.ToUpper(sum) + " *bin.zip\n")
	if got, err := checksum(sums, "PowerPilot_v1_linux_amd64.tar.gz"); err != nil || got != sum {
		t.Errorf("got %q, %v", got, err)
	}
	if got, err := checksum(sums, "bin.zip"); err != nil || got != sum {
		t.Errorf("binary-mode line: got %q, %v", got, err)
	}
	if _, err := checksum(sums, "missing.zip"); err == nil {
		t.Error("found a checksum for a file that isn't listed")
	}
}

// fakeInstall lays out an installed copy of PowerPilot for this OS and builds
// the release archive that should replace it. read returns the program's
// current contents.
func fakeInstall(t *testing.T, version string) (exe string, archive []byte, read func() string) {
	t.Helper()
	dir := t.TempDir()
	var buf bytes.Buffer
	switch runtime.GOOS {
	case "linux":
		exe = filepath.Join(dir, "powerpilot")
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		body := []byte("new program")
		name := strings.TrimSuffix(assetName(version), ".tar.gz") + "/powerpilot"
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write(body)
		tw.Close()
		gz.Close()
	case "windows":
		exe = filepath.Join(dir, "PowerPilot.exe")
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create(strings.TrimSuffix(assetName(version), ".zip") + "/PowerPilot.exe")
		w.Write([]byte("new program"))
		zw.Close()
	case "darwin":
		exe = filepath.Join(dir, "PowerPilot.app", "Contents", "MacOS", "powerpilot")
		zw := zip.NewWriter(&buf)
		for name, body := range map[string]string{
			"PowerPilot.app/Contents/MacOS/powerpilot": "new program",
			"PowerPilot.app/Contents/Info.plist":       "<plist/>",
		} {
			h := &zip.FileHeader{Name: name, Method: zip.Deflate}
			h.SetMode(0o755)
			w, _ := zw.CreateHeader(h)
			w.Write([]byte(body))
		}
		zw.Close()
	default:
		t.Skip("no release for", runtime.GOOS)
	}
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old program"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe, buf.Bytes(), func() string {
		b, _ := os.ReadFile(exe)
		return string(b)
	}
}

// fakeGitHub serves a latest release made of archive, listed in SHA256SUMS
// with sum (the archive's real checksum when empty). It counts requests.
func fakeGitHub(t *testing.T, version string, archive []byte, sum string) *atomic.Int32 {
	t.Helper()
	var hits atomic.Int32
	if sum == "" {
		h := sha256.Sum256(archive)
		sum = hex.EncodeToString(h[:])
	}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"tag_name": version,
			"html_url": srv.URL + "/notes",
			"assets": []map[string]string{
				{"name": assetName(version), "browser_download_url": srv.URL + "/asset"},
				{"name": "SHA256SUMS.txt", "browser_download_url": srv.URL + "/sums"},
			},
		})
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", sum, assetName(version))
	})
	t.Setenv("POWERPILOT_UPDATE_URL", srv.URL+"/latest")
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		mux.ServeHTTP(w, r)
	})
	return &hits
}

func TestCheckAndApply(t *testing.T) {
	exe, archive, read := fakeInstall(t, "v9.9.9")
	fakeGitHub(t, "v9.9.9", archive, "")
	u := New("v1.0.2", exe)

	st, err := u.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Available || st.Latest != "v9.9.9" || st.Blocked != "" || !strings.HasSuffix(st.URL, "/notes") {
		t.Fatalf("Check = %+v", st)
	}
	v, err := u.Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v != "v9.9.9" || read() != "new program" {
		t.Fatalf("Apply = %q, program now %q", v, read())
	}
	if fi, err := os.Stat(exe); err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm()&0o100 == 0) {
		t.Fatalf("installed program isn't executable: %v %v", fi.Mode(), err)
	}
	Cleanup(exe)
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), "*.*")); runtime.GOOS == "windows" && len(left) != 1 {
		t.Errorf("left behind: %v", left)
	}
}

func TestApplyRejectsBadChecksum(t *testing.T) {
	exe, archive, read := fakeInstall(t, "v9.9.9")
	fakeGitHub(t, "v9.9.9", archive, strings.Repeat("0", 64))
	if _, err := New("v1.0.2", exe).Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("Apply error = %v, want a checksum mismatch", err)
	}
	if read() != "old program" {
		t.Fatal("a download that failed its checksum was installed")
	}
}

func TestUpToDate(t *testing.T) {
	exe, archive, read := fakeInstall(t, "v1.0.2")
	fakeGitHub(t, "v1.0.2", archive, "")
	u := New("v1.0.2", exe)
	if st, err := u.Check(context.Background()); err != nil || st.Available {
		t.Fatalf("Check = %+v, %v", st, err)
	}
	if _, err := u.Apply(context.Background()); err == nil || read() != "old program" {
		t.Fatalf("Apply on the latest version: err %v, program %q", err, read())
	}
}

func TestDevBuildDoesNotUpdate(t *testing.T) {
	exe, archive, _ := fakeInstall(t, "v9.9.9")
	hits := fakeGitHub(t, "v9.9.9", archive, "")
	u := New("dev", exe)
	st, err := u.Check(context.Background())
	if err != nil || st.Blocked == "" || st.Available {
		t.Fatalf("Check = %+v, %v", st, err)
	}
	if _, err := u.Apply(context.Background()); !errors.Is(err, ErrBlocked) {
		t.Fatalf("Apply error = %v, want ErrBlocked", err)
	}
	if hits.Load() != 0 {
		t.Errorf("a development build asked GitHub %d times", hits.Load())
	}
}

func TestUnzipRejectsEscapes(t *testing.T) {
	for _, name := range []string{"../evil", "/abs/evil", "PowerPilot.app/../../evil"} {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create(name)
		w.Write([]byte("x"))
		zw.Close()
		archive := filepath.Join(t.TempDir(), "a.zip")
		os.WriteFile(archive, buf.Bytes(), 0o600)
		dir := t.TempDir()
		if err := unzip(archive, dir); err == nil {
			t.Errorf("unzip accepted %q", name)
		}
	}
}
