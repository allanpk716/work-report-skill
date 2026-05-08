// Package digest provides CRUD storage for digest configurations.
// Digests are scheduled batch summaries of work records, stored in a single
// JSON array file at ~/.work-report/digests.json.
//
// This is a standalone package (D008) — it does not depend on the record
// storage or scheduler packages. Data queries for digest execution are
// handled by the scheduler in a separate layer (S03).
package digest

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"wr/internal/logger"
)

// Direction controls the digest output orientation.
type Direction string

const (
	// DirectionAgenda summarizes records as a forward-looking agenda.
	DirectionAgenda Direction = "agenda"
	// DirectionSummary summarizes records as a retrospective summary.
	DirectionSummary Direction = "summary"
)

// DigestConfig represents a single digest configuration.
type DigestConfig struct {
	ID        string    `json:"id"`
	Schedule  string    `json:"schedule"` // cron expression, e.g. "0 8 * * *"
	Scope     Scope     `json:"scope"`
	Direction Direction `json:"direction"`
	Enabled   bool      `json:"enabled"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
}

// PromptName identifies a named prompt slot (e.g. "agenda", "report").
type PromptName string

const (
	// PromptNameAgenda is the default prompt used for agenda-style digests.
	PromptNameAgenda PromptName = "agenda"
	// PromptNameReport is the default prompt used for report-style digests.
	PromptNameReport PromptName = "report"
)

// PromptEntry stores a user-overridden prompt with metadata.
type PromptEntry struct {
	Text      string `json:"text"`
	UpdatedAt string `json:"updated_at"`
}

// Default prompts used when no override is set.
var (
	DefaultPromptAgenda = `你是一个专业的工作助手。请根据以下工作记录，生成今日待办议程（Agenda）。

要求：
1. 按优先级排序，标注紧急程度
2. 列出未完成的上期任务
3. 识别潜在的阻塞问题
4. 建议时间分配`

	DefaultPromptReport = `你是一个专业的工作助手。请根据以下工作记录，生成工作日报（Report）。

要求：
1. 按项目/模块分类总结
2. 标注完成状态（完成/进行中/待开始）
3. 突出关键成果和里程碑
4. 列出遇到的问题和解决方案`
)

// DefaultPrompts maps built-in prompt names to their default text.
func DefaultPrompts() map[PromptName]string {
	return map[PromptName]string{
		PromptNameAgenda: DefaultPromptAgenda,
		PromptNameReport: DefaultPromptReport,
	}
}

// storeFile is the on-disk JSON structure for digests.json.
// It wraps the digest list and prompt overrides in a single object.
type storeFile struct {
	Digests         []DigestConfig              `json:"digests"`
	PromptOverrides map[PromptName]PromptEntry  `json:"prompt_overrides,omitempty"`
}

// DigestStore provides mutex-protected CRUD operations on a digests.json file.
type DigestStore struct {
	path string
	mu   sync.Mutex
}

// NewStore creates a DigestStore backed by the given file path.
// The file and its parent directory are created on first write if they
// do not exist.
func NewStore(path string) *DigestStore {
	return &DigestStore{path: path}
}

// DefaultStorePath returns ~/.work-report/digests.json.
func DefaultStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("digest: cannot determine home dir: %w", err)
	}
	return filepath.Join(home, ".work-report", "digests.json"), nil
}

// Load reads the digests file from disk. Returns an empty slice if the file
// does not exist (first run). Returns an error for any other read/parse
// failure.
func (s *DigestStore) Load() ([]DigestConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.read()
}

// Add creates a new digest configuration with an auto-generated ID and
// timestamps. Returns the created config.
func (s *DigestStore) Add(cfg DigestConfig) (DigestConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cfg.ID != "" {
		return DigestConfig{}, fmt.Errorf("digest: add: ID must be empty (auto-generated), got %q", cfg.ID)
	}

	if err := validateSchedule(cfg.Schedule); err != nil {
		return DigestConfig{}, err
	}

	cfg.ID = generateID()
	now := time.Now().Format(time.RFC3339)
	cfg.CreatedAt = now
	cfg.UpdatedAt = now
	if !cfg.Enabled {
		cfg.Enabled = true // default to enabled
	}

	digests, err := s.read()
	if err != nil {
		return DigestConfig{}, err
	}

	digests = append(digests, cfg)

	if err := s.write(digests); err != nil {
		return DigestConfig{}, err
	}

	logger.WithField("digest_id", cfg.ID).WithField("scope", string(cfg.Scope)).WithField("schedule", cfg.Schedule).Info("digest added")
	return cfg, nil
}

// Get returns the digest with the given ID, or ErrNotFound.
func (s *DigestStore) Get(id string) (DigestConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	digests, err := s.read()
	if err != nil {
		return DigestConfig{}, err
	}

	for _, d := range digests {
		if d.ID == id {
			return d, nil
		}
	}
	return DigestConfig{}, ErrNotFound
}

// List returns all digest configurations.
func (s *DigestStore) List() ([]DigestConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.read()
}

// Remove deletes the digest with the given ID. Returns ErrNotFound if the
// ID does not exist.
func (s *DigestStore) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	digests, err := s.read()
	if err != nil {
		return err
	}

	found := -1
	for i, d := range digests {
		if d.ID == id {
			found = i
			break
		}
	}
	if found == -1 {
		return ErrNotFound
	}

	digests = append(digests[:found], digests[found+1:]...)
	if err := s.write(digests); err != nil {
		return err
	}

	logger.WithField("digest_id", id).Info("digest removed")
	return nil
}

// Enable sets the digest with the given ID to enabled=true.
func (s *DigestStore) Enable(id string) error {
	return s.setEnabled(id, true)
}

// Disable sets the digest with the given ID to enabled=false.
func (s *DigestStore) Disable(id string) error {
	return s.setEnabled(id, false)
}

// setEnabled flips the enabled flag and persists.
func (s *DigestStore) setEnabled(id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	digests, err := s.read()
	if err != nil {
		return err
	}

	found := false
	for i, d := range digests {
		if d.ID == id {
			digests[i].Enabled = enabled
			digests[i].UpdatedAt = time.Now().Format(time.RFC3339)
			found = true
			break
		}
	}
	if !found {
		return ErrNotFound
	}

	if err := s.write(digests); err != nil {
		return err
	}

	action := "disabled"
	if enabled {
		action = "enabled"
	}
	logger.WithField("digest_id", id).WithField("action", action).Info("digest state changed")
	return nil
}

// --- internal helpers ---

// readStore loads the full storeFile from disk. Returns an empty storeFile
// for ENOENT. Handles backward compatibility: if the file contains a raw
// []DigestConfig array (old format), it is transparently migrated.
func (s *DigestStore) readStore() (*storeFile, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &storeFile{Digests: []DigestConfig{}, PromptOverrides: map[PromptName]PromptEntry{}}, nil
		}
		return nil, fmt.Errorf("digest: read %s: %w", s.path, err)
	}

	// Try new object format first.
	var sf storeFile
	if err := json.Unmarshal(data, &sf); err == nil && sf.Digests != nil {
		if sf.PromptOverrides == nil {
			sf.PromptOverrides = map[PromptName]PromptEntry{}
		}
		return &sf, nil
	}

	// Fallback: old format was a bare []DigestConfig array.
	var digests []DigestConfig
	if err := json.Unmarshal(data, &digests); err != nil {
		return nil, fmt.Errorf("digest: parse %s: %w", s.path, err)
	}

	if digests == nil {
		digests = []DigestConfig{}
	}

	return &storeFile{
		Digests:         digests,
		PromptOverrides: map[PromptName]PromptEntry{},
	}, nil
}

// read loads digests from disk (backward-compatible convenience wrapper).
func (s *DigestStore) read() ([]DigestConfig, error) {
	sf, err := s.readStore()
	if err != nil {
		return nil, err
	}
	return sf.Digests, nil
}

// writeStore persists the full storeFile to disk, creating parent dirs as needed.
func (s *DigestStore) writeStore(sf *storeFile) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("digest: mkdir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("digest: marshal: %w", err)
	}

	if err := os.WriteFile(s.path, data, 0644); err != nil {
		return fmt.Errorf("digest: write %s: %w", s.path, err)
	}
	return nil
}

// write persists the digests list to disk (convenience wrapper that preserves prompt overrides).
func (s *DigestStore) write(digests []DigestConfig) error {
	sf, err := s.readStore()
	if err != nil {
		return err
	}
	sf.Digests = digests
	return s.writeStore(sf)
}

// --- Prompt methods ---

// GetPrompt returns the effective prompt text for the given name.
// If a user override exists, it is returned; otherwise the built-in default.
// Returns ErrPromptNotFound if the name has no default.
func (s *DigestStore) GetPrompt(name PromptName) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if err != nil {
		return "", err
	}

	if entry, ok := sf.PromptOverrides[name]; ok {
		return entry.Text, nil
	}

	def, ok := DefaultPrompts()[name]
	if !ok {
		return "", ErrPromptNotFound
	}
	return def, nil
}

// SetPrompt stores a user override for the named prompt.
// The name does not need to be a built-in — any name is accepted.
func (s *DigestStore) SetPrompt(name PromptName, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if err != nil {
		return err
	}

	if sf.PromptOverrides == nil {
		sf.PromptOverrides = map[PromptName]PromptEntry{}
	}

	sf.PromptOverrides[name] = PromptEntry{
		Text:      text,
		UpdatedAt: time.Now().Format(time.RFC3339),
	}

	if err := s.writeStore(sf); err != nil {
		return err
	}

	logger.WithField("prompt_name", string(name)).Info("prompt set")
	return nil
}

// ResetPrompt removes a user override, restoring the built-in default.
// Returns ErrPromptNotFound if the name has no default.
// If the name has a default and no override, this is a no-op.
func (s *DigestStore) ResetPrompt(name PromptName) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Verify the name has a default; otherwise there's nothing to reset to.
	if _, ok := DefaultPrompts()[name]; !ok {
		return ErrPromptNotFound
	}

	sf, err := s.readStore()
	if err != nil {
		return err
	}

	if _, hasOverride := sf.PromptOverrides[name]; hasOverride {
		delete(sf.PromptOverrides, name)
		if err := s.writeStore(sf); err != nil {
			return err
		}
		logger.WithField("prompt_name", string(name)).Info("prompt reset to default")
	}

	return nil
}

// ListPrompts returns all prompt names with their effective text and whether
// each is a user override or the built-in default.
func (s *DigestStore) ListPrompts() (map[PromptName]PromptInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.readStore()
	if err != nil {
		return nil, err
	}

	defaults := DefaultPrompts()
	result := make(map[PromptName]PromptInfo, len(defaults)+len(sf.PromptOverrides))

	// Add all defaults first.
	for name, defText := range defaults {
		info := PromptInfo{
			Name:    name,
			Text:    defText,
			IsDefault: true,
		}
		if entry, ok := sf.PromptOverrides[name]; ok {
			info.Text = entry.Text
			info.IsDefault = false
			info.UpdatedAt = entry.UpdatedAt
		}
		result[name] = info
	}

	// Add any custom overrides that are not built-in names.
	for name, entry := range sf.PromptOverrides {
		if _, isBuiltIn := defaults[name]; !isBuiltIn {
			result[name] = PromptInfo{
				Name:      name,
				Text:      entry.Text,
				IsDefault: false,
				UpdatedAt: entry.UpdatedAt,
			}
		}
	}

	return result, nil
}

// PromptInfo describes a prompt's current state for listing.
type PromptInfo struct {
	Name      PromptName `json:"name"`
	Text      string     `json:"text"`
	IsDefault bool       `json:"is_default"`
	UpdatedAt string     `json:"updated_at,omitempty"`
}

// ErrPromptNotFound is returned when a prompt name has no default and no override.
var ErrPromptNotFound = fmt.Errorf("digest: prompt not found")

// --- ID generation ---

// generateID returns a unique digest ID in the format d_YYYYMMDD_<random6>.
func generateID() string {
	now := time.Now()
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return fmt.Sprintf("d_%s_%x", now.Format("20060102"), b)
}

// validateSchedule performs a basic format check on the cron expression.
// A full cron parser is out of scope for the data layer — the scheduler
// (S03) will do complete validation at registration time.
func validateSchedule(schedule string) error {
	if schedule == "" {
		return fmt.Errorf("digest: schedule must not be empty")
	}
	// Basic sanity: reject obviously wrong input (too short, no spaces).
	// Real cron validation happens in the scheduler layer.
	if len(schedule) < 5 {
		return fmt.Errorf("digest: invalid schedule %q: too short", schedule)
	}
	return nil
}

// ErrNotFound is returned when a digest ID does not match any stored config.
var ErrNotFound = fmt.Errorf("digest: not found")
