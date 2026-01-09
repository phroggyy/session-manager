package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/leosjoberg/session-manager/internal/config"
	"github.com/leosjoberg/session-manager/internal/process"
	"github.com/leosjoberg/session-manager/internal/session"
	"github.com/leosjoberg/session-manager/internal/worktree"
	"go.uber.org/zap"
)

// Daemon manages processes and coordinates worktree switching.
type Daemon struct {
	session     *session.Session
	config      *config.Config
	configPath  string
	procMgr     *process.Manager
	server      *Server
	logger      *zap.Logger
	subscribers []chan Event
	mu          sync.RWMutex
	running     bool
	stopCh      chan struct{}
}

// New creates a new Daemon instance.
func New(sess *session.Session, cfg *config.Config, logger *zap.Logger) (*Daemon, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	procMgr := process.NewManager(sess.GetLogDir(), logger)

	d := &Daemon{
		session:     sess,
		config:      cfg,
		procMgr:     procMgr,
		logger:      logger,
		subscribers: make([]chan Event, 0),
		stopCh:      make(chan struct{}),
	}

	// Create the server
	d.server = NewServer(sess.GetSocketPath(), d, logger)

	return d, nil
}

// Run starts the daemon and blocks until stopped.
func (d *Daemon) Run(ctx context.Context) error {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return fmt.Errorf("daemon is already running")
	}
	d.running = true
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		d.running = false
		d.mu.Unlock()
	}()

	// Start the server
	if err := d.server.Start(); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	d.logger.Info("daemon started",
		zap.String("session_id", d.session.GetState().ID),
		zap.String("socket", d.server.SocketPath()),
	)

	// Start all configured processes
	state := d.session.GetState()
	if state.CurrentWorktree != "" {
		if err := d.startAllProcesses(state.CurrentWorktree); err != nil {
			d.logger.Error("failed to start processes", zap.Error(err))
		}
	}

	// Create a context that cancels on stop
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Watch for stop signal
	go func() {
		select {
		case <-d.stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	// Serve requests
	err := d.server.Serve(runCtx)

	// Clean up
	d.procMgr.StopAll()
	d.server.Stop()

	return err
}

// Switch stops all processes, switches to a new worktree, and restarts processes.
func (d *Daemon) Switch(target string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	state := d.session.GetState()
	oldWorktree := state.CurrentWorktree
	oldBranch := state.CurrentBranch

	// Resolve the target worktree
	wt, err := worktree.Resolve(state.RepoPath, target)
	if err != nil {
		d.broadcast(Event{
			Type: EventError,
			Data: fmt.Sprintf("failed to resolve worktree: %v", err),
		})
		return fmt.Errorf("failed to resolve worktree %q: %w", target, err)
	}

	d.logger.Info("switching worktree",
		zap.String("from", oldWorktree),
		zap.String("to", wt.Path),
		zap.String("branch", wt.Branch),
	)

	// Broadcast switching event
	d.broadcast(Event{
		Type: EventSwitch,
		Data: SwitchData{
			OldWorktree: oldWorktree,
			NewWorktree: wt.Path,
			OldBranch:   oldBranch,
			NewBranch:   wt.Branch,
		},
	})

	// Stop all processes
	if err := d.procMgr.StopAll(); err != nil {
		d.logger.Warn("error stopping processes", zap.Error(err))
	}

	// Update session worktree
	if err := d.session.UpdateWorktree(wt.Path, wt.Branch); err != nil {
		d.broadcast(Event{
			Type: EventError,
			Data: fmt.Sprintf("failed to update session: %v", err),
		})
		return fmt.Errorf("failed to update session worktree: %w", err)
	}

	// Re-read config from new worktree (in case it differs)
	newConfig, err := d.loadConfigFromWorktree(wt.Path)
	if err != nil {
		d.logger.Warn("failed to load config from new worktree, using existing config",
			zap.Error(err),
		)
	} else {
		d.config = newConfig
	}

	// Start all processes in new worktree
	if err := d.startAllProcesses(wt.Path); err != nil {
		d.broadcast(Event{
			Type: EventError,
			Data: fmt.Sprintf("failed to start processes: %v", err),
		})
		return fmt.Errorf("failed to start processes: %w", err)
	}

	// Broadcast switched event
	d.broadcast(Event{
		Type: EventSwitch,
		Data: SwitchData{
			OldWorktree: oldWorktree,
			NewWorktree: wt.Path,
			OldBranch:   oldBranch,
			NewBranch:   wt.Branch,
		},
	})

	d.logger.Info("switched worktree successfully",
		zap.String("path", wt.Path),
		zap.String("branch", wt.Branch),
	)

	return nil
}

// Stop stops all processes and shuts down the daemon.
func (d *Daemon) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.logger.Info("stopping daemon")

	// Stop all processes
	if err := d.procMgr.StopAll(); err != nil {
		d.logger.Warn("error stopping processes", zap.Error(err))
	}

	// Close all subscriber channels
	for _, ch := range d.subscribers {
		close(ch)
	}
	d.subscribers = nil

	// Signal the run loop to stop
	select {
	case <-d.stopCh:
		// Already closed
	default:
		close(d.stopCh)
	}

	return nil
}

// Status returns the current status of the daemon.
func (d *Daemon) Status() *StatusResponse {
	d.mu.RLock()
	defer d.mu.RUnlock()

	state := d.session.GetState()

	processes := make([]ProcessStatus, 0)
	for _, proc := range d.procMgr.List() {
		processes = append(processes, ProcessStatus{
			Name:      proc.Name,
			Status:    proc.Status.String(),
			PID:       proc.PID,
			StartedAt: proc.StartedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	return &StatusResponse{
		SessionID:       state.ID,
		RepoPath:        state.RepoPath,
		CurrentWorktree: state.CurrentWorktree,
		CurrentBranch:   state.CurrentBranch,
		Processes:       processes,
		Running:         d.running,
	}
}

// broadcast sends an event to all subscribers.
func (d *Daemon) broadcast(event Event) {
	for _, ch := range d.subscribers {
		select {
		case ch <- event:
		default:
			// Drop if buffer is full
			d.logger.Debug("dropping event for slow subscriber")
		}
	}

	// Also broadcast via the server
	if d.server != nil {
		d.server.BroadcastEvent(event)
	}
}

// addSubscriber adds a new event subscriber.
func (d *Daemon) addSubscriber(ch chan Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.subscribers = append(d.subscribers, ch)
}

// removeSubscriber removes an event subscriber.
func (d *Daemon) removeSubscriber(ch chan Event) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for i, sub := range d.subscribers {
		if sub == ch {
			d.subscribers = append(d.subscribers[:i], d.subscribers[i+1:]...)
			break
		}
	}
}

// startAllProcesses starts all configured processes in the given worktree.
func (d *Daemon) startAllProcesses(worktreePath string) error {
	if d.config == nil {
		return fmt.Errorf("no configuration loaded")
	}

	var errs []error
	for _, procCfg := range d.config.Processes {
		if err := d.procMgr.Start(procCfg, worktreePath); err != nil {
			d.logger.Error("failed to start process",
				zap.String("name", procCfg.Name),
				zap.Error(err),
			)
			errs = append(errs, fmt.Errorf("failed to start %s: %w", procCfg.Name, err))
			continue
		}

		// Broadcast process status
		proc := d.procMgr.Get(procCfg.Name)
		if proc != nil {
			d.broadcast(Event{
				Type:    EventProcessStatus,
				Process: procCfg.Name,
				Data: ProcessStatusData{
					Name:   proc.Name,
					Status: proc.Status.String(),
					PID:    proc.PID,
				},
			})

			// Subscribe to process output and forward to clients
			outputCh, unsubscribe := d.procMgr.Subscribe(procCfg.Name)
			if outputCh != nil {
				go d.forwardProcessOutput(procCfg.Name, outputCh, unsubscribe)
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("some processes failed to start: %v", errs)
	}

	return nil
}

// forwardProcessOutput forwards process output to TUI clients as events.
func (d *Daemon) forwardProcessOutput(processName string, outputCh <-chan []byte, unsubscribe func()) {
	defer unsubscribe()

	for data := range outputCh {
		d.broadcast(Event{
			Type:    EventOutput,
			Process: processName,
			Data:    string(data),
		})
	}
}

// loadConfigFromWorktree attempts to load a config file from the given worktree path.
func (d *Daemon) loadConfigFromWorktree(worktreePath string) (*config.Config, error) {
	// Try common config file names
	configNames := []string{"sm.yaml", "sm.yml", "sm.json"}

	for _, name := range configNames {
		configPath := filepath.Join(worktreePath, name)
		cfg, err := config.Load(configPath)
		if err == nil {
			d.configPath = configPath
			return cfg, nil
		}
	}

	return nil, fmt.Errorf("no config file found in worktree")
}

// GetSession returns the current session.
func (d *Daemon) GetSession() *session.Session {
	return d.session
}

// GetConfig returns the current configuration.
func (d *Daemon) GetConfig() *config.Config {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.config
}

// GetProcessManager returns the process manager.
func (d *Daemon) GetProcessManager() *process.Manager {
	return d.procMgr
}
