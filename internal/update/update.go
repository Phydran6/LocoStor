// Package update implements self-update from GitHub Releases with
// one-step rollback.
//
// Each release ships raw binaries named locostor-linux-<arch> and a
// SHA256SUMS file. Updating downloads the binary for the running
// architecture, verifies its checksum, keeps the running binary as
// <exe>.previous and swaps the new one in. Rollback swaps them back.
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Phydran6/LocoStor/internal/sysexec"
)

// Status is the update state shown in the UI.
type Status struct {
	Current         string    `json:"current"`
	Latest          string    `json:"latest"`
	UpdateAvailable bool      `json:"update_available"`
	ReleaseURL      string    `json:"release_url"`
	ReleaseNotes    string    `json:"release_notes"`
	PublishedAt     time.Time `json:"published_at"`
	CheckedAt       time.Time `json:"checked_at"`
	Error           string    `json:"error,omitempty"`
	CanUpdate       bool      `json:"can_update"`
	DisabledReason  string    `json:"disabled_reason,omitempty"`
	PreviousVersion string    `json:"previous_version,omitempty"`
	Busy            bool      `json:"busy"`
}

type release struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Options configures an Updater.
type Options struct {
	Repo    string // owner/name
	Current string // running version
	// Disabled, if non-empty, blocks apply/rollback with this reason.
	Disabled string
	// Restart is called after a successful swap to restart the service.
	Restart func()
	Run     sysexec.Runner
}

// Updater checks for and applies updates.
type Updater struct {
	opts   Options
	client *http.Client
	exe    string

	mu     sync.Mutex
	status Status
	latest *release
	busy   bool

	prevMod     time.Time
	prevVersion string
}

// New creates an Updater.
func New(opts Options) *Updater {
	exe, err := os.Executable()
	if err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
	}
	u := &Updater{opts: opts, client: &http.Client{Timeout: 5 * time.Minute}, exe: exe}
	u.status = Status{Current: opts.Current}
	return u
}

// AssetName returns the binary asset name for the running architecture.
func AssetName() string { return "locostor-linux-" + runtime.GOARCH }

func (u *Updater) previousPath() string { return u.exe + ".previous" }

// Status returns the last known status.
func (u *Updater) Status(ctx context.Context) Status {
	u.mu.Lock()
	s := u.status
	s.Busy = u.busy
	u.mu.Unlock()
	u.fillLocal(ctx, &s)
	return s
}

func (u *Updater) fillLocal(ctx context.Context, s *Status) {
	s.DisabledReason = u.disabledReason()
	s.CanUpdate = s.DisabledReason == "" && s.UpdateAvailable
	s.PreviousVersion = ""
	if s.DisabledReason == "" {
		s.PreviousVersion = u.previousVersion(ctx)
	}
}

// previousVersion returns the version of the rollback binary, cached by
// modification time so the status endpoint doesn't spawn a process each call.
func (u *Updater) previousVersion(ctx context.Context) string {
	fi, err := os.Stat(u.previousPath())
	if err != nil {
		return ""
	}
	u.mu.Lock()
	if fi.ModTime().Equal(u.prevMod) {
		v := u.prevVersion
		u.mu.Unlock()
		return v
	}
	u.mu.Unlock()
	v := u.binaryVersion(ctx, u.previousPath())
	u.mu.Lock()
	u.prevMod, u.prevVersion = fi.ModTime(), v
	u.mu.Unlock()
	return v
}

func (u *Updater) disabledReason() string {
	switch {
	case u.opts.Disabled != "":
		return u.opts.Disabled
	case runtime.GOOS != "linux":
		return "self-update is only supported on Linux"
	case !parseSemver(u.opts.Current).ok:
		return "development build - self-update disabled"
	case u.exe == "":
		return "cannot determine executable path"
	}
	return ""
}

func (u *Updater) binaryVersion(ctx context.Context, path string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := u.opts.Run.Run(ctx, "", path, "version")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// Check queries GitHub for the latest release.
func (u *Updater) Check(ctx context.Context) Status {
	rel, err := u.fetchLatest(ctx)
	u.mu.Lock()
	u.status.CheckedAt = time.Now()
	if err != nil {
		u.status.Error = err.Error()
	} else {
		u.latest = rel
		u.status.Error = ""
		u.status.Latest = strings.TrimPrefix(rel.TagName, "v")
		u.status.ReleaseURL = rel.HTMLURL
		u.status.ReleaseNotes = rel.Body
		u.status.PublishedAt = rel.PublishedAt
		u.status.UpdateAvailable = Newer(rel.TagName, u.opts.Current)
	}
	u.mu.Unlock()
	return u.Status(ctx)
}

// RunPeriodic checks for updates now and then every interval until ctx ends.
func (u *Updater) RunPeriodic(ctx context.Context, interval time.Duration) {
	for {
		s := u.Check(ctx)
		if s.Error != "" {
			log.Printf("update check: %s", s.Error)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (u *Updater) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "LocoStor/"+u.opts.Current)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, nil
}

func (u *Updater) fetchLatest(ctx context.Context) (*release, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resp, err := u.get(ctx, "https://api.github.com/repos/"+u.opts.Repo+"/releases/latest")
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, errors.New("no releases published yet")
		}
		return nil, err
	}
	defer resp.Body.Close()
	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func (u *Updater) lock() error {
	if r := u.disabledReason(); r != "" {
		return errors.New(r)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.busy {
		return errors.New("an update operation is already running")
	}
	u.busy = true
	return nil
}

func (u *Updater) unlock() {
	u.mu.Lock()
	u.busy = false
	u.mu.Unlock()
}

// Apply downloads and installs the latest release, then restarts.
func (u *Updater) Apply(ctx context.Context) error {
	if err := u.lock(); err != nil {
		return err
	}
	defer u.unlock()

	rel, err := u.fetchLatest(ctx)
	if err != nil {
		return err
	}
	if !Newer(rel.TagName, u.opts.Current) {
		return fmt.Errorf("already up to date (%s)", u.opts.Current)
	}
	var binURL, sumsURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case AssetName():
			binURL = a.URL
		case "SHA256SUMS":
			sumsURL = a.URL
		}
	}
	if binURL == "" || sumsURL == "" {
		return fmt.Errorf("release %s has no %s or SHA256SUMS asset", rel.TagName, AssetName())
	}
	want, err := u.expectedSum(ctx, sumsURL)
	if err != nil {
		return err
	}

	dir := filepath.Dir(u.exe)
	tmp, err := os.CreateTemp(dir, ".locostor-update-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	resp, err := u.get(ctx, binURL)
	if err != nil {
		tmp.Close()
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), resp.Body)
	resp.Body.Close()
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, want)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if v := u.binaryVersion(ctx, tmpName); v != strings.TrimPrefix(rel.TagName, "v") {
		return fmt.Errorf("downloaded binary reports version %q, expected %s", v, rel.TagName)
	}

	// Keep the running binary for rollback, then move the new one in.
	if err := copyFile(u.exe, u.previousPath()); err != nil {
		return fmt.Errorf("backup current binary: %w", err)
	}
	if err := os.Rename(tmpName, u.exe); err != nil {
		return fmt.Errorf("install new binary: %w", err)
	}
	log.Printf("updated %s -> %s, restarting", u.opts.Current, rel.TagName)
	u.restartSoon()
	return nil
}

// Rollback swaps the current and previous binary, then restarts.
func (u *Updater) Rollback(ctx context.Context) error {
	if err := u.lock(); err != nil {
		return err
	}
	defer u.unlock()
	prev := u.previousPath()
	if _, err := os.Stat(prev); err != nil {
		return errors.New("no previous version available")
	}
	swap := u.exe + ".swap"
	if err := os.Rename(u.exe, swap); err != nil {
		return err
	}
	if err := os.Rename(prev, u.exe); err != nil {
		_ = os.Rename(swap, u.exe)
		return err
	}
	if err := os.Rename(swap, prev); err != nil {
		return err
	}
	log.Printf("rolled back from %s, restarting", u.opts.Current)
	u.restartSoon()
	return nil
}

func (u *Updater) restartSoon() {
	if u.opts.Restart == nil {
		return
	}
	// Give the HTTP response time to reach the browser.
	time.AfterFunc(500*time.Millisecond, u.opts.Restart)
}

func (u *Updater) expectedSum(ctx context.Context, url string) (string, error) {
	resp, err := u.get(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == AssetName() {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("SHA256SUMS has no entry for %s", AssetName())
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
