package process

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/phroggyy/session-manager/internal/config"
	"go.uber.org/zap"
)

const (
	// gracefulShutdownTimeout is the time to wait for SIGTERM before sending SIGKILL.
	gracefulShutdownTimeout = 5 * time.Second
)

// Manager handles lifecycle management of multiple processes.
type Manager struct {
	processes map[string]*Process
	logDir    string
	mu        sync.RWMutex
	logger    *zap.Logger
}

// NewManager creates a new process manager with the specified log directory.
func NewManager(logDir string, logger *zap.Logger) *Manager {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &Manager{
		processes: make(map[string]*Process),
		logDir:    logDir,
		logger:    logger,
	}
}

// Start starts a single process based on the provided configuration.
// The env parameter should contain the fully resolved environment variables
// (merged from env_file and inline env settings).
func (m *Manager) Start(cfg config.ProcessConfig, worktreePath string, env map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if process already exists and is running
	if existing, ok := m.processes[cfg.Name]; ok {
		if existing.Status == StatusRunning {
			return fmt.Errorf("process %q is already running", cfg.Name)
		}
	}

	// Determine working directory
	cwd := cfg.Cwd
	if cwd == "" {
		cwd = worktreePath
	} else if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(worktreePath, cwd)
	}

	// Create the process
	proc := newProcess(cfg.Name, cfg.Command, cwd, worktreePath, env)

	// Set up log file
	if err := os.MkdirAll(m.logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	logPath := filepath.Join(m.logDir, fmt.Sprintf("%s.log", cfg.Name))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	proc.logFile = logFile

	// Create the command
	cmd := exec.Command("sh", "-c", cfg.Command)
	cmd.Dir = cwd

	// Set up environment
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}

	// Set process group on Unix systems
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid: true,
		}
	}

	// Set up output capture
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logFile.Close()
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		logFile.Close()
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	proc.cmd = cmd

	// Start the process
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("failed to start process: %w", err)
	}

	proc.PID = cmd.Process.Pid
	proc.Status = StatusRunning
	proc.StartedAt = time.Now()

	m.logger.Info("process started",
		zap.String("name", cfg.Name),
		zap.Int("pid", proc.PID),
		zap.String("command", cfg.Command),
	)

	// Store the process
	m.processes[cfg.Name] = proc

	// Start goroutines to capture output
	go m.captureOutput(proc, stdout, "stdout")
	go m.captureOutput(proc, stderr, "stderr")

	// Monitor process exit
	go m.monitorProcess(proc)

	return nil
}

// captureOutput reads from a pipe and writes to both log file and broadcast channel.
func (m *Manager) captureOutput(proc *Process, pipe io.ReadCloser, source string) {
	scanner := bufio.NewScanner(pipe)
	// Increase buffer size for long lines
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		timestamp := time.Now().Format("2006-01-02T15:04:05.000Z07:00")
		logLine := fmt.Sprintf("[%s] [%s] %s\n", timestamp, source, string(line))

		// Write to log file
		if proc.logFile != nil {
			proc.logFile.WriteString(logLine)
		}

		// Broadcast to subscribers
		// Make a copy since scanner reuses the buffer
		lineCopy := make([]byte, len(line))
		copy(lineCopy, line)
		proc.broadcast(lineCopy)
	}

	if err := scanner.Err(); err != nil {
		m.logger.Debug("scanner error",
			zap.String("process", proc.Name),
			zap.String("source", source),
			zap.Error(err),
		)
	}
}

// monitorProcess watches for process exit and updates status accordingly.
func (m *Manager) monitorProcess(proc *Process) {
	if proc.cmd == nil || proc.cmd.Process == nil {
		return
	}

	err := proc.cmd.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Close done channel to signal any watchers
	select {
	case <-proc.done:
		// Already closed
	default:
		close(proc.done)
	}

	// Close log file
	if proc.logFile != nil {
		proc.logFile.Close()
		proc.logFile = nil
	}

	// Update status based on exit
	if err != nil {
		proc.Status = StatusCrashed
		m.logger.Warn("process crashed",
			zap.String("name", proc.Name),
			zap.Int("pid", proc.PID),
			zap.Error(err),
		)
	} else {
		proc.Status = StatusStopped
		m.logger.Info("process stopped",
			zap.String("name", proc.Name),
			zap.Int("pid", proc.PID),
		)
	}

	proc.PID = 0
}

// Stop stops a process by name, first sending SIGTERM and then SIGKILL after timeout.
func (m *Manager) Stop(name string) error {
	m.mu.Lock()
	proc, ok := m.processes[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("process %q not found", name)
	}

	if proc.Status != StatusRunning {
		m.mu.Unlock()
		return nil // Already stopped
	}

	if proc.cmd == nil || proc.cmd.Process == nil {
		m.mu.Unlock()
		return nil
	}

	pid := proc.PID
	done := proc.done
	m.mu.Unlock()

	m.logger.Info("stopping process",
		zap.String("name", name),
		zap.Int("pid", pid),
	)

	// Send SIGTERM to process group
	if err := m.signalProcessGroup(pid, syscall.SIGTERM); err != nil {
		m.logger.Debug("failed to send SIGTERM",
			zap.String("name", name),
			zap.Error(err),
		)
	}

	// Wait for process to exit or timeout
	select {
	case <-done:
		return nil
	case <-time.After(gracefulShutdownTimeout):
		m.logger.Warn("process did not exit gracefully, sending SIGKILL",
			zap.String("name", name),
			zap.Int("pid", pid),
		)

		// Send SIGKILL to process group
		if err := m.signalProcessGroup(pid, syscall.SIGKILL); err != nil {
			return fmt.Errorf("failed to kill process %q: %w", name, err)
		}

		// Wait a bit for the process to die
		select {
		case <-done:
			return nil
		case <-time.After(2 * time.Second):
			return fmt.Errorf("process %q did not exit after SIGKILL", name)
		}
	}
}

// signalProcessGroup sends a signal to the entire process group.
func (m *Manager) signalProcessGroup(pid int, sig syscall.Signal) error {
	if runtime.GOOS == "windows" {
		// Windows doesn't support process groups the same way
		return syscall.Kill(pid, sig)
	}
	// Send signal to the entire process group (negative PID)
	return syscall.Kill(-pid, sig)
}

// StopAll stops all managed processes.
func (m *Manager) StopAll() error {
	m.mu.RLock()
	names := make([]string, 0, len(m.processes))
	for name := range m.processes {
		names = append(names, name)
	}
	m.mu.RUnlock()

	var errs []error
	for _, name := range names {
		if err := m.Stop(name); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop %s: %w", name, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors stopping processes: %v", errs)
	}
	return nil
}

// Get returns a process by name, or nil if not found.
func (m *Manager) Get(name string) *Process {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.processes[name]
}

// List returns all managed processes.
func (m *Manager) List() []*Process {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Process, 0, len(m.processes))
	for _, proc := range m.processes {
		result = append(result, proc)
	}
	return result
}

// Subscribe subscribes to a process's output stream.
// Returns a channel that receives output and an unsubscribe function.
func (m *Manager) Subscribe(name string) (<-chan []byte, func()) {
	m.mu.RLock()
	proc, ok := m.processes[name]
	m.mu.RUnlock()

	if !ok {
		// Return closed channel for non-existent process
		ch := make(chan []byte)
		close(ch)
		return ch, func() {}
	}

	ch := make(chan []byte, 256)
	proc.addSubscriber(ch)

	unsubscribe := func() {
		proc.removeSubscriber(ch)
	}

	return ch, unsubscribe
}
