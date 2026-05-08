package digest

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// tempDir creates (and returns) a temporary directory for test files.
func tempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

func TestNewStore(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "digests.json")
	s := NewStore(path)
	if s == nil {
		t.Fatal("NewStore returned nil")
	}
	if s.path != path {
		t.Errorf("expected path %q, got %q", path, s.path)
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "nonexistent.json"))

	digests, err := s.Load()
	if err != nil {
		t.Fatalf("Load on nonexistent file should succeed: %v", err)
	}
	if len(digests) != 0 {
		t.Errorf("expected empty slice, got %d items", len(digests))
	}
}

func TestAddAndGet(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	cfg := DigestConfig{
		Schedule:  "0 8 * * *",
		Scope:     ScopeToday,
		Direction: DirectionAgenda,
	}

	added, err := s.Add(cfg)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if added.ID == "" {
		t.Error("expected non-empty ID after Add")
	}
	if !added.Enabled {
		t.Error("expected Enabled=true by default")
	}
	if added.CreatedAt == "" {
		t.Error("expected CreatedAt to be set")
	}
	if added.UpdatedAt == "" {
		t.Error("expected UpdatedAt to be set")
	}

	got, err := s.Get(added.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.ID != added.ID {
		t.Errorf("expected ID %q, got %q", added.ID, got.ID)
	}
	if got.Schedule != "0 8 * * *" {
		t.Errorf("expected schedule %q, got %q", "0 8 * * *", got.Schedule)
	}
	if got.Scope != ScopeToday {
		t.Errorf("expected scope %q, got %q", ScopeToday, got.Scope)
	}
	if got.Direction != DirectionAgenda {
		t.Errorf("expected direction %q, got %q", DirectionAgenda, got.Direction)
	}
}

func TestAdd_GeneratesUniqueIDs(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	cfg := DigestConfig{Schedule: "0 8 * * *", Scope: ScopeToday}

	ids := make(map[string]bool)
	for i := 0; i < 50; i++ {
		added, err := s.Add(cfg)
		if err != nil {
			t.Fatalf("Add #%d failed: %v", i, err)
		}
		if ids[added.ID] {
			t.Errorf("duplicate ID generated: %s", added.ID)
		}
		ids[added.ID] = true
	}
}

func TestAdd_RejectsProvidedID(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	cfg := DigestConfig{ID: "d_manual_123", Schedule: "0 8 * * *", Scope: ScopeToday}

	_, err := s.Add(cfg)
	if err == nil {
		t.Fatal("expected error when providing a non-empty ID")
	}
}

func TestAdd_InvalidSchedule(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	// Empty schedule
	_, err := s.Add(DigestConfig{Schedule: "", Scope: ScopeToday})
	if err == nil {
		t.Error("expected error for empty schedule")
	}

	// Too short
	_, err = s.Add(DigestConfig{Schedule: "ab", Scope: ScopeToday})
	if err == nil {
		t.Error("expected error for too-short schedule")
	}
}

func TestList(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	// Empty list
	digests, err := s.List()
	if err != nil {
		t.Fatalf("List on empty store failed: %v", err)
	}
	if len(digests) != 0 {
		t.Errorf("expected 0 digests, got %d", len(digests))
	}

	// Add two
	_, _ = s.Add(DigestConfig{Schedule: "0 8 * * *", Scope: ScopeToday})
	_, _ = s.Add(DigestConfig{Schedule: "0 18 * * 5", Scope: ScopeWeek})

	digests, err = s.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(digests) != 2 {
		t.Errorf("expected 2 digests, got %d", len(digests))
	}
}

func TestRemove(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	added, _ := s.Add(DigestConfig{Schedule: "0 8 * * *", Scope: ScopeToday})

	err := s.Remove(added.ID)
	if err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	// Verify gone
	_, err = s.Get(added.ID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after Remove, got %v", err)
	}

	// Remove non-existent
	err = s.Remove("d_nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for non-existent ID, got %v", err)
	}
}

func TestEnableDisable(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	added, _ := s.Add(DigestConfig{Schedule: "0 8 * * *", Scope: ScopeToday})
	if !added.Enabled {
		t.Fatal("expected default Enabled=true")
	}

	// Disable
	err := s.Disable(added.ID)
	if err != nil {
		t.Fatalf("Disable failed: %v", err)
	}
	got, _ := s.Get(added.ID)
	if got.Enabled {
		t.Error("expected Enabled=false after Disable")
	}

	// Enable
	err = s.Enable(added.ID)
	if err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	got, _ = s.Get(added.ID)
	if !got.Enabled {
		t.Error("expected Enabled=true after Enable")
	}

	// Non-existent ID
	err = s.Enable("d_nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for Enable on non-existent ID, got %v", err)
	}
	err = s.Disable("d_nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for Disable on non-existent ID, got %v", err)
	}
}

func TestGet_NotFound(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	_, err := s.Get("d_nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	var wg sync.WaitGroup
	const goroutines = 20

	// Concurrent adds
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Add(DigestConfig{
				Schedule:  "0 8 * * *",
				Scope:     ScopeToday,
				Direction: DirectionSummary,
			})
		}()
	}
	wg.Wait()

	// Verify all were persisted
	digests, err := s.List()
	if err != nil {
		t.Fatalf("List after concurrent adds failed: %v", err)
	}
	if len(digests) != goroutines {
		t.Errorf("expected %d digests after concurrent adds, got %d", goroutines, len(digests))
	}

	// Concurrent enables/disables
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if idx%2 == 0 {
				_ = s.Enable(digests[idx].ID)
			} else {
				_ = s.Disable(digests[idx].ID)
			}
		}(i)
	}
	wg.Wait()

	// Verify store is still consistent
	digests2, err := s.List()
	if err != nil {
		t.Fatalf("List after concurrent toggles failed: %v", err)
	}
	if len(digests2) != goroutines {
		t.Errorf("expected %d digests after toggles, got %d", goroutines, len(digests2))
	}
}

func TestFilePersistence(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "digests.json")

	// First store: add and close
	s1 := NewStore(path)
	cfg, err := s1.Add(DigestConfig{Schedule: "0 8 * * *", Scope: ScopeWeek, Direction: DirectionSummary})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Second store: should read from same file
	s2 := NewStore(path)
	digests, err := s2.List()
	if err != nil {
		t.Fatalf("List from second store failed: %v", err)
	}
	if len(digests) != 1 {
		t.Fatalf("expected 1 digest, got %d", len(digests))
	}
	if digests[0].ID != cfg.ID {
		t.Errorf("expected ID %q, got %q", cfg.ID, digests[0].ID)
	}
}

func TestCorruptFile(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "digests.json")

	// Write garbage
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json {{{"), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewStore(path)
	_, err := s.List()
	if err == nil {
		t.Error("expected error when reading corrupt file")
	}
}
