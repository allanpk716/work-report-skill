package digest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

	// Only empty schedule should be rejected (cron validation removed with scheduler)
	_, err := s.Add(DigestConfig{Schedule: "", Scope: ScopeToday})
	if err == nil {
		t.Error("expected error for empty schedule, got nil")
	}

	// Non-empty schedules should succeed (cron validation deferred to scheduler)
	validCases := []struct {
		name   string
		sched  string
	}{
		{"daily 8am", "0 8 * * *"},
		{"every 5 min", "*/5 * * * *"},
		{"monthly 1st", "0 0 1 * *"},
		{"monday 9:30", "30 9 * * 1"},
		{"weekday 8am", "0 8 * * 1-5"},
		{"nonsense text", "abc def ghi jkl mno"},
		{"4 fields", "0 8 * *"},
	}

	for _, tc := range validCases {
		t.Run("valid_"+tc.name, func(t *testing.T) {
			_, err := s.Add(DigestConfig{Schedule: tc.sched, Scope: ScopeToday})
			if err != nil {
				t.Errorf("expected success for schedule %q (%s), got error: %v", tc.sched, tc.name, err)
			}
		})
	}
}

// TestValidateSchedule_ValidCronExpressions tests that validateSchedule accepts
// a comprehensive set of valid 5-field cron expressions.
func TestValidateSchedule_ValidCronExpressions(t *testing.T) {
	validCases := []struct {
		name string
		sched string
	}{
		{"every minute", "* * * * *"},
		{"daily at 8am", "0 8 * * *"},
		{"daily at midnight", "0 0 * * *"},
		{"every 5 minutes", "*/5 * * * *"},
		{"every 15 minutes", "*/15 * * * *"},
		{"monthly 1st at midnight", "0 0 1 * *"},
		{"monthly last day", "0 0 28-31 * *"},
		{"monday at 9:30", "30 9 * * 1"},
		{"friday at 17:00", "0 17 * * 5"},
		{"weekday 8am", "0 8 * * 1-5"},
		{"weekend noon", "0 12 * * 0,6"},
		{"range hours", "0 9-17 * * *"},
		{"list months", "0 0 1 1,6,12 *"},
		{"specific day", "30 14 15 * *"},
		{"step day of month", "0 0 */2 * *"},
		{"comma doy", "0 8 * * 1,3,5"},
		{"range+dow", "0 8 1-15 * 1-5"},
	}

	for _, tc := range validCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSchedule(tc.sched)
			if err != nil {
				t.Errorf("validateSchedule(%q) returned unexpected error: %v", tc.sched, err)
			}
		})
	}
}

// TestValidateSchedule_InvalidCronExpressions tests that validateSchedule rejects
// only empty schedule strings. Cron-based validation was removed with the
// scheduler — S03 will add proper scheduling with a new implementation.
func TestValidateSchedule_InvalidCronExpressions(t *testing.T) {
	// Only empty schedule should fail
	err := validateSchedule("")
	if err == nil {
		t.Error("validateSchedule(\"\") expected error, got nil")
	}

	// Non-empty strings pass (even malformed cron — validation deferred)
	nonEmptyCases := []string{
		"a",
		"ab",
		"0 8 *",
		"0 8 * *",
		"0 0 8 * * *",
		"abc def ghi jkl mno",
		"60 0 * * *",
		"     ",
	}
	for _, s := range nonEmptyCases {
		if err := validateSchedule(s); err != nil {
			t.Errorf("validateSchedule(%q) expected nil (non-empty), got error: %v", s, err)
		}
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

// --- Prompt tests ---

func TestPrompt_GetDefault(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	// GetPrompt should return the built-in default when no override exists.
	text, err := s.GetPrompt(PromptNameAgenda)
	if err != nil {
		t.Fatalf("GetPrompt(agenda) failed: %v", err)
	}
	if text != DefaultPromptAgenda {
		t.Errorf("expected default agenda prompt, got %q", text)
	}

	text, err = s.GetPrompt(PromptNameReport)
	if err != nil {
		t.Fatalf("GetPrompt(report) failed: %v", err)
	}
	if text != DefaultPromptReport {
		t.Errorf("expected default report prompt, got %q", text)
	}
}

func TestPrompt_SetAndGet(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	custom := "custom agenda prompt"
	err := s.SetPrompt(PromptNameAgenda, custom)
	if err != nil {
		t.Fatalf("SetPrompt failed: %v", err)
	}

	text, err := s.GetPrompt(PromptNameAgenda)
	if err != nil {
		t.Fatalf("GetPrompt after set failed: %v", err)
	}
	if text != custom {
		t.Errorf("expected %q, got %q", custom, text)
	}

	// Report should still be default.
	text, err = s.GetPrompt(PromptNameReport)
	if err != nil {
		t.Fatalf("GetPrompt(report) failed: %v", err)
	}
	if text != DefaultPromptReport {
		t.Errorf("expected default report prompt, got %q", text)
	}
}

func TestPrompt_SetCustomName(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	customName := PromptName("custom_summary")
	customText := "my custom prompt"
	err := s.SetPrompt(customName, customText)
	if err != nil {
		t.Fatalf("SetPrompt(custom) failed: %v", err)
	}

	text, err := s.GetPrompt(customName)
	if err != nil {
		t.Fatalf("GetPrompt(custom) failed: %v", err)
	}
	if text != customText {
		t.Errorf("expected %q, got %q", customText, text)
	}
}

func TestPrompt_GetNotFound(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	_, err := s.GetPrompt(PromptName("nonexistent"))
	if err != ErrPromptNotFound {
		t.Errorf("expected ErrPromptNotFound, got %v", err)
	}
}

func TestPrompt_ResetToDefault(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	// Override, then reset.
	custom := "custom text"
	_ = s.SetPrompt(PromptNameAgenda, custom)

	err := s.ResetPrompt(PromptNameAgenda)
	if err != nil {
		t.Fatalf("ResetPrompt failed: %v", err)
	}

	text, err := s.GetPrompt(PromptNameAgenda)
	if err != nil {
		t.Fatalf("GetPrompt after reset failed: %v", err)
	}
	if text != DefaultPromptAgenda {
		t.Errorf("expected default after reset, got %q", text)
	}
}

func TestPrompt_ResetAlreadyDefault(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	// Resetting a prompt that was never overridden should be a no-op.
	err := s.ResetPrompt(PromptNameAgenda)
	if err != nil {
		t.Fatalf("ResetPrompt on default should be no-op: %v", err)
	}
}

func TestPrompt_ResetNotFound(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	err := s.ResetPrompt(PromptName("nonexistent"))
	if err != ErrPromptNotFound {
		t.Errorf("expected ErrPromptNotFound, got %v", err)
	}
}

func TestPrompt_ResetCustomName(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	customName := PromptName("my_prompt")
	_ = s.SetPrompt(customName, "text")

	// Custom names have no default, so reset should fail.
	err := s.ResetPrompt(customName)
	if err != ErrPromptNotFound {
		t.Errorf("expected ErrPromptNotFound for custom name reset, got %v", err)
	}
}

func TestPrompt_ListDefaults(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	prompts, err := s.ListPrompts()
	if err != nil {
		t.Fatalf("ListPrompts failed: %v", err)
	}

	if len(prompts) != 2 {
		t.Fatalf("expected 2 default prompts, got %d", len(prompts))
	}

	agenda, ok := prompts[PromptNameAgenda]
	if !ok {
		t.Fatal("agenda prompt missing")
	}
	if !agenda.IsDefault {
		t.Error("agenda should be marked as default")
	}
	if agenda.Text != DefaultPromptAgenda {
		t.Errorf("agenda text mismatch")
	}

	report, ok := prompts[PromptNameReport]
	if !ok {
		t.Fatal("report prompt missing")
	}
	if !report.IsDefault {
		t.Error("report should be marked as default")
	}
}

func TestPrompt_ListWithOverrides(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	_ = s.SetPrompt(PromptNameAgenda, "custom agenda")
	_ = s.SetPrompt(PromptName("extra"), "extra prompt")

	prompts, err := s.ListPrompts()
	if err != nil {
		t.Fatalf("ListPrompts failed: %v", err)
	}

	if len(prompts) != 3 {
		t.Fatalf("expected 3 prompts (2 built-in + 1 custom), got %d", len(prompts))
	}

	agenda := prompts[PromptNameAgenda]
	if agenda.IsDefault {
		t.Error("agenda should not be default after override")
	}
	if agenda.Text != "custom agenda" {
		t.Errorf("expected custom agenda text, got %q", agenda.Text)
	}
	if agenda.UpdatedAt == "" {
		t.Error("expected UpdatedAt to be set on override")
	}

	report := prompts[PromptNameReport]
	if !report.IsDefault {
		t.Error("report should still be default")
	}

	extra := prompts[PromptName("extra")]
	if extra.IsDefault {
		t.Error("extra should not be default")
	}
	if extra.Text != "extra prompt" {
		t.Errorf("expected extra prompt text, got %q", extra.Text)
	}
}

func TestPrompt_FileFormatMigration(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "digests.json")

	// Write old-format file (bare []DigestConfig array).
	oldData := []DigestConfig{
		{ID: "d_test", Schedule: "0 8 * * *", Scope: ScopeToday, Direction: DirectionAgenda, Enabled: true},
	}
	raw, _ := json.MarshalIndent(oldData, "", "  ")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}

	s := NewStore(path)

	// Old digests should still be readable.
	digests, err := s.List()
	if err != nil {
		t.Fatalf("List failed after migration: %v", err)
	}
	if len(digests) != 1 || digests[0].ID != "d_test" {
		t.Fatalf("expected 1 digest with ID d_test, got %v", digests)
	}

	// Prompt defaults should work.
	text, err := s.GetPrompt(PromptNameAgenda)
	if err != nil {
		t.Fatalf("GetPrompt failed after migration: %v", err)
	}
	if text != DefaultPromptAgenda {
		t.Errorf("expected default agenda after migration")
	}

	// Setting a prompt should produce new format.
	_ = s.SetPrompt(PromptNameAgenda, "new text")

	// Read back and verify new format.
	data, _ := os.ReadFile(path)
	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("failed to parse new format: %v", err)
	}
	if sf.Digests == nil || len(sf.Digests) != 1 {
		t.Errorf("expected 1 digest in new format")
	}
	if sf.PromptOverrides == nil || sf.PromptOverrides[PromptNameAgenda].Text != "new text" {
		t.Errorf("expected prompt override in new format")
	}
}

func TestPrompt_EmptyOldFormatMigration(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "digests.json")

	// Write empty old-format array.
	if err := os.WriteFile(path, []byte("[]"), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewStore(path)

	digests, err := s.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(digests) != 0 {
		t.Errorf("expected 0 digests, got %d", len(digests))
	}

	prompts, err := s.ListPrompts()
	if err != nil {
		t.Fatalf("ListPrompts failed: %v", err)
	}
	if len(prompts) != 2 {
		t.Errorf("expected 2 default prompts, got %d", len(prompts))
	}
}

func TestPrompt_PersistenceAcrossStores(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "digests.json")

	s1 := NewStore(path)
	_ = s1.SetPrompt(PromptNameAgenda, "persisted prompt")

	// Open a second store instance pointing to the same file.
	s2 := NewStore(path)
	text, err := s2.GetPrompt(PromptNameAgenda)
	if err != nil {
		t.Fatalf("GetPrompt from second store failed: %v", err)
	}
	if text != "persisted prompt" {
		t.Errorf("expected persisted prompt, got %q", text)
	}
}

func TestPrompt_ConcurrentAccess(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	var wg sync.WaitGroup
	const goroutines = 20

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if idx%2 == 0 {
				_ = s.SetPrompt(PromptNameAgenda, "agenda_"+string(rune(idx)))
			} else {
				_ = s.SetPrompt(PromptNameReport, "report_"+string(rune(idx)))
			}
		}(i)
	}
	wg.Wait()

	// Store should be consistent — no corrupt JSON.
	prompts, err := s.ListPrompts()
	if err != nil {
		t.Fatalf("ListPrompts after concurrent access failed: %v", err)
	}
	if len(prompts) != 2 {
		t.Errorf("expected 2 prompts, got %d", len(prompts))
	}
}

func TestPrompt_PreservesDigestsOnWrite(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	// Add a digest.
	cfg, _ := s.Add(DigestConfig{Schedule: "0 8 * * *", Scope: ScopeToday})

	// Set a prompt — should not clobber digests.
	_ = s.SetPrompt(PromptNameAgenda, "custom")

	digests, err := s.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(digests) != 1 || digests[0].ID != cfg.ID {
		t.Errorf("digest lost after prompt set: got %v", digests)
	}

	// Add another digest — should not clobber prompts.
	_, _ = s.Add(DigestConfig{Schedule: "0 18 * * 5", Scope: ScopeWeek})

	text, err := s.GetPrompt(PromptNameAgenda)
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	if text != "custom" {
		t.Errorf("prompt lost after digest add: got %q", text)
	}
}

func TestPrompt_EmptyString(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	// Setting an empty string should be allowed (user's choice).
	err := s.SetPrompt(PromptNameAgenda, "")
	if err != nil {
		t.Fatalf("SetPrompt with empty string failed: %v", err)
	}

	text, err := s.GetPrompt(PromptNameAgenda)
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty string, got %q", text)
	}
}

func TestPrompt_OverwriteOverride(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	_ = s.SetPrompt(PromptNameAgenda, "first")
	_ = s.SetPrompt(PromptNameAgenda, "second")

	text, _ := s.GetPrompt(PromptNameAgenda)
	if text != "second" {
		t.Errorf("expected second override, got %q", text)
	}
}

func TestPrompt_NewFormatOnFreshFile(t *testing.T) {
	dir := tempDir(t)
	s := NewStore(filepath.Join(dir, "digests.json"))

	// On a fresh store, writing a prompt should create the new format.
	_ = s.SetPrompt(PromptNameAgenda, "test")

	data, err := os.ReadFile(filepath.Join(dir, "digests.json"))
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}

	// Should be a JSON object with "digests" key, not a bare array.
	if !strings.HasPrefix(strings.TrimSpace(string(data)), "{") {
		t.Errorf("expected JSON object format, got: %s", string(data)[:50])
	}

	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("failed to parse new format: %v", err)
	}
	if sf.Digests == nil {
		t.Error("expected digests field")
	}
	if len(sf.Digests) != 0 {
		t.Errorf("expected 0 digests, got %d", len(sf.Digests))
	}
}
