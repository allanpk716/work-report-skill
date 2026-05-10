package cmd

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"wr/internal/backup"
	"wr/internal/config"
	"wr/internal/digest"
	"wr/internal/storage"
)

// statusResult is the structured output for wr status.
type statusResult struct {
	Config statusConfig `json:"config"`
	Data   statusData   `json:"data"`
}

// statusConfig shows configuration health.
type statusConfig struct {
	Timezone  string `json:"timezone"`
	DataDir   string `json:"data_dir"`
	Pushover  string `json:"pushover"`
	LLMText   string `json:"llm_text"`
	LLMVision string `json:"llm_vision"`
}

// statusData shows record statistics.
type statusData struct {
	TotalRecords int            `json:"total_records"`
	ByType       map[string]int `json:"by_type"`
	ByStatus     map[string]int `json:"by_status"`
	Digests      int            `json:"digests"`
	Backups      int            `json:"backups"`
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show configuration and data statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		// 1. Load config
		cfg := loadConfig()

		// 2. Build config summary
		result := statusResult{
			Config: buildStatusConfig(cfg),
		}

		// 3. Build data statistics
		result.Data = buildStatusData(cfg)

		// 4. Output as JSONL
		writeJSONLSuccess(result)
		return nil
	},
}

// buildStatusConfig extracts config health information.
func buildStatusConfig(cfg *config.Config) statusConfig {
	sc := statusConfig{
		Timezone:  "UTC",
		DataDir:   "",
		Pushover:  "not_configured",
		LLMText:   "not_configured",
		LLMVision: "not_configured",
	}

	if cfg != nil {
		sc.Timezone = cfg.Timezone
		sc.DataDir = cfg.DataDir

		if cfg.Pushover.APIToken != "" && cfg.Pushover.UserKey != "" {
			sc.Pushover = "configured"
		}
		if cfg.LLM.Text.APIKey != "" {
			sc.LLMText = "configured"
		}
		if cfg.LLM.Vision.APIKey != "" {
			sc.LLMVision = "configured"
		}
	}

	// Fallback for missing config
	if sc.DataDir == "" {
		if dir, err := config.DefaultDataDir(); err == nil {
			sc.DataDir = dir
		}
	}
	if sc.Timezone == "" {
		sc.Timezone = "Asia/Shanghai"
	}

	return sc
}

// buildStatusData collects record counts, digest count, and backup count.
func buildStatusData(cfg *config.Config) statusData {
	sd := statusData{
		ByType:   map[string]int{},
		ByStatus: map[string]int{},
	}

	// Determine data dir
	dataDir := ""
	if cfg != nil {
		dataDir = cfg.DataDir
	}
	if dataDir == "" {
		if dir, err := config.DefaultDataDir(); err == nil {
			dataDir = dir
		}
	}

	// Count records by type and status
	if dataDir != "" {
		store := storage.New(dataDir)
		records, err := store.ListRecords(storage.ListOptions{
			IncludeCompleted: true,
			Status:           "all",
		})
		if err == nil {
			sd.TotalRecords = len(records)
			for _, r := range records {
				sd.ByType[string(r.Type)]++
				sd.ByStatus[r.Status]++
			}
		}
	}

	// Count digests
	if home, err := os.UserHomeDir(); err == nil {
		digestPath := filepath.Join(home, ".work-report", "digests.json")
		ds := digest.NewStore(digestPath)
		if digests, err := ds.List(); err == nil {
			sd.Digests = len(digests)
		}
	}

	// Count backups
	if cfgPath, err := backup.ConfigPath(); err == nil {
		if bCfg, err := backup.LoadConfig(cfgPath); err == nil {
			if backups, err := backup.ListBackups(bCfg.OutputDir); err == nil {
				sd.Backups = len(backups)
			}
		}
	}

	return sd
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
