package state

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/terracotta-ai/beecon/internal/logging"
)

const maxBackups = 10

// BackupInfo describes a single state backup file.
type BackupInfo struct {
	Path      string
	Timestamp time.Time
}

// backupDir returns the path to the backups directory.
func (s *Store) backupDir() string {
	return filepath.Join(filepath.Dir(s.path), "backups")
}

// backupIfNeeded copies the current state.json to .beecon/backups/state-{RFC3339}.json
// if state.json exists. Prunes backups beyond maxBackups (default 10).
func (s *Store) backupIfNeeded() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no state file yet, nothing to back up
		}
		return fmt.Errorf("read state for backup: %w", err)
	}

	dir := s.backupDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	// Replace colons with hyphens for filesystem safety
	ts = strings.ReplaceAll(ts, ":", "-")
	backupPath := filepath.Join(dir, fmt.Sprintf("state-%s.json", ts))

	if err := os.WriteFile(backupPath, data, 0o600); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}

	// Prune old backups
	if err := s.pruneBackups(); err != nil {
		logging.Logger.Warn("failed to prune old backups", "error", err)
	}

	return nil
}

// pruneBackups removes the oldest backups so that at most maxBackups remain.
func (s *Store) pruneBackups() error {
	backups, err := s.ListBackups()
	if err != nil {
		return err
	}
	if len(backups) <= maxBackups {
		return nil
	}
	// backups are sorted newest-first, so remove from the end
	for _, b := range backups[maxBackups:] {
		if err := os.Remove(b.Path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove old backup %s: %w", b.Path, err)
		}
	}
	return nil
}

// ListBackups returns backup file paths sorted newest-first.
func (s *Store) ListBackups() ([]BackupInfo, error) {
	dir := s.backupDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read backup dir: %w", err)
	}

	var backups []BackupInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "state-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		ts, err := parseBackupTimestamp(name)
		if err != nil {
			continue // skip unrecognized files
		}
		backups = append(backups, BackupInfo{
			Path:      filepath.Join(dir, name),
			Timestamp: ts,
		})
	}

	// Sort newest-first
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Timestamp.After(backups[j].Timestamp)
	})

	return backups, nil
}

// RestoreBackup copies the specified backup file back to state.json.
// The timestamp parameter should match the filesystem-safe format used in
// backup filenames (e.g., "2026-03-08T16-04-05Z").
func (s *Store) RestoreBackup(timestamp string) error {
	backupPath := filepath.Join(s.backupDir(), fmt.Sprintf("state-%s.json", timestamp))
	data, err := os.ReadFile(backupPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("backup not found for timestamp %q", timestamp)
		}
		return fmt.Errorf("read backup: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write state temp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename state: %w", err)
	}
	return nil
}

// parseBackupTimestamp extracts the timestamp from a backup filename like
// "state-2026-03-08T16-04-05Z.json".
func parseBackupTimestamp(filename string) (time.Time, error) {
	// Strip "state-" prefix and ".json" suffix
	name := strings.TrimPrefix(filename, "state-")
	name = strings.TrimSuffix(name, ".json")
	// Restore colons from hyphens in time portion (after the T)
	tIdx := strings.Index(name, "T")
	if tIdx < 0 {
		return time.Time{}, fmt.Errorf("no T separator in %q", filename)
	}
	datePart := name[:tIdx]
	timePart := name[tIdx:]
	// The time part has hyphens instead of colons: T16-04-05Z -> T16:04:05Z
	// We need to replace only the hyphens that represent colons (positions after T)
	// Format: T16-04-05Z — replace first two hyphens with colons
	timePart = replaceFirstN(timePart, "-", ":", 2)
	restored := datePart + timePart
	return time.Parse(time.RFC3339, restored)
}

// replaceFirstN replaces the first n occurrences of old with new in s.
func replaceFirstN(s, old, new string, n int) string {
	result := s
	for i := 0; i < n; i++ {
		idx := strings.Index(result, old)
		if idx < 0 {
			break
		}
		result = result[:idx] + new + result[idx+len(old):]
	}
	return result
}
