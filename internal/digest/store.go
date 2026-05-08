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

// read loads the JSON array from disk. Returns empty slice for ENOENT.
func (s *DigestStore) read() ([]DigestConfig, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []DigestConfig{}, nil
		}
		return nil, fmt.Errorf("digest: read %s: %w", s.path, err)
	}

	var digests []DigestConfig
	if err := json.Unmarshal(data, &digests); err != nil {
		return nil, fmt.Errorf("digest: parse %s: %w", s.path, err)
	}
	return digests, nil
}

// write persists the JSON array to disk, creating parent dirs as needed.
func (s *DigestStore) write(digests []DigestConfig) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("digest: mkdir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(digests, "", "  ")
	if err != nil {
		return fmt.Errorf("digest: marshal: %w", err)
	}

	if err := os.WriteFile(s.path, data, 0644); err != nil {
		return fmt.Errorf("digest: write %s: %w", s.path, err)
	}
	return nil
}

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
