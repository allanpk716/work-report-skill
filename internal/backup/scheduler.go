package backup

import (
	"sync"

	"github.com/robfig/cron/v3"
	"wr/internal/logger"
)

// cronParser validates 6-field cron expressions (with seconds).
var cronParser = cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// BackupScheduler manages a standalone cron instance for scheduled backup
// execution. It follows the same pattern as DigestScheduler — a separate
// cron.New(cron.WithSeconds()) instance, mutex-protected entries map, and
// Sync() for loading config and reconciling cron entries.
//
// When a cron entry fires, the scheduler calls CreateBackup() followed by
// GFSRotate(). Errors from either operation are logged but do not block
// subsequent scheduled runs.
//
// Observability: all actions log with [backup-scheduler] prefix and include
// schedule, entry_count fields.
type BackupScheduler struct {
	cron *cron.Cron
	mu   sync.Mutex
	id   cron.EntryID // the single cron entry ID (one schedule, not per-item like digest)
}

// NewBackupScheduler creates a new BackupScheduler.
func NewBackupScheduler() *BackupScheduler {
	return &BackupScheduler{
		cron: cron.New(cron.WithSeconds()),
	}
}

// Start begins the cron scheduler. Call Sync() afterwards to load the config
// and register the backup schedule.
func (bs *BackupScheduler) Start() {
	bs.cron.Start()
	logger.Info("[backup-scheduler] started")
}

// Stop gracefully stops the cron scheduler, waiting for running jobs to finish.
func (bs *BackupScheduler) Stop() {
	ctx := bs.cron.Stop()
	<-ctx.Done()
	bs.mu.Lock()
	bs.id = 0
	bs.mu.Unlock()
	logger.Info("[backup-scheduler] stopped")
}

// Sync loads the backup config from disk and registers/unregisters the cron
// entry. If the schedule is empty, disabled, or invalid, any existing entry is
// removed.
//
// Failure modes:
//   - Config load error: log error, skip sync, keep existing entry.
//   - Invalid cron expression: log warning, remove existing entry if any.
func (bs *BackupScheduler) Sync() {
	cfgPath, err := ConfigPath()
	if err != nil {
		logger.WithField("error", err).Error("[backup-scheduler] sync: failed to determine config path")
		return
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		logger.WithField("error", err).Error("[backup-scheduler] sync: failed to load config, keeping existing entry")
		return
	}

	bs.mu.Lock()
	defer bs.mu.Unlock()

	// If schedule is disabled or empty, remove any existing entry.
	if !cfg.Enabled || cfg.Schedule == "" {
		if bs.id != 0 {
			bs.cron.Remove(bs.id)
			bs.id = 0
			logger.Info("[backup-scheduler] unregistered (schedule disabled or empty)")
		}
		return
	}

	// Validate cron expression.
	_, parseErr := cronParser.Parse(cfg.Schedule)
	if parseErr != nil {
		logger.WithField("schedule", cfg.Schedule).
			Warn("[backup-scheduler] sync: invalid cron expression, skipping")
		// Remove any existing entry if the schedule changed to invalid.
		if bs.id != 0 {
			bs.cron.Remove(bs.id)
			bs.id = 0
		}
		return
	}

	// Already registered with the same schedule — no-op.
	// We remove and re-register if the schedule changed.
	if bs.id != 0 {
		bs.cron.Remove(bs.id)
		bs.id = 0
	}

	// Capture config for the closure.
	schedule := cfg.Schedule
	outputDir := cfg.OutputDir
	policy := cfg.Retention

	entryID, err := bs.cron.AddFunc(schedule, func() {
		logger.WithField("schedule", schedule).Info("[backup-scheduler] trigger fired")

		// Run backup.
		dataDir, dataErr := DataDir()
		if dataErr != nil {
			logger.WithField("error", dataErr).Error("[backup-scheduler] backup failed: cannot determine data dir")
			return
		}

		_, _, backupErr := CreateBackup(dataDir, outputDir)
		if backupErr != nil {
			logger.WithField("error", backupErr).Error("[backup-scheduler] CreateBackup failed")
			// Continue to rotation even if backup failed — there may be old backups to rotate.
		}

		// Run GFS rotation.
		backups, listErr := ListBackups(outputDir)
		if listErr != nil {
			logger.WithField("error", listErr).Error("[backup-scheduler] GFSRotate failed: cannot list backups")
			return
		}

		_, rotateErr := GFSRotate(backups, policy, outputDir)
		if rotateErr != nil {
			logger.WithField("error", rotateErr).Error("[backup-scheduler] GFSRotate failed")
		}
	})
	if err != nil {
		logger.WithField("schedule", schedule).Error("[backup-scheduler] register: cron.AddFunc failed")
		return
	}

	bs.id = entryID
	logger.WithField("schedule", schedule).Info("[backup-scheduler] registered")
}

// Registered returns whether a cron entry is currently active.
func (bs *BackupScheduler) Registered() bool {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	return bs.id != 0
}
