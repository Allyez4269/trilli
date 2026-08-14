package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"sync"
)

// Global flag to enable/disable debug logging
var DebugLoggingEnabled bool = false

// Logger is a custom logger with package-based filtering
type Logger struct {
	mu         sync.RWMutex
	logger     *log.Logger
	disabled   map[string]bool
	allEnabled bool
}

// New creates a new Logger instance
func New() *Logger {
	return &Logger{
		logger:     log.New(os.Stdout, "", log.LstdFlags),
		disabled:   make(map[string]bool),
		allEnabled: true,
	}
}

// SetOutput sets the output destination for the logger
func (l *Logger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logger.SetOutput(w)
}

// SetFlags sets the output flags for the logger
func (l *Logger) SetFlags(flags int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logger.SetFlags(flags)
}

// Enable enables logging for a specific package
func (l *Logger) Enable(packageName string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.disabled, packageName)
}

// Disable disables logging for a specific package
func (l *Logger) Disable(packageName string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.disabled[packageName] = true
}

// EnableAll enables logging for all packages
func (l *Logger) EnableAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.allEnabled = true
	l.disabled = make(map[string]bool)
}

// DisableAll disables logging for all packages
func (l *Logger) DisableAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.allEnabled = false
}

// IsEnabled checks if a package is enabled for logging
func (l *Logger) IsEnabled(packageName string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if !l.allEnabled {
		return false
	}

	return !l.disabled[packageName]
}

// Log logs a message for the specified package
func (l *Logger) Log(packageName, level, format string, v ...interface{}) {
	if l.IsEnabled(packageName) {
		l.mu.RLock()
		logger := l.logger
		l.mu.RUnlock()

		message := fmt.Sprintf(format, v...)
		logger.Printf("[%s][%s] %s", packageName, level, message)
	}
}

// Info logs an informational message
func (l *Logger) Info(packageName, format string, v ...interface{}) {
	l.Log(packageName, "INFO", format, v...)
}

// Error logs an error message
func (l *Logger) Error(packageName, format string, v ...interface{}) {
	l.Log(packageName, "ERROR", format, v...)
}

// Debug logs a debug message
func (l *Logger) Debug(packageName, format string, v ...interface{}) {
	// Skip debug logging entirely if the global flag is disabled
	if !DebugLoggingEnabled {
		return
	}
	l.Log(packageName, "DEBUG", format, v...)
}

// Fatal logs a fatal error message and exits the application
func (l *Logger) Fatal(packageName, format string, v ...interface{}) {
	if l.IsEnabled(packageName) {
		l.mu.RLock()
		logger := l.logger
		l.mu.RUnlock()

		message := fmt.Sprintf(format, v...)
		logger.Fatalf("[%s][FATAL] %s", packageName, message)
	}
}

// Default logger instance
var DefaultLogger = New()

// Helper functions that use the default logger

// Enable enables logging for a package in the default logger
func Enable(packageName string) {
	DefaultLogger.Enable(packageName)
}

// Disable disables logging for a package in the default logger
func Disable(packageName string) {
	DefaultLogger.Disable(packageName)
}

// EnableAll enables all logging in the default logger
func EnableAll() {
	DefaultLogger.EnableAll()
}

// DisableAll disables all logging in the default logger
func DisableAll() {
	DefaultLogger.DisableAll()
}

// SetDebugLogging sets the global debug logging flag
func SetDebugLogging(enabled bool) {
	DebugLoggingEnabled = enabled
}

// Info logs an informational message using the default logger
func Info(packageName, format string, v ...interface{}) {
	DefaultLogger.Info(packageName, format, v...)
}

// Error logs an error message using the default logger
func Error(packageName, format string, v ...interface{}) {
	DefaultLogger.Error(packageName, format, v...)
}

// Debug logs a debug message using the default logger
func Debug(packageName, format string, v ...interface{}) {
	// Skip debug logging entirely if the global flag is disabled
	if !DebugLoggingEnabled {
		return
	}
	DefaultLogger.Debug(packageName, format, v...)
}

// Fatal logs a fatal error message and exits using the default logger
func Fatal(packageName, format string, v ...interface{}) {
	DefaultLogger.Fatal(packageName, format, v...)
}
