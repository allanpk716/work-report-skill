package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
	"github.com/spf13/cobra"

	"wr/internal/backup"
)

var backupOutputDir string

// backupCmd is the parent command for backup management.
var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Manage data backups with GFS rotation",
	Long:  `Create, list, and clean up timestamped zip backups of ~/.work-report/ data. Supports Grandfather-Father-Son rotation.`,
}

// backupCreateCmd creates a zip backup immediately.
var backupCreateCmd = &cobra.Command{
	Use:   "create [--output <dir>]",
	Short: "Create a zip backup immediately",
	Long: `Create a timestamped zip archive of ~/.work-report/ data (config.json, work-records/, digests.json, etc.) in the backup output directory. Filename format: wr-backup-YYYYMMDD-HHMMSS.zip.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dataDir, err := backup.DataDir()
		if err != nil {
			return writeExitError(agentsdk.ExitFatalError, err.Error())
		}

		outDir := backupOutputDir
		if outDir == "" {
			cfgPath, err2 := backup.ConfigPath()
			if err2 != nil {
				return writeExitError(agentsdk.ExitFatalError, err2.Error())
			}
			cfg, err2 := backup.LoadConfig(cfgPath)
			if err2 != nil {
				return writeExitError(agentsdk.ExitFatalError, err2.Error())
			}
			outDir = cfg.OutputDir
		}

		zipPath, size, err := backup.CreateBackup(dataDir, outDir)
		if err != nil {
			if strings.Contains(err.Error(), "data_dir_not_found") {
				return writeExitError(agentsdk.ExitNotFound, err.Error())
			}
			if strings.Contains(err.Error(), "backup_failed") {
				return writeExitError(agentsdk.ExitFatalError, err.Error())
			}
			return writeExitError(agentsdk.ExitFatalError, err.Error())
		}

		return app.JSONL().Success(map[string]interface{}{
			"path":       zipPath,
			"size_bytes": size,
			"output_dir": outDir,
		})
	},
}

// backupListCmd lists all existing backups.
var backupListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all backups with metadata",
	Long:  `List all wr-backup-*.zip files in the backup output directory, sorted newest first. Shows filename, size, and creation time.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		outDir := backupOutputDir
		if outDir == "" {
			cfgPath, err := backup.ConfigPath()
			if err != nil {
				return writeExitError(agentsdk.ExitFatalError, err.Error())
			}
			cfg, err := backup.LoadConfig(cfgPath)
			if err != nil {
				return writeExitError(agentsdk.ExitFatalError, err.Error())
			}
			outDir = cfg.OutputDir
		}

		metas, err := backup.ListBackups(outDir)
		if err != nil {
			return writeExitError(agentsdk.ExitFatalError, err.Error())
		}

		if metas == nil {
			metas = []backup.BackupMeta{}
		}

		return app.JSONL().Success(map[string]interface{}{
			"backups":    metas,
			"output_dir": outDir,
			"count":      len(metas),
		})
	},
}

// backupCleanupCmd runs GFS rotation to remove old backups.
var backupCleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Run GFS rotation to remove old backups",
	Long:  `Apply Grandfather-Father-Son rotation based on backup configuration retention policy. Removes backups that exceed the daily/weekly/monthly retention counts.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		outDir := backupOutputDir
		var policy backup.RetentionPolicy

		cfgPath, err := backup.ConfigPath()
		if err != nil {
			return writeExitError(agentsdk.ExitFatalError, err.Error())
		}
		cfg, err := backup.LoadConfig(cfgPath)
		if err != nil {
			return writeExitError(agentsdk.ExitFatalError, err.Error())
		}

		if outDir == "" {
			outDir = cfg.OutputDir
		}
		policy = cfg.Retention

		metas, err := backup.ListBackups(outDir)
		if err != nil {
			return writeExitError(agentsdk.ExitFatalError, err.Error())
		}

		result, err := backup.GFSRotate(metas, policy, outDir)
		if err != nil {
			return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("rotation_failed: %v", err))
		}

		return app.JSONL().Success(map[string]interface{}{
			"kept":       result.Kept,
			"removed":    result.Removed,
			"kept_count": len(result.Kept),
			"removed_count": func() int {
				// Count only actually-deleted files (removed may include failed deletes).
				count := 0
				for _, r := range result.Removed {
					if _, statErr := os.Stat(filepath.Join(outDir, r.Filename)); os.IsNotExist(statErr) {
						count++
					}
				}
				return count
			}(),
		})
	},
}

// backupConfigCmd is the parent for backup config sub-commands.
var backupConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage backup configuration",
}

// backupConfigShowCmd displays the current backup configuration.
var backupConfigShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Display backup configuration",
	Long:  `Load and display the current backup configuration from ~/.work-report/backup-config.json.`,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, err := backup.ConfigPath()
		if err != nil {
			return writeExitError(agentsdk.ExitFatalError, err.Error())
		}

		// Check if config file exists for a specific error.
		if _, statErr := os.Stat(cfgPath); os.IsNotExist(statErr) {
			cfg := backup.DefaultConfig()
			return app.JSONL().Success(map[string]interface{}{
				"config":      cfg,
				"config_path": cfgPath,
				"source":      "defaults",
			})
		}

		cfg, err := backup.LoadConfig(cfgPath)
		if err != nil {
			return writeExitError(agentsdk.ExitFatalError, err.Error())
		}

		return app.JSONL().Success(map[string]interface{}{
			"config":      cfg,
			"config_path": cfgPath,
			"source":      "file",
		})
	},
}

func init() {
	backupCmd.AddCommand(backupCreateCmd)
	backupCmd.AddCommand(backupListCmd)
	backupCmd.AddCommand(backupCleanupCmd)

	backupConfigCmd.AddCommand(backupConfigShowCmd)
	backupCmd.AddCommand(backupConfigCmd)

	// Global --output flag on parent backup command, accessible by all sub-commands.
	backupCmd.PersistentFlags().StringVar(&backupOutputDir, "output", "", "Override backup output directory")

	rootCmd.AddCommand(backupCmd)
}
