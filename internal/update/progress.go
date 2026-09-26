package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// maxBinarySize caps the download so a bad release cannot fill the disk.
const maxBinarySize = 200 << 20

// LogLine is one step of an update or rollback.
type LogLine struct {
	Time time.Time `json:"time"`
	Msg  string    `json:"msg"`
}

// Progress describes the current or last update/rollback run so the UI can
// follow it live.
type Progress struct {
	Action string    `json:"action"` // update, rollback
	Phase  string    `json:"phase"`  // idle, preparing, downloading, verifying, installing, restarting, failed
	From   string    `json:"from"`
	Target string    `json:"target"`
	Bytes  int64     `json:"bytes"`
	Total  int64     `json:"total"`
	Error  string    `json:"error,omitempty"`
	Log    []LogLine `json:"log"`
}

// Progress returns a snapshot of the running or last operation.
func (u *Updater) Progress() Progress {
	u.mu.Lock()
	defer u.mu.Unlock()
	p := u.progress
	if p.Phase == "" {
		p.Phase = "idle"
	}
	p.Log = append([]LogLine{}, p.Log...)
	if p.Phase == "downloading" {
		p.Bytes = u.bytes.Load()
	}
	return p
}

func (u *Updater) step(phase, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	u.mu.Lock()
	if phase != "" {
		u.progress.Phase = phase
	}
	u.progress.Log = append(u.progress.Log, LogLine{Time: time.Now(), Msg: msg})
	action := u.progress.Action
	u.mu.Unlock()
	log.Printf("%s: %s", action, msg)
}

func (u *Updater) fail(err error) {
	u.mu.Lock()
	u.progress.Phase = "failed"
	u.progress.Error = err.Error()
	u.progress.Log = append(u.progress.Log, LogLine{Time: time.Now(), Msg: "Failed: " + err.Error()})
	u.busy = false
	action := u.progress.Action
	u.mu.Unlock()
	log.Printf("%s failed: %v", action, err)
}

// begin marks an operation as running. Only one runs at a time.
func (u *Updater) begin(action string) error {
	if r := u.disabledReason(); r != "" {
		return errors.New(r)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.busy {
		return errors.New("an update operation is already running")
	}
	u.busy = true
	u.bytes.Store(0)
	u.progress = Progress{Action: action, Phase: "preparing", From: u.opts.Current, Log: []LogLine{}}
	return nil
}

// StartUpdate starts downloading and installing the latest release in the
// background. Follow it with Progress.
func (u *Updater) StartUpdate() error {
	if err := u.begin("update"); err != nil {
		return err
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		if err := u.apply(ctx); err != nil {
			u.fail(err)
		}
	}()
	return nil
}

// StartRollback swaps back to the previous binary in the background.
func (u *Updater) StartRollback() error {
	if err := u.begin("rollback"); err != nil {
		return err
	}
	go func() {
		if err := u.rollback(); err != nil {
			u.fail(err)
		}
	}()
	return nil
}

// countingWriter counts downloaded bytes for the progress bar.
type countingWriter struct{ n *atomic.Int64 }

func (c countingWriter) Write(p []byte) (int, error) {
	c.n.Add(int64(len(p)))
	return len(p), nil
}

func (u *Updater) apply(ctx context.Context) error {
	u.step("", "Checking the latest release of %s", u.opts.Repo)
	rel, err := u.fetchLatest(ctx)
	if err != nil {
		return err
	}
	target := strings.TrimPrefix(rel.TagName, "v")
	u.mu.Lock()
	u.progress.Target = target
	u.mu.Unlock()
	if !Newer(rel.TagName, u.opts.Current) {
		return fmt.Errorf("already up to date (%s)", u.opts.Current)
	}
	u.step("", "Found version %s", target)

	var binURL, sumsURL string
	var size int64
	for _, a := range rel.Assets {
		switch a.Name {
		case AssetName():
			binURL, size = a.URL, a.Size
		case "SHA256SUMS":
			sumsURL = a.URL
		}
	}
	if binURL == "" || sumsURL == "" {
		return fmt.Errorf("release %s has no %s or SHA256SUMS asset", rel.TagName, AssetName())
	}
	if size > maxBinarySize {
		return fmt.Errorf("release binary is unexpectedly large (%d bytes)", size)
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

	u.mu.Lock()
	u.progress.Total = size
	u.mu.Unlock()
	u.step("downloading", "Downloading %s (%.1f MB)", AssetName(), float64(size)/(1<<20))
	resp, err := u.get(ctx, binURL)
	if err != nil {
		tmp.Close()
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h, countingWriter{&u.bytes}), io.LimitReader(resp.Body, maxBinarySize+1))
	resp.Body.Close()
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if n > maxBinarySize {
		return errors.New("download exceeds the size limit")
	}
	u.mu.Lock()
	u.progress.Bytes = n
	u.mu.Unlock()

	u.step("verifying", "Verifying SHA-256 checksum")
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, want)
	}
	u.step("", "Checksum OK (%s…)", got[:16])
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if v := u.binaryVersion(ctx, tmpName); v != target {
		return fmt.Errorf("downloaded binary reports version %q, expected %s", v, target)
	}
	u.step("", "New binary runs and reports version %s", target)

	u.step("installing", "Keeping version %s for rollback", u.opts.Current)
	if err := copyFile(u.exe, u.previousPath()); err != nil {
		return fmt.Errorf("backup current binary: %w", err)
	}
	if err := os.Rename(tmpName, u.exe); err != nil {
		return fmt.Errorf("install new binary: %w", err)
	}
	u.step("restarting", "Installed. Restarting LocoStor…")
	u.restartSoon()
	return nil
}

func (u *Updater) rollback() error {
	prev := u.previousPath()
	if _, err := os.Stat(prev); err != nil {
		return errors.New("no previous version available")
	}
	target := u.previousVersion(context.Background())
	u.mu.Lock()
	u.progress.Target = target
	u.mu.Unlock()
	u.step("installing", "Switching from %s back to %s", u.opts.Current, target)
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
	u.step("restarting", "Restarting LocoStor…")
	u.restartSoon()
	return nil
}

func (u *Updater) restartSoon() {
	if u.opts.Restart == nil {
		return
	}
	// Give the browser a moment to fetch the final progress.
	time.AfterFunc(1500*time.Millisecond, u.opts.Restart)
}
