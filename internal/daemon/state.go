// Package daemon manages the HTTP daemon lifecycle and state file.
package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
)

// DaemonState represents the daemon's runtime state written to disk.
type DaemonState struct {
	Port int `json:"port"`
	PID  int `json:"pid"`
}

// DefaultStateDir returns ~/.work-report, creating it if needed.
func DefaultStateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("daemon: cannot determine home dir: %w", err)
	}
	dir := filepath.Join(home, ".work-report")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("daemon: cannot create state dir: %w", err)
	}
	return dir, nil
}

// StatePath returns the path to the state file for a given directory.
func StatePath(dir string) string {
	return filepath.Join(dir, ".daemon.json")
}

// BackupPath returns the path to the backup state file.
func BackupPath(dir string) string {
	return filepath.Join(dir, ".daemon.json.bak")
}

// WriteState writes the daemon state to the state file, creating a .bak backup first.
func WriteState(dir string, state DaemonState) error {
	mainPath := StatePath(dir)
	bakPath := BackupPath(dir)

	// Write backup first
	bakData, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("daemon: marshal state: %w", err)
	}
	if err := os.WriteFile(bakPath, bakData, 0644); err != nil {
		return fmt.Errorf("daemon: write backup: %w", err)
	}

	// Write main state file
	if err := os.WriteFile(mainPath, bakData, 0644); err != nil {
		return fmt.Errorf("daemon: write state: %w", err)
	}
	return nil
}

// ReadState reads the daemon state, recovering from backup if the main file is corrupt.
func ReadState(dir string) (DaemonState, error) {
	mainPath := StatePath(dir)
	bakPath := BackupPath(dir)

	// Try reading main state file
	state, err := readStateFile(mainPath)
	if err == nil {
		return state, nil
	}

	// Main file corrupt or missing, try backup
	state, errBak := readStateFile(bakPath)
	if errBak == nil {
		return state, nil
	}

	return DaemonState{}, fmt.Errorf("daemon: state file and backup both unreadable: main=%v, backup=%v", err, errBak)
}

// RemoveState removes both the state file and its backup.
func RemoveState(dir string) error {
	err1 := os.Remove(StatePath(dir))
	err2 := os.Remove(BackupPath(dir))
	if err1 != nil && !os.IsNotExist(err1) {
		return err1
	}
	if err2 != nil && !os.IsNotExist(err2) {
		return err2
	}
	return nil
}

// IsPortInUse checks if a TCP port is already in use by attempting to bind.
// On Windows, binding 0.0.0.0:port does not conflict with a listener on
// 127.0.0.1:port (and vice-versa), so both addresses must be probed.
func IsPortInUse(port int) bool {
	// Try binding on 0.0.0.0
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return true // failed to bind — something is using it
	}
	ln.Close()

	// On Windows, also try 127.0.0.1 to detect loopback-only listeners.
	if runtime.GOOS == "windows" {
		ln2, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return true
		}
		ln2.Close()
	}

	return false
}

func readStateFile(path string) (DaemonState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DaemonState{}, err
	}
	if len(data) == 0 {
		return DaemonState{}, fmt.Errorf("daemon: empty state file: %s", path)
	}
	var state DaemonState
	if err := json.Unmarshal(data, &state); err != nil {
		return DaemonState{}, fmt.Errorf("daemon: corrupt state file %s: %w", path, err)
	}
	return state, nil
}
