package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const maxLogLines = 5000

// LogBuffer is an io.Writer for the standard logger that appends to a log file
// and keeps the most recent lines in memory for the log window.
type LogBuffer struct {
	mu       sync.Mutex
	file     *os.File
	lines    []string
	onChange func()
}

func logFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Logs", "cloudsqlproxy.log"), nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cloudsqlproxy", "cloudsqlproxy.log"), nil
}

func NewLogBuffer(path string) (*LogBuffer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &LogBuffer{file: f}, nil
}

func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	_, _ = b.file.Write(p)
	_, _ = os.Stderr.Write(p)
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		b.lines = append(b.lines, line)
	}
	if len(b.lines) > maxLogLines {
		b.lines = append([]string(nil), b.lines[len(b.lines)-maxLogLines:]...)
	}
	onChange := b.onChange
	b.mu.Unlock()

	if onChange != nil {
		onChange()
	}
	return len(p), nil
}

func (b *LogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.lines, "\n")
}

func (b *LogBuffer) Clear() {
	b.mu.Lock()
	b.lines = nil
	b.mu.Unlock()
}

func (b *LogBuffer) Path() string {
	return b.file.Name()
}

func (b *LogBuffer) SetOnChange(fn func()) {
	b.mu.Lock()
	b.onChange = fn
	b.mu.Unlock()
}
