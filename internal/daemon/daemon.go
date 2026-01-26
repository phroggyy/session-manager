package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/phroggyy/session-manager/internal/config"
	"github.com/phroggyy/session-manager/internal/ngrok"
	"github.com/phroggyy/session-manager/internal/process"
	"github.com/phroggyy/session-manager/internal/session"
	"github.com/phroggyy/session-manager/internal/worktree"
	"go.uber.org/zap"
)

// Daemon manages processes and coordinates worktree switching.
type Daemon struct {
	session       *session.Session
	config        *config.Config
	configPath    string
	templateCtx   config.TemplateContext
	procMgr       *process.Manager
	ngrokMgr      *ngrok.Manager
	routedSession string // Worktree path of session currently routed via ngrok
	server        *Server
	logger        *zap.Logger
	subscribers   []chan Event
	mu            sync.RWMutex
	running       bool
	stopCh        chan struct{}
}

// New creates a new Daemon instance.
// configPath is the path to the config file, used for resolving relative env_file paths.
// socketPath is the Unix socket path for the daemon to listen on; if empty, sess.GetSocketPath() is used.
func New(sess *session.Session, cfg *config.Config, configPath string, socketPath string, logger *zap.Logger) (*Daemon, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	// Create template context from session
	// Note: Name is kept for backwards compatibility with config templates using ${name}
	// but sessions are now identified by worktree, not name
	templateCtx := config.TemplateContext{
		Index: sess.GetIndex(),
		Name:  "", // Sessions no longer have names; use empty string
	}

	// Expand config templates
	expandedCfg, err := cfg.Expand(templateCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to expand config templates: %w", err)
	}

	procMgr := process.NewManager(sess.GetLogDir(), logger)

	// Port change callback will be set after daemon is created
	// (needs reference to daemon for broadcasting)

	// Create ngrok manager if ngrok config is present
	var ngrokMgr *ngrok.Manager
	if expandedCfg.Ngrok != nil && (expandedCfg.Ngrok.Port != "" || expandedCfg.Ngrok.Subdomain != "") {
		ngrokMgr = ngrok.NewManager(
			expandedCfg.Ngrok.AuthToken,
			expandedCfg.Ngrok.Region,
			expandedCfg.Ngrok.Subdomain,
			logger,
		)
	}

	d := &Daemon{
		session:     sess,
		config:      expandedCfg,
		configPath:  configPath,
		templateCtx: templateCtx,
		procMgr:     procMgr,
		ngrokMgr:    ngrokMgr,
		logger:      logger,
		subscribers: make([]chan Event, 0),
		stopCh:      make(chan struct{}),
	}

	// Create the server - use provided socketPath if set, otherwise fall back to session default
	actualSocketPath := socketPath
	if actualSocketPath == "" {
		actualSocketPath = sess.GetSocketPath()
	}
	d.server = NewServer(actualSocketPath, d, logger)

	// Set up port change callback to broadcast port changes to clients
	procMgr.SetPortChangeCallback(func(processName string, ports []uint32) {
		d.broadcast(Event{
			Type:    EventProcessStatus,
			Process: processName,
			Data: ProcessStatusData{
				Name:  processName,
				Ports: ports,
			},
		})
	})

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

	state := d.session.GetState()
	d.logger.Info("daemon started",
		zap.String("session_id", state.ID),
		zap.String("worktree", state.CurrentWorktree),
		zap.Int("session_index", state.Index),
		zap.String("socket", d.server.SocketPath()),
	)

	// Start all configured processes
	if state.CurrentWorktree != "" {
		if err := d.startAllProcesses(state.CurrentWorktree); err != nil {
			d.logger.Error("failed to start processes", zap.Error(err))
		}
	}

	// Note: ngrok tunnel is not started automatically.
	// Use 'sm route <session>' to start routing to a specific session.

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
	d.stopNgrokTunnels()
	d.procMgr.StopAll()
	d.server.Stop()

	return err
}

// Switch stops all processes, switches to a new worktree, and restarts processes.
// Returns the resolved worktree path on success.
func (d *Daemon) Switch(target string) (string, error) {
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
		return "", fmt.Errorf("failed to resolve worktree %q: %w", target, err)
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
		return "", fmt.Errorf("failed to update session worktree: %w", err)
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
		return "", fmt.Errorf("failed to start processes: %w", err)
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

	return wt.Path, nil
}

// Stop stops all processes and shuts down the daemon.
func (d *Daemon) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.logger.Info("stopping daemon")

	// Stop ngrok tunnels
	d.stopNgrokTunnels()

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
			Ports:     proc.Ports,
		})
	}

	// Get ngrok tunnel info
	var ngrokTunnels []NgrokTunnelStatus
	if d.ngrokMgr != nil && d.ngrokMgr.IsRunning() {
		ngrokTunnels = append(ngrokTunnels, NgrokTunnelStatus{
			Port:      d.ngrokMgr.GetCurrentPort(),
			PublicURL: d.ngrokMgr.GetPublicURL(),
			Subdomain: d.ngrokMgr.GetSubdomain(),
		})
	}

	return &StatusResponse{
		SessionID:       state.ID,
		SessionName:     "", // Sessions no longer have names; field kept for API compatibility
		SessionIndex:    state.Index,
		RepoPath:        state.RepoPath,
		CurrentWorktree: state.CurrentWorktree,
		CurrentBranch:   state.CurrentBranch,
		Processes:       processes,
		NgrokTunnels:    ngrokTunnels,
		RoutedSession:   d.routedSession,
		Running:         d.running,
	}
}

// broadcast sends an event to all subscribers.
func (d *Daemon) broadcast(event Event) {
	d.mu.RLock()
	subscribers := make([]chan Event, len(d.subscribers))
	copy(subscribers, d.subscribers)
	d.mu.RUnlock()

	for _, ch := range subscribers {
		d.safeSend(ch, event)
	}

	// Also broadcast via the server
	if d.server != nil {
		d.server.BroadcastEvent(event)
	}
}

// safeSend sends an event to a channel, recovering from panics if the channel is closed.
func (d *Daemon) safeSend(ch chan Event, event Event) {
	defer func() {
		if r := recover(); r != nil {
			d.logger.Debug("recovered from send on closed channel")
		}
	}()

	select {
	case ch <- event:
	default:
		// Drop if buffer is full
		d.logger.Debug("dropping event for slow subscriber")
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

	// Get base path for resolving relative env_file paths
	basePath := filepath.Dir(d.configPath)
	if basePath == "" {
		basePath = worktreePath
	}

	var errs []error
	for _, procCfg := range d.config.Processes {
		// Resolve environment variables from env_file and inline env
		env, err := d.config.GetEnv(procCfg, basePath)
		if err != nil {
			d.logger.Error("failed to load environment for process",
				zap.String("name", procCfg.Name),
				zap.Error(err),
			)
			errs = append(errs, fmt.Errorf("failed to load env for %s: %w", procCfg.Name, err))
			continue
		}

		if err := d.procMgr.Start(procCfg, worktreePath, env); err != nil {
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
					Ports:  proc.Ports,
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

// Route routes the ngrok tunnel to a specific session's port.
// This is only available on the default daemon.
func (d *Daemon) Route(sessionName string, port int) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.ngrokMgr == nil {
		return "", fmt.Errorf("ngrok is not configured")
	}

	d.logger.Info("routing ngrok tunnel",
		zap.String("session", sessionName),
		zap.Int("port", port),
	)

	// Reroute the tunnel
	publicURL, err := d.ngrokMgr.Reroute(port)
	if err != nil {
		return "", fmt.Errorf("failed to reroute ngrok: %w", err)
	}

	d.routedSession = sessionName

	// Update session state with tunnel info
	tunnelInfo := []session.NgrokTunnel{{
		Port:      port,
		PublicURL: publicURL,
		Subdomain: d.ngrokMgr.GetSubdomain(),
	}}
	if err := d.session.UpdateNgrokTunnels(tunnelInfo); err != nil {
		d.logger.Warn("failed to save ngrok tunnel info", zap.Error(err))
	}

	// Broadcast route event
	d.broadcast(Event{
		Type: EventRoute,
		Data: RouteData{
			SessionName: sessionName,
			Port:        port,
			PublicURL:   publicURL,
		},
	})

	d.logger.Info("ngrok tunnel routed",
		zap.String("session", sessionName),
		zap.Int("port", port),
		zap.String("public_url", publicURL),
	)

	return publicURL, nil
}

// stopNgrokTunnels stops the ngrok tunnel.
func (d *Daemon) stopNgrokTunnels() {
	if d.ngrokMgr == nil {
		return
	}

	if err := d.ngrokMgr.Stop(); err != nil {
		d.logger.Warn("error stopping ngrok tunnel", zap.Error(err))
	}

	d.routedSession = ""

	// Clear tunnel info from session state
	if err := d.session.UpdateNgrokTunnels(nil); err != nil {
		d.logger.Warn("failed to clear ngrok tunnel info", zap.Error(err))
	}
}
