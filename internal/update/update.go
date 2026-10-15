// Package update checks GitHub for a newer PowerPilot release and installs it
// over the running program, then restarts the service on the new version.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// UpdatedFromEnv tells the restarted service which version it replaced.
const UpdatedFromEnv = "POWERPILOT_UPDATED_FROM"

const (
	sumsName   = "SHA256SUMS.txt"
	maxArchive = 256 << 20
)

// ErrBlocked wraps the reason this copy can't update itself.
var ErrBlocked = errors.New("can't update itself")

// latestURL is the newest published release; GitHub skips drafts and
// pre-releases. POWERPILOT_UPDATE_URL points it at a test server.
func latestURL() string {
	if u := os.Getenv("POWERPILOT_UPDATE_URL"); u != "" {
		return u
	}
	return "https://api.github.com/repos/tlmanz/PowerPilot/releases/latest"
}

// Status is what Settings shows about updates.
type Status struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	URL       string `json:"url,omitempty"`
	// Blocked says why this copy can't update itself, when it can't.
	Blocked string `json:"blocked,omitempty"`
}

type Updater struct {
	current string
	exe     string // the running program, symlinks resolved
	client  *http.Client
	busy    atomic.Bool
}

// New returns an updater for the running program exe at version current.
func New(current, exe string) *Updater {
	return &Updater{current: current, exe: exe, client: &http.Client{Timeout: 10 * time.Minute}}
}

type release struct {
	Tag    string `json:"tag_name"`
	URL    string `json:"html_url"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (u *Updater) Check(ctx context.Context) (Status, error) {
	st := Status{Current: u.current, Blocked: u.blocked()}
	if _, ok := parseVersion(u.current); !ok {
		return st, nil
	}
	r, err := u.latest(ctx)
	if err != nil {
		return st, err
	}
	st.Latest, st.URL, st.Available = r.Tag, r.URL, newer(r.Tag, u.current)
	return st, nil
}

// Apply downloads the latest release for this system, checks it against the
// release's SHA256SUMS.txt and installs it over the running program. It
// returns the installed version; Restart switches to it.
func (u *Updater) Apply(ctx context.Context) (string, error) {
	if !u.busy.CompareAndSwap(false, true) {
		return "", errors.New("an update is already in progress")
	}
	defer u.busy.Store(false)
	if b := u.blocked(); b != "" {
		return "", fmt.Errorf("%w: %s", ErrBlocked, b)
	}
	r, err := u.latest(ctx)
	if err != nil {
		return "", err
	}
	if !newer(r.Tag, u.current) {
		return "", fmt.Errorf("%s is already the latest version", u.current)
	}
	name := assetName(r.Tag)
	var assetURL, sumsURL string
	for _, a := range r.Assets {
		switch a.Name {
		case name:
			assetURL = a.URL
		case sumsName:
			sumsURL = a.URL
		}
	}
	if assetURL == "" || sumsURL == "" {
		return "", fmt.Errorf("release %s has no %s or %s", r.Tag, name, sumsName)
	}
	sums, err := u.fetch(ctx, sumsURL)
	if err != nil {
		return "", err
	}
	want, err := checksum(sums, name)
	if err != nil {
		return "", err
	}
	archive, err := u.download(ctx, assetURL, want)
	if err != nil {
		return "", err
	}
	defer os.Remove(archive)
	if err := install(archive, u.exe); err != nil {
		return "", fmt.Errorf("installing %s: %w", r.Tag, err)
	}
	return r.Tag, nil
}

// Restart runs the newly installed program in place of this one, keeping the
// command line. It returns only if that fails.
func (u *Updater) Restart() error {
	os.Setenv(UpdatedFromEnv, u.current)
	return restart(u.exe)
}

// Cleanup removes what an earlier update left next to exe. On Windows the
// previous program stays locked until it has exited, so it retries a while.
func Cleanup(exe string) {
	for range 30 {
		left := false
		for _, p := range []string{exe + ".old", exe + ".new"} {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				left = true
			}
		}
		if !left {
			return
		}
		time.Sleep(time.Second)
	}
}

func (u *Updater) blocked() string {
	if _, ok := parseVersion(u.current); !ok {
		return "This is a development build, which doesn't update itself."
	}
	dir, err := installDir(u.exe)
	if err != nil {
		return err.Error() + "."
	}
	f, err := os.CreateTemp(dir, ".powerpilot-write-test-")
	if err != nil {
		return fmt.Sprintf("PowerPilot can't write to %s, so update it by hand.", dir)
	}
	f.Close()
	os.Remove(f.Name())
	return ""
}

func (u *Updater) latest(ctx context.Context) (release, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	body, err := u.get(ctx, latestURL(), 1<<20)
	if err != nil {
		return release{}, err
	}
	var r release
	if err := json.Unmarshal(body, &r); err != nil || r.Tag == "" {
		return release{}, errors.New("unexpected reply from GitHub")
	}
	return r, nil
}

func (u *Updater) fetch(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return u.get(ctx, url, 1<<16)
}

func (u *Updater) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	resp, err := u.do(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func (u *Updater) do(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "PowerPilot/"+u.current)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach GitHub: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub answered %s for %s", resp.Status, filepath.Base(url))
	}
	return resp, nil
}

// download saves url to a temp file and checks its SHA-256 against want.
func (u *Updater) download(ctx context.Context, url, want string) (string, error) {
	resp, err := u.do(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	f, err := os.CreateTemp("", "powerpilot-update-*")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxArchive))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != want {
		err = errors.New("the download doesn't match its published checksum")
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// checksum finds name's SHA-256 in a sha256sum listing.
func checksum(sums []byte, name string) (string, error) {
	for line := range strings.Lines(string(sums)) {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			if b, err := hex.DecodeString(f[0]); err == nil && len(b) == sha256.Size {
				return strings.ToLower(f[0]), nil
			}
		}
	}
	return "", fmt.Errorf("%s doesn't list %s", sumsName, name)
}

// parseVersion reads a release tag like "v1.2.3".
func parseVersion(v string) ([]int, bool) {
	s, ok := strings.CutPrefix(v, "v")
	parts := strings.Split(s, ".")
	if !ok || len(parts) != 3 {
		return nil, false
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		nums[i] = n
	}
	return nums, true
}

// newer reports whether version a is later than version b.
func newer(a, b string) bool {
	va, okA := parseVersion(a)
	vb, okB := parseVersion(b)
	return okA && okB && slices.Compare(va, vb) > 0
}
