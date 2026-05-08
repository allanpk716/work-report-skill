// Package logger provides a daemon-level structured logging wrapper built on
// logrus with file-rotatelogs for daily log rotation. It writes structured
// log files to ~/.work-report/logs/ with 7-day retention.
//
// CLI commands must NOT import this package — they keep pure JSONL stdout output.
//
// Note: we use logrus + file-rotatelogs directly instead of the higher-level
// WQGroup/logger package because WQGroup/logger's path validation rejects
// absolute paths under C:\Users on Windows (it blocks the entire user home
// directory as a "system directory"), which conflicts with the daemon's log
// directory at ~/.work-report/logs/.
package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	"github.com/sirupsen/logrus"
)

var (
	baseLogger *logrus.Logger
	logWriter  *rotatelogs.RotateLogs
)

// Init configures a structured logger that writes to logDir with daily
// rotation and 7-day retention.  logDir is created if it does not exist.
// Call Shutdown() before process exit to flush and release resources.
func Init(logDir string) error {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("logger: create log dir %s: %w", logDir, err)
	}

	logPattern := filepath.Join(logDir, "daemon--%Y%m%d%H%M--.log")

	writer, err := rotatelogs.New(
		logPattern,
		rotatelogs.WithMaxAge(7*24*time.Hour),
		rotatelogs.WithRotationTime(24*time.Hour),
	)
	if err != nil {
		return fmt.Errorf("logger: create rotatelogs writer: %w", err)
	}
	logWriter = writer

	baseLogger = &logrus.Logger{
		Out: io.MultiWriter(os.Stderr, writer),
		Formatter: &logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05",
			DisableColors:   true,
		},
		Level: logrus.InfoLevel,
	}

	return nil
}

// Shutdown releases logger resources (closes the rotatelogs writer).
// Safe to call multiple times.
func Shutdown() {
	if logWriter != nil {
		_ = logWriter.Close()
		logWriter = nil
	}
	baseLogger = nil
}

// GetLogger returns the underlying *logrus.Logger, or nil if not initialized.
// Other packages (storage, scheduler) that need a *log.Logger can wrap this
// via a stdlib adapter until they are migrated (T03).
func GetLogger() *logrus.Logger {
	return baseLogger
}

// Package-level convenience functions. Callers write logger.Infof(...)
// instead of importing logrus directly.

func Debug(args ...interface{}) {
	getOrCreate().Debug(args...)
}

func Debugf(format string, args ...interface{}) {
	getOrCreate().Debugf(format, args...)
}

func Info(args ...interface{}) {
	getOrCreate().Info(args...)
}

func Infof(format string, args ...interface{}) {
	getOrCreate().Infof(format, args...)
}

func Warn(args ...interface{}) {
	getOrCreate().Warn(args...)
}

func Warnf(format string, args ...interface{}) {
	getOrCreate().Warnf(format, args...)
}

func Error(args ...interface{}) {
	getOrCreate().Error(args...)
}

func Errorf(format string, args ...interface{}) {
	getOrCreate().Errorf(format, args...)
}

// WithField returns a logrus.Entry with a single structured field.
func WithField(key string, value interface{}) *logrus.Entry {
	return getOrCreate().WithField(key, value)
}

// WithFields returns a logrus.Entry with multiple structured fields.
func WithFields(fields map[string]interface{}) *logrus.Entry {
	return getOrCreate().WithFields(fields)
}

// getOrCreate returns the initialized logger or a safe fallback.
func getOrCreate() *logrus.Logger {
	if baseLogger != nil {
		return baseLogger
	}
	// Fallback: stderr-only logger if Init was never called (should not
	// happen in the daemon, but protects against nil-pointer panics).
	fallback := logrus.New()
	fallback.Out = os.Stderr
	fallback.Level = logrus.InfoLevel
	return fallback
}
