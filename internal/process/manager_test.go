package process

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leosjoberg/session-manager/internal/config"
	"go.uber.org/zap"
)

func TestNewManager(t *testing.T) {
	logDir := t.TempDir()
	logger := zap.NewNop()

	m := NewManager(logDir, logger)

	if m == nil {
		t.Fatal("expected non-nil manager")
	}
	if m.logDir != logDir {
		t.Errorf("expected logDir %q, got %q", logDir, m.logDir)
	}
	if m.processes == nil {
		t.Error("expected processes map to be initialized")
	}
}

func TestNewManagerNilLogger(t *testing.T) {
	logDir := t.TempDir()

	m := NewManager(logDir, nil)

	if m == nil {
		t.Fatal("expected non-nil manager")
	}
	if m.logger == nil {
		t.Error("expected logger to be set to nop logger")
	}
}

func TestStartAndStopProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping test on Windows")
	}

	logDir := t.TempDir()
	worktreePath := t.TempDir()
	logger := zap.NewNop()

	m := NewManager(logDir, logger)

	cfg := config.ProcessConfig{
		Name:    "test-process",
		Command: "sleep 60",
	}

	// Start the process
	err := m.Start(cfg, worktreePath)
	if err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	// Verify process is running
	proc := m.Get("test-process")
	if proc == nil {
		t.Fatal("expected process to be registered")
	}
	if proc.Status != StatusRunning {
		t.Errorf("expected status %v, got %v", StatusRunning, proc.Status)
	}
	if proc.PID <= 0 {
		t.Errorf("expected positive PID, got %d", proc.PID)
	}

	// Stop the process
	err = m.Stop("test-process")
	if err != nil {
		t.Fatalf("failed to stop process: %v", err)
	}

	// Give it a moment to update status
	time.Sleep(100 * time.Millisecond)

	// Verify process is stopped
	proc = m.Get("test-process")
	if proc.Status == StatusRunning {
		t.Error("expected process to not be running")
	}
}

func TestStartDuplicateProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping test on Windows")
	}

	logDir := t.TempDir()
	worktreePath := t.TempDir()
	logger := zap.NewNop()

	m := NewManager(logDir, logger)

	cfg := config.ProcessConfig{
		Name:    "test-process",
		Command: "sleep 60",
	}

	// Start the process
	err := m.Start(cfg, worktreePath)
	if err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	defer m.Stop("test-process")

	// Try to start it again
	err = m.Start(cfg, worktreePath)
	if err == nil {
		t.Error("expected error when starting duplicate process")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("expected 'already running' error, got: %v", err)
	}
}

func TestStopNonexistentProcess(t *testing.T) {
	logDir := t.TempDir()
	logger := zap.NewNop()

	m := NewManager(logDir, logger)

	err := m.Stop("nonexistent")
	if err == nil {
		t.Error("expected error when stopping nonexistent process")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestList(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping test on Windows")
	}

	logDir := t.TempDir()
	worktreePath := t.TempDir()
	logger := zap.NewNop()

	m := NewManager(logDir, logger)

	// Initially empty
	list := m.List()
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d processes", len(list))
	}

	// Start some processes
	cfg1 := config.ProcessConfig{Name: "proc1", Command: "sleep 60"}
	cfg2 := config.ProcessConfig{Name: "proc2", Command: "sleep 60"}

	m.Start(cfg1, worktreePath)
	m.Start(cfg2, worktreePath)
	defer m.StopAll()

	list = m.List()
	if len(list) != 2 {
		t.Errorf("expected 2 processes, got %d", len(list))
	}
}

func TestStopAll(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping test on Windows")
	}

	logDir := t.TempDir()
	worktreePath := t.TempDir()
	logger := zap.NewNop()

	m := NewManager(logDir, logger)

	// Start some processes
	cfg1 := config.ProcessConfig{Name: "proc1", Command: "sleep 60"}
	cfg2 := config.ProcessConfig{Name: "proc2", Command: "sleep 60"}

	m.Start(cfg1, worktreePath)
	m.Start(cfg2, worktreePath)

	// Stop all
	err := m.StopAll()
	if err != nil {
		t.Fatalf("failed to stop all processes: %v", err)
	}

	// Give it a moment to update status
	time.Sleep(100 * time.Millisecond)

	// Verify all stopped
	for _, proc := range m.List() {
		if proc.Status == StatusRunning {
			t.Errorf("process %s is still running", proc.Name)
		}
	}
}

func TestProcessOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping test on Windows")
	}

	logDir := t.TempDir()
	worktreePath := t.TempDir()
	logger := zap.NewNop()

	m := NewManager(logDir, logger)

	cfg := config.ProcessConfig{
		Name:    "echo-process",
		Command: "sleep 0.2 && echo 'hello world' && sleep 1",
	}

	// Start the process first
	err := m.Start(cfg, worktreePath)
	if err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	defer m.Stop("echo-process")

	// Subscribe after starting
	ch, unsubscribe := m.Subscribe("echo-process")
	defer unsubscribe()

	// Wait for output
	var output []byte
	timeout := time.After(3 * time.Second)
	select {
	case output = <-ch:
		// Got output
	case <-timeout:
		t.Fatal("timeout waiting for output")
	}

	if !strings.Contains(string(output), "hello world") {
		t.Errorf("expected output to contain 'hello world', got: %s", string(output))
	}
}

func TestSubscribeNonexistentProcess(t *testing.T) {
	logDir := t.TempDir()
	logger := zap.NewNop()

	m := NewManager(logDir, logger)

	ch, unsubscribe := m.Subscribe("nonexistent")
	defer unsubscribe()

	// Channel should be closed
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected closed channel for nonexistent process")
		}
	default:
		// This is also acceptable - the channel is closed
	}
}

func TestProcessWithEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping test on Windows")
	}

	logDir := t.TempDir()
	worktreePath := t.TempDir()
	logger := zap.NewNop()

	m := NewManager(logDir, logger)

	cfg := config.ProcessConfig{
		Name:    "env-process",
		Command: "sleep 0.2 && echo $TEST_VAR && sleep 1",
		Env: map[string]string{
			"TEST_VAR": "custom_value",
		},
	}

	// Start the process first
	err := m.Start(cfg, worktreePath)
	if err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	defer m.Stop("env-process")

	// Subscribe after starting
	ch, unsubscribe := m.Subscribe("env-process")
	defer unsubscribe()

	// Wait for output
	timeout := time.After(3 * time.Second)
	select {
	case output := <-ch:
		if !strings.Contains(string(output), "custom_value") {
			t.Errorf("expected output to contain 'custom_value', got: %s", string(output))
		}
	case <-timeout:
		t.Fatal("timeout waiting for output")
	}
}

func TestProcessWithCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping test on Windows")
	}

	logDir := t.TempDir()
	worktreePath := t.TempDir()
	customDir := filepath.Join(worktreePath, "subdir")
	os.MkdirAll(customDir, 0755)

	logger := zap.NewNop()

	m := NewManager(logDir, logger)

	cfg := config.ProcessConfig{
		Name:    "cwd-process",
		Command: "sleep 0.2 && pwd && sleep 1",
		Cwd:     "subdir",
	}

	// Start the process first
	err := m.Start(cfg, worktreePath)
	if err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	defer m.Stop("cwd-process")

	// Subscribe after starting
	ch, unsubscribe := m.Subscribe("cwd-process")
	defer unsubscribe()

	// Wait for output
	timeout := time.After(3 * time.Second)
	select {
	case output := <-ch:
		if !strings.Contains(string(output), "subdir") {
			t.Errorf("expected output to contain 'subdir', got: %s", string(output))
		}
	case <-timeout:
		t.Fatal("timeout waiting for output")
	}
}

func TestLogFileCreation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping test on Windows")
	}

	logDir := t.TempDir()
	worktreePath := t.TempDir()
	logger := zap.NewNop()

	m := NewManager(logDir, logger)

	cfg := config.ProcessConfig{
		Name:    "log-process",
		Command: "echo 'log test' && sleep 0.5",
	}

	err := m.Start(cfg, worktreePath)
	if err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	// Wait for process to finish and log to be written
	time.Sleep(1 * time.Second)

	logPath := filepath.Join(logDir, "log-process.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}

	if !strings.Contains(string(content), "log test") {
		t.Errorf("expected log file to contain 'log test', got: %s", string(content))
	}
}

func TestProcessStatusString(t *testing.T) {
	tests := []struct {
		status ProcessStatus
		want   string
	}{
		{StatusStopped, "stopped"},
		{StatusRunning, "running"},
		{StatusCrashed, "crashed"},
		{ProcessStatus(99), "unknown"},
	}

	for _, tt := range tests {
		got := tt.status.String()
		if got != tt.want {
			t.Errorf("ProcessStatus(%d).String() = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping test on Windows")
	}

	logDir := t.TempDir()
	worktreePath := t.TempDir()
	logger := zap.NewNop()

	m := NewManager(logDir, logger)

	// Start multiple processes concurrently
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cfg := config.ProcessConfig{
				Name:    "concurrent-" + string(rune('a'+idx)),
				Command: "sleep 60",
			}
			m.Start(cfg, worktreePath)
		}(i)
	}
	wg.Wait()

	defer m.StopAll()

	// List should work concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.List()
		}()
	}
	wg.Wait()

	list := m.List()
	if len(list) != 5 {
		t.Errorf("expected 5 processes, got %d", len(list))
	}
}
