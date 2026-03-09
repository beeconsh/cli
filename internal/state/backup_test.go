package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupCreatedOnLoadForUpdate(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	// Create initial state so there's something to back up
	st := newState()
	st.Resources["svc.api"] = &ResourceRecord{ResourceID: "svc.api", Managed: true, Status: StatusMatched}
	if err := s.Save(st); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	tx, err := s.LoadForUpdate()
	if err != nil {
		t.Fatalf("LoadForUpdate failed: %v", err)
	}
	defer tx.Rollback()

	backups, err := s.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected 1 backup, got %d", len(backups))
	}

	// Verify backup content matches original state
	data, err := os.ReadFile(backups[0].Path)
	if err != nil {
		t.Fatalf("read backup failed: %v", err)
	}
	var restored State
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal backup failed: %v", err)
	}
	if restored.Resources["svc.api"] == nil {
		t.Fatal("backup should contain the original resource")
	}
}

func TestBackupSkippedOnFirstApply(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	// No state.json exists yet
	tx, err := s.LoadForUpdate()
	if err != nil {
		t.Fatalf("LoadForUpdate failed: %v", err)
	}
	defer tx.Rollback()

	backups, err := s.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(backups) != 0 {
		t.Fatalf("expected 0 backups on first apply, got %d", len(backups))
	}
}

func TestBackupPrunesOldFiles(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	backupDir := s.backupDir()
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	// Create 15 backup files with distinct timestamps
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 15; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		formatted := ts.Format(time.RFC3339)
		formatted = replaceColons(formatted)
		name := "state-" + formatted + ".json"
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte(`{}`), 0o600); err != nil {
			t.Fatalf("write backup %d failed: %v", i, err)
		}
	}

	// Create state.json so backup triggers
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.path, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// LoadForUpdate creates one more backup (16 total) then prunes to 10
	tx, err := s.LoadForUpdate()
	if err != nil {
		t.Fatalf("LoadForUpdate failed: %v", err)
	}
	defer tx.Rollback()

	backups, err := s.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(backups) != maxBackups {
		t.Fatalf("expected %d backups after pruning, got %d", maxBackups, len(backups))
	}
}

func TestListBackups(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	backupDir := s.backupDir()
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// Create 3 backups with known timestamps
	timestamps := []time.Time{
		time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 8, 11, 0, 0, 0, time.UTC),
	}
	for _, ts := range timestamps {
		formatted := replaceColons(ts.Format(time.RFC3339))
		name := "state-" + formatted + ".json"
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	backups, err := s.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(backups) != 3 {
		t.Fatalf("expected 3 backups, got %d", len(backups))
	}

	// Should be sorted newest-first
	if !backups[0].Timestamp.Equal(timestamps[1]) { // 12:00
		t.Errorf("first backup should be newest, got %v", backups[0].Timestamp)
	}
	if !backups[1].Timestamp.Equal(timestamps[2]) { // 11:00
		t.Errorf("second backup should be middle, got %v", backups[1].Timestamp)
	}
	if !backups[2].Timestamp.Equal(timestamps[0]) { // 10:00
		t.Errorf("third backup should be oldest, got %v", backups[2].Timestamp)
	}
}

func TestRestoreBackup(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	// Save initial state
	original := newState()
	original.Resources["svc.api"] = &ResourceRecord{ResourceID: "svc.api", Managed: true, Status: StatusMatched}
	if err := s.Save(original); err != nil {
		t.Fatal(err)
	}

	// LoadForUpdate creates a backup
	tx, err := s.LoadForUpdate()
	if err != nil {
		t.Fatal(err)
	}

	// Modify state and commit
	tx.State.Resources["svc.api"].Status = StatusDrifted
	tx.State.Resources["svc.new"] = &ResourceRecord{ResourceID: "svc.new", Managed: true}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Verify the state was modified
	modified, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if modified.Resources["svc.api"].Status != StatusDrifted {
		t.Fatal("state should reflect modification")
	}
	if modified.Resources["svc.new"] == nil {
		t.Fatal("new resource should exist after modification")
	}

	// Get the backup timestamp
	backups, err := s.ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected 1 backup, got %d", len(backups))
	}

	// Extract timestamp from filename
	tsStr := extractTimestamp(backups[0].Path)

	// Restore the backup
	if err := s.RestoreBackup(tsStr); err != nil {
		t.Fatalf("RestoreBackup failed: %v", err)
	}

	// Verify the state was restored
	restored, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if restored.Resources["svc.api"] == nil {
		t.Fatal("original resource should be present after restore")
	}
	if restored.Resources["svc.api"].Status != StatusMatched {
		t.Errorf("status should be restored to MATCHED, got %s", restored.Resources["svc.api"].Status)
	}
	if restored.Resources["svc.new"] != nil {
		t.Error("new resource should not exist after restore")
	}
}

func TestRestoreBackupInvalidTimestamp(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	err := s.RestoreBackup("not-a-real-timestamp")
	if err == nil {
		t.Fatal("expected error for invalid timestamp")
	}
}

// replaceColons replaces colons with hyphens for filesystem-safe timestamps.
func replaceColons(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			result[i] = '-'
		} else {
			result[i] = s[i]
		}
	}
	return string(result)
}

// extractTimestamp extracts the timestamp portion from a backup path like
// ".../state-2026-03-08T16-04-05Z.json" -> "2026-03-08T16-04-05Z"
func extractTimestamp(path string) string {
	base := filepath.Base(path)
	// Strip "state-" prefix and ".json" suffix
	ts := base[len("state-") : len(base)-len(".json")]
	return ts
}
