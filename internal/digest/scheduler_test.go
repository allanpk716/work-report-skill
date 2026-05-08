package digest

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"wr/internal/logger"
)

// --- helpers ---

func init() {
	// Initialize logger so structured logging doesn't panic in tests.
	_ = logger.Init(filepath.Join(os.TempDir(), "wr-test-logs"))
}

// tempStore creates a DigestStore in a temp directory.
func tempStore(t *testing.T) (*DigestStore, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "digests.json")
	return NewStore(path), path
}

// TestDigestScheduler_NewScheduler tests basic construction.
func TestDigestScheduler_NewScheduler(t *testing.T) {
	store, _ := tempStore(t)
	s := NewDigestScheduler(store, nil)

	if s == nil {
		t.Fatal("NewDigestScheduler returned nil")
	}
	if s.RegisteredEntries() != 0 {
		t.Fatalf("expected 0 entries, got %d", s.RegisteredEntries())
	}
}

// TestDigestScheduler_StartStop tests lifecycle.
func TestDigestScheduler_StartStop(t *testing.T) {
	store, _ := tempStore(t)
	s := NewDigestScheduler(store, nil)

	s.Start()
	// Allow goroutines to settle.
	time.Sleep(50 * time.Millisecond)

	s.Stop()
	// After stop, registered entries should be 0.
	if s.RegisteredEntries() != 0 {
		t.Fatalf("expected 0 entries after stop, got %d", s.RegisteredEntries())
	}
}

// TestDigestScheduler_Register tests registering a valid digest.
func TestDigestScheduler_Register(t *testing.T) {
	store, _ := tempStore(t)
	var triggered []DigestConfig
	s := NewDigestScheduler(store, func(cfg DigestConfig) {
		triggered = append(triggered, cfg)
	})
	s.Start()
	defer s.Stop()

	cfg := DigestConfig{
		ID:        "test-1",
		Schedule:  "* * * * * *", // every second
		Scope:     ScopeToday,
		Direction: DirectionAgenda,
		Enabled:   true,
	}

	err := s.Register(cfg)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if s.RegisteredEntries() != 1 {
		t.Fatalf("expected 1 entry, got %d", s.RegisteredEntries())
	}

	// Wait for at least one trigger (up to 3 seconds).
	deadline := time.Now().Add(3 * time.Second)
	for len(triggered) == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if len(triggered) == 0 {
		t.Fatal("expected at least one trigger within 3s")
	}
	if triggered[0].ID != "test-1" {
		t.Fatalf("expected triggered ID 'test-1', got %q", triggered[0].ID)
	}
}

// TestDigestScheduler_RegisterInvalidCron tests that invalid cron expressions are rejected.
func TestDigestScheduler_RegisterInvalidCron(t *testing.T) {
	store, _ := tempStore(t)
	s := NewDigestScheduler(store, nil)

	tests := []struct {
		name string
		sched string
	}{
		{"too few fields", "0 8 * *"},
		{"bad syntax", "abc def ghi jkl mno pqr"},
		{"empty schedule", ""},
		{"only stars", "* * * *"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DigestConfig{
				ID:        "invalid-" + tt.name,
				Schedule:  tt.sched,
				Scope:     ScopeToday,
				Direction: DirectionAgenda,
				Enabled:   true,
			}
			err := s.Register(cfg)
			if err == nil {
				t.Errorf("expected error for %q, got nil", tt.name)
			}
			if s.RegisteredEntries() != 0 {
				t.Fatalf("expected 0 entries after invalid register, got %d", s.RegisteredEntries())
			}
		})
	}
}

// TestDigestScheduler_RegisterDuplicate tests that registering the same ID twice is a no-op.
func TestDigestScheduler_RegisterDuplicate(t *testing.T) {
	store, _ := tempStore(t)
	s := NewDigestScheduler(store, nil)

	cfg := DigestConfig{
		ID:        "dup-1",
		Schedule:  "0 0 8 * * *",
		Scope:     ScopeToday,
		Direction: DirectionAgenda,
		Enabled:   true,
	}

	if err := s.Register(cfg); err != nil {
		t.Fatalf("first Register failed: %v", err)
	}
	if err := s.Register(cfg); err != nil {
		t.Fatalf("second Register should be no-op, got error: %v", err)
	}
	if s.RegisteredEntries() != 1 {
		t.Fatalf("expected 1 entry after duplicate register, got %d", s.RegisteredEntries())
	}
}

// TestDigestScheduler_Unregister tests removing a registered entry.
func TestDigestScheduler_Unregister(t *testing.T) {
	store, _ := tempStore(t)
	s := NewDigestScheduler(store, nil)

	cfg := DigestConfig{
		ID:        "unreg-1",
		Schedule:  "0 0 8 * * *",
		Scope:     ScopeToday,
		Direction: DirectionAgenda,
		Enabled:   true,
	}

	s.Register(cfg)
	if s.RegisteredEntries() != 1 {
		t.Fatalf("expected 1 entry, got %d", s.RegisteredEntries())
	}

	s.Unregister("unreg-1")
	if s.RegisteredEntries() != 0 {
		t.Fatalf("expected 0 entries after unregister, got %d", s.RegisteredEntries())
	}

	// Unregister non-existent ID should be no-op.
	s.Unregister("non-existent")
	if s.RegisteredEntries() != 0 {
		t.Fatalf("expected 0 entries after non-existent unregister, got %d", s.RegisteredEntries())
	}
}

// TestDigestScheduler_Sync tests loading configs from store.
func TestDigestScheduler_Sync(t *testing.T) {
	store, _ := tempStore(t)
	s := NewDigestScheduler(store, nil)

	// Add configs to the store.
	cfg1 := DigestConfig{
		Schedule:  "0 0 8 * * *",
		Scope:     ScopeToday,
		Direction: DirectionAgenda,
		Enabled:   true,
	}
	added1, err := store.Add(cfg1)
	if err != nil {
		t.Fatalf("store.Add failed: %v", err)
	}

	cfg2 := DigestConfig{
		Schedule:  "0 30 9 * * *",
		Scope:     ScopeWeek,
		Direction: DirectionSummary,
		Enabled:   true,
	}
	added2, err := store.Add(cfg2)
	if err != nil {
		t.Fatalf("store.Add failed: %v", err)
	}

	// Add a disabled one (should not be registered after sync).
	cfg3 := DigestConfig{
		Schedule:  "0 0 10 * * *",
		Scope:     ScopeMonth,
		Direction: DirectionSummary,
		Enabled:   false,
	}
	added3, err := store.Add(cfg3)
	if err != nil {
		t.Fatalf("store.Add failed: %v", err)
	}
	// store.Add forces Enabled=true; disable it explicitly.
	if err := store.Disable(added3.ID); err != nil {
		t.Fatalf("store.Disable failed: %v", err)
	}

	// Add one with invalid cron (should be skipped by Sync).
	cfg4 := DigestConfig{
		Schedule:  "invalid-cron",
		Scope:     ScopeToday,
		Direction: DirectionAgenda,
		Enabled:   true,
	}
	_, err = store.Add(cfg4)
	if err != nil {
		t.Fatalf("store.Add failed: %v", err)
	}

	s.Start()
	defer s.Stop()

	s.Sync()

	// Should have 2 entries (cfg1 and cfg2; cfg3 disabled, cfg4 invalid cron).
	if s.RegisteredEntries() != 2 {
		t.Fatalf("expected 2 entries after sync, got %d", s.RegisteredEntries())
	}

	// Unregister one and verify sync removes it.
	s.Unregister(added1.ID)
	store.Remove(added1.ID)
	s.Sync()
	if s.RegisteredEntries() != 1 {
		t.Fatalf("expected 1 entry after sync with removal, got %d", s.RegisteredEntries())
	}

	// Disable the remaining one and verify sync removes it.
	store.Disable(added2.ID)
	s.Sync()
	if s.RegisteredEntries() != 0 {
		t.Fatalf("expected 0 entries after sync with disable, got %d", s.RegisteredEntries())
	}
}

// TestDigestScheduler_SyncStoreError tests that actual store errors are handled gracefully.
// Note: a missing store file is NOT an error (returns empty list), so Sync will
// clear existing entries. Only actual read/parse failures skip sync.
func TestDigestScheduler_SyncStoreError(t *testing.T) {
	// Create a store with corrupted JSON (actual parse error, not missing file).
	dir := t.TempDir()
	path := filepath.Join(dir, "digests.json")
	if err := os.WriteFile(path, []byte("{corrupted"), 0644); err != nil {
		t.Fatalf("failed to write corrupted file: %v", err)
	}
	store := NewStore(path)
	s := NewDigestScheduler(store, nil)

	s.Start()
	defer s.Stop()

	// Register one entry first.
	cfg := DigestConfig{
		ID:        "pre-existing",
		Schedule:  "0 0 8 * * *",
		Scope:     ScopeToday,
		Direction: DirectionAgenda,
		Enabled:   true,
	}
	_ = s.Register(cfg)

	// Sync should fail to load (corrupted JSON), keep existing entries.
	s.Sync()
	if s.RegisteredEntries() != 1 {
		t.Fatalf("expected 1 entry (kept from before error), got %d", s.RegisteredEntries())
	}
}

// TestDigestScheduler_SyncZeroEnabled tests sync with zero enabled digests.
func TestDigestScheduler_SyncZeroEnabled(t *testing.T) {
	store, _ := tempStore(t)
	s := NewDigestScheduler(store, nil)

	s.Start()
	defer s.Stop()

	s.Sync()
	if s.RegisteredEntries() != 0 {
		t.Fatalf("expected 0 entries with empty store, got %d", s.RegisteredEntries())
	}
}

// TestDigestScheduler_SyncAllDisabled tests sync where all digests are disabled.
func TestDigestScheduler_SyncAllDisabled(t *testing.T) {
	store, _ := tempStore(t)
	s := NewDigestScheduler(store, nil)

	cfg := DigestConfig{
		Schedule:  "0 0 8 * * *",
		Scope:     ScopeToday,
		Direction: DirectionAgenda,
	}
	added, _ := store.Add(cfg)
	store.Disable(added.ID)

	s.Start()
	defer s.Stop()
	s.Sync()

	if s.RegisteredEntries() != 0 {
		t.Fatalf("expected 0 entries with all disabled, got %d", s.RegisteredEntries())
	}
}

// TestDigestScheduler_SyncInvalidCronInStore tests that invalid cron in store is skipped.
func TestDigestScheduler_SyncInvalidCronInStore(t *testing.T) {
	store, _ := tempStore(t)
	s := NewDigestScheduler(store, nil)

	// Manually write a config with invalid cron to bypass store validation.
	// The store only does basic length checks; the scheduler does full validation.
	cfg := DigestConfig{
		Schedule:  "0 8 * *", // only 4 fields — invalid for 6-field parser
		Scope:     ScopeToday,
		Direction: DirectionAgenda,
		Enabled:   true,
	}
	// We need to write this directly since store.Add validates basic length.
	// The store's validateSchedule allows this (len >= 5), but the scheduler's
	// cron parser rejects it. Let's use a longer but still invalid expression.
	cfg.Schedule = "60 60 60 60 60 60" // valid length but invalid values
	_, err := store.Add(cfg)
	if err != nil {
		t.Fatalf("store.Add failed: %v", err)
	}

	s.Start()
	defer s.Stop()
	s.Sync()

	// Invalid cron should be skipped.
	if s.RegisteredEntries() != 0 {
		t.Fatalf("expected 0 entries (invalid cron skipped), got %d", s.RegisteredEntries())
	}
}

// TestDigestScheduler_ConcurrentSync tests that concurrent Sync calls don't panic or deadlock.
func TestDigestScheduler_ConcurrentSync(t *testing.T) {
	store, _ := tempStore(t)
	s := NewDigestScheduler(store, nil)

	// Pre-populate the store.
	for i := 0; i < 10; i++ {
		_, err := store.Add(DigestConfig{
			Schedule:  "0 0 8 * * *",
			Scope:     ScopeToday,
			Direction: DirectionAgenda,
		})
		if err != nil {
			t.Fatalf("store.Add failed: %v", err)
		}
	}

	s.Start()
	defer s.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Sync()
		}()
	}
	wg.Wait()

	// Should have 10 entries (all enabled, valid cron).
	if s.RegisteredEntries() != 10 {
		t.Fatalf("expected 10 entries after concurrent sync, got %d", s.RegisteredEntries())
	}
}

// TestDigestScheduler_CronValidation verifies cron expressions used in tests.
func TestDigestScheduler_CronValidation(t *testing.T) {
	tests := []struct {
		sched string
		valid bool
	}{
		{"0 0 8 * * *", true},       // every day at 08:00:00
		{"* * * * * *", true},        // every second
		{"0 30 9 * * 1", true},       // every Monday at 09:30:00
		{"0 0 8 * *", false},         // 5 fields — invalid for seconds parser
		{"", false},                  // empty
		{"abc", false},               // non-cron text
		{"0 0 25 * * *", false},      // invalid hour
		{"60 0 0 * * *", false},      // invalid second
	}

	for _, tt := range tests {
		t.Run(tt.sched, func(t *testing.T) {
			_, err := cronParser.Parse(tt.sched)
			if tt.valid && err != nil {
				t.Errorf("expected valid for %q, got error: %v", tt.sched, err)
			}
			if !tt.valid && err == nil {
				t.Errorf("expected error for %q, got nil", tt.sched)
			}
		})
	}
}

// TestDigestScheduler_CallbackReceivesCorrectConfig verifies that the callback
// receives the DigestConfig with correct scope and direction fields.
func TestDigestScheduler_CallbackReceivesCorrectConfig(t *testing.T) {
	store, _ := tempStore(t)

	var mu sync.Mutex
	var received []DigestConfig
	s := NewDigestScheduler(store, func(cfg DigestConfig) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, cfg)
	})

	s.Start()
	defer s.Stop()

	cfg := DigestConfig{
		ID:        "callback-test",
		Schedule:  "* * * * * *",
		Scope:     ScopeWeek,
		Direction: DirectionSummary,
		Enabled:   true,
	}
	_ = s.Register(cfg)

	// Wait for trigger.
	deadline := time.Now().Add(3 * time.Second)
	mu.Lock()
	for len(received) == 0 && time.Now().Before(deadline) {
		mu.Unlock()
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
	}
	mu.Unlock()

	if len(received) == 0 {
		t.Fatal("expected at least one trigger")
	}
	if received[0].Scope != ScopeWeek {
		t.Errorf("expected scope %q, got %q", ScopeWeek, received[0].Scope)
	}
	if received[0].Direction != DirectionSummary {
		t.Errorf("expected direction %q, got %q", DirectionSummary, received[0].Direction)
	}
	if received[0].ID != "callback-test" {
		t.Errorf("expected ID 'callback-test', got %q", received[0].ID)
	}
}

// TestDigestScheduler_StoreFileCorrupted tests behavior when the store file
// contains corrupted JSON.
func TestDigestScheduler_StoreFileCorrupted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "digests.json")
	store := NewStore(path)

	// Write corrupted JSON.
	if err := os.WriteFile(path, []byte("{not valid json"), 0644); err != nil {
		t.Fatalf("failed to write corrupted file: %v", err)
	}

	s := NewDigestScheduler(store, nil)
	s.Start()
	defer s.Stop()

	// Sync should handle the error gracefully (log + keep existing).
	s.Sync()
	if s.RegisteredEntries() != 0 {
		t.Fatalf("expected 0 entries with corrupted store, got %d", s.RegisteredEntries())
	}
}

// TestDigestScheduler_StoreFileMissing tests that a missing store file is handled
// (returns empty config list, not an error).
func TestDigestScheduler_StoreFileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")
	store := NewStore(path)

	s := NewDigestScheduler(store, nil)
	s.Start()
	defer s.Stop()

	// Sync with missing file should succeed with 0 entries.
	s.Sync()
	if s.RegisteredEntries() != 0 {
		t.Fatalf("expected 0 entries with missing store, got %d", s.RegisteredEntries())
	}
}

// TestDigestScheduler_WithSeconds verifies that the scheduler uses 6-field
// (with seconds) cron format.
func TestDigestScheduler_WithSeconds(t *testing.T) {
	store, _ := tempStore(t)
	s := NewDigestScheduler(store, nil)

	// 5-field (standard cron without seconds) should be rejected.
	cfg := DigestConfig{
		ID:        "5field",
		Schedule:  "0 8 * * *", // 5 fields — should fail
		Scope:     ScopeToday,
		Direction: DirectionAgenda,
	}
	err := s.Register(cfg)
	if err == nil {
		t.Error("expected error for 5-field cron expression")
	}

	// 6-field (with seconds) should succeed.
	cfg.ID = "6field"
	cfg.Schedule = "0 0 8 * * *"
	err = s.Register(cfg)
	if err != nil {
		t.Errorf("expected success for 6-field cron, got error: %v", err)
	}
}

// Verify that the cron instance in DigestScheduler uses WithSeconds option
// by checking that it was created with the correct option.
func TestDigestScheduler_CronUsesSeconds(t *testing.T) {
	store, _ := tempStore(t)
	s := NewDigestScheduler(store, nil)

	// The scheduler's cron instance should be created with cron.New(cron.WithSeconds()).
	// We can't directly inspect the option, but we can verify that a valid
	// 6-field expression works via the scheduler's cron.
	_, err := s.cron.AddFunc("0 0 8 * * *", func() {})
	if err != nil {
		t.Errorf("expected 6-field cron to work on scheduler's cron instance, got: %v", err)
	}

	// 5-field should fail on the scheduler's cron instance.
	_, err = s.cron.AddFunc("0 8 * * *", func() {})
	if err == nil {
		t.Error("expected 5-field cron to fail on scheduler's cron instance")
	}
}
