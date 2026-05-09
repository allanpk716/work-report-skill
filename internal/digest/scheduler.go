package digest

import (
	"sync"

	"github.com/robfig/cron/v3"
	"wr/internal/logger"
)

// cronParser validates standard 5-field cron expressions (minute hour day month weekday).
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// DigestCallback is the function signature called when a cron entry fires.
// It receives the DigestConfig that triggered, so the summarize pipeline
// (T02) can resolve scope, direction, and prompt.
type DigestCallback func(cfg DigestConfig)

// DigestScheduler manages a standalone cron instance for digest execution.
// It follows D008 — a separate scheduler from the record-based one in
// internal/scheduler, because digest triggers batch queries + LLM + push
// instead of single-record reminders.
//
// Observability: all actions log with [digest-scheduler] prefix and include
// digest_id, schedule, scope, direction, entry_count fields.
type DigestScheduler struct {
	cron     *cron.Cron
	store    *DigestStore
	callback DigestCallback
	mu       sync.Mutex
	entries  map[string]cron.EntryID // digest ID → cron entry ID
}

// NewDigestScheduler creates a scheduler backed by the given store.
// The callback is invoked on each cron trigger with the matching DigestConfig.
func NewDigestScheduler(store *DigestStore, cb DigestCallback) *DigestScheduler {
	return &DigestScheduler{
		cron:     cron.New(),
		store:    store,
		callback: cb,
		entries:  make(map[string]cron.EntryID),
	}
}

// Start begins the cron scheduler. Call Sync() afterwards to load configs,
// or rely on external Sync() calls.
func (ds *DigestScheduler) Start() {
	ds.cron.Start()
	logger.Info("[digest-scheduler] started")
}

// Stop gracefully stops the cron scheduler, waiting for running jobs to finish.
func (ds *DigestScheduler) Stop() {
	ctx := ds.cron.Stop()
	<-ctx.Done()
	ds.mu.Lock()
	ds.entries = make(map[string]cron.EntryID)
	ds.mu.Unlock()
	logger.Info("[digest-scheduler] stopped")
}

// Sync loads all enabled digest configs from the store and replaces the
// current cron entries. Existing entries not present in the store are
// removed; new entries are added.
//
// Failure modes (per task plan):
//   - Store.Load() error: log error, skip sync, keep existing entries.
//   - Invalid cron expression: log warning, skip that digest, continue.
func (ds *DigestScheduler) Sync() {
	configs, err := ds.store.Load()
	if err != nil {
		logger.WithField("error", err).Error("[digest-scheduler] sync: failed to load configs, keeping existing entries")
		return
	}

	ds.mu.Lock()
	defer ds.mu.Unlock()

	// Collect IDs that are still valid.
	activeIDs := make(map[string]bool, len(configs))

	// Register enabled configs.
	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}
		activeIDs[cfg.ID] = true

		// Skip if already registered.
		if _, exists := ds.entries[cfg.ID]; exists {
			continue
		}

		// Validate cron expression.
		_, parseErr := cronParser.Parse(cfg.Schedule)
		if parseErr != nil {
			logger.WithField("digest_id", cfg.ID).
				WithField("schedule", cfg.Schedule).
				Warn("[digest-scheduler] sync: invalid cron expression, skipping")
			continue
		}

		ds.registerLocked(cfg)
	}

	// Remove entries no longer in the store or now disabled.
	for id, entryID := range ds.entries {
		if !activeIDs[id] {
			ds.cron.Remove(entryID)
			delete(ds.entries, id)
			logger.WithField("digest_id", id).Info("[digest-scheduler] unregistered")
		}
	}

	logger.WithField("entry_count", len(ds.entries)).Info("[digest-scheduler] sync complete")
}

// Register adds a single digest config to the cron scheduler.
// If the digest is already registered, it is a no-op.
// Returns an error if the cron expression is invalid.
func (ds *DigestScheduler) Register(cfg DigestConfig) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	return ds.registerLocked(cfg)
}

// registerLocked must be called with ds.mu held.
func (ds *DigestScheduler) registerLocked(cfg DigestConfig) error {
	if _, exists := ds.entries[cfg.ID]; exists {
		return nil
	}

	// Validate cron expression.
	_, parseErr := cronParser.Parse(cfg.Schedule)
	if parseErr != nil {
		logger.WithField("digest_id", cfg.ID).
			WithField("schedule", cfg.Schedule).
			Warn("[digest-scheduler] register: invalid cron expression")
		return parseErr
	}

	cb := ds.callback
	entryID, err := ds.cron.AddFunc(cfg.Schedule, func() {
		logger.WithField("digest_id", cfg.ID).
			WithField("scope", string(cfg.Scope)).
			WithField("direction", string(cfg.Direction)).
			Info("[digest-scheduler] trigger fired")
		if cb != nil {
			cb(cfg)
		}
	})
	if err != nil {
		logger.WithField("digest_id", cfg.ID).Error("[digest-scheduler] register: cron.AddFunc failed")
		return err
	}

	ds.entries[cfg.ID] = entryID
	logger.WithField("digest_id", cfg.ID).
		WithField("schedule", cfg.Schedule).
		WithField("scope", string(cfg.Scope)).
		WithField("direction", string(cfg.Direction)).
		Info("[digest-scheduler] registered")

	return nil
}

// Unregister removes a single digest config from the cron scheduler.
// If the digest is not registered, it is a no-op.
func (ds *DigestScheduler) Unregister(id string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	if entryID, exists := ds.entries[id]; exists {
		ds.cron.Remove(entryID)
		delete(ds.entries, id)
		logger.WithField("digest_id", id).Info("[digest-scheduler] unregistered")
	}
}

// RegisteredEntries returns the number of currently registered cron entries.
func (ds *DigestScheduler) RegisteredEntries() int {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return len(ds.entries)
}
