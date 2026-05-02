package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"wr/internal/client"
	"wr/internal/config"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status and config diagnostics",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Try calling the daemon's /api/status endpoint
		err := client.CallDaemonGet(os.Stdout, "/api/status")
		if err == nil {
			return nil
		}

		// Daemon is not running — fall through to local config diagnostics
		localStatus()
		return nil
	},
}

// localStatus outputs JSONL with daemon status=not_running and local config diagnostics.
func localStatus() {
	cfg, cfgErr := config.LoadDefault()

	// Check config file existence
	configPath, _ := config.DefaultConfigPath()
	configExists := false
	if configPath != "" {
		if _, err := os.Stat(configPath); err == nil {
			configExists = true
		}
	}

	configInfo := map[string]interface{}{
		"exists": configExists,
	}

	if cfgErr == nil && cfg != nil {
		// Check data dir accessibility
		dataDirAccessible := false
		if cfg.DataDir != "" {
			if fi, err := os.Stat(cfg.DataDir); err == nil && fi.IsDir() {
				dataDirAccessible = true
			}
		}

		configInfo = map[string]interface{}{
			"exists": configExists,
			"pushover": map[string]interface{}{
				"configured": cfg.Pushover.APIToken != "" && cfg.Pushover.UserKey != "",
			},
			"llm": map[string]interface{}{
				"text": map[string]interface{}{
					"configured": cfg.LLM.Text.APIKey != "",
				},
				"vision": map[string]interface{}{
					"configured": cfg.LLM.Vision.APIKey != "",
				},
			},
			"data_dir": map[string]interface{}{
				"path":       cfg.DataDir,
				"accessible": dataDirAccessible,
			},
		}
	}

	response := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"daemon": map[string]interface{}{
				"status":     "not_running",
				"suggestion": "Run 'wr daemon start' to start the daemon.",
			},
			"config": configInfo,
		},
	}

	b, _ := json.Marshal(response)
	fmt.Fprintf(os.Stdout, "%s\n", b)
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
