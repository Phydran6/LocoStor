// Package fsutil contains small file helpers.
package fsutil

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// WriteFileAtomic writes data to a temp file next to path and renames it
// into place, so readers never see a partially written file.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadJSON decodes path into v. A missing file leaves v untouched and
// returns nil.
func ReadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// WriteJSON atomically writes v as indented JSON.
func WriteJSON(path string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, append(data, '\n'), perm)
}

// Backup writes a timestamped copy of data next to path
// (path.locostor-YYYYMMDD-HHMMSS) and keeps only the newest keep backups.
func Backup(path string, data []byte, perm os.FileMode, keep int) error {
	name := path + ".locostor-" + time.Now().Format("20060102-150405")
	if err := WriteFileAtomic(name, data, perm); err != nil {
		return err
	}
	old, _ := filepath.Glob(path + ".locostor-*")
	sort.Strings(old) // timestamps sort chronologically
	for len(old) > keep {
		os.Remove(old[0])
		old = old[1:]
	}
	return nil
}
