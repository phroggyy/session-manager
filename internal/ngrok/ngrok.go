package ngrok

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// Tunnel represents an active ngrok tunnel.
type Tunnel struct {
	Port      int
	PublicURL string
	Subdomain string
	cmd       *exec.Cmd
}

// Manager manages a single ngrok tunnel that can be dynamically rerouted.
type Manager struct {
	authToken   string
	region      string
	subdomain   string
	domain      string // Full domain (e.g., "myapp.eu.ngrok.io")
	currentPort int
	tunnel      *Tunnel
	logger      *zap.Logger
	mu          sync.RWMutex
}

// NewManager creates a new ngrok manager.
// If authToken is empty, it will check NGROK_AUTH and NGROK_AUTHTOKEN env vars.
// The subdomain and region are combined to form the full domain: {subdomain}.{region}.ngrok.io
func NewManager(authToken, region, subdomain string, logger *zap.Logger) *Manager {
	// Fall back to environment variables if auth token not provided
	if authToken == "" {
		authToken = os.Getenv("NGROK_AUTH")
	}
	if authToken == "" {
		authToken = os.Getenv("NGROK_AUTHTOKEN")
	}

	// Construct full domain from subdomain and region
	var domain string
	if subdomain != "" && region != "" {
		domain = fmt.Sprintf("%s.%s.ngrok.io", subdomain, region)
	} else if subdomain != "" {
		// If no region, assume it might already be a full domain or use default region
		domain = fmt.Sprintf("%s.ngrok.io", subdomain)
	}

	return &Manager{
		authToken: authToken,
		region:    region,
		subdomain: subdomain,
		domain:    domain,
		logger:    logger,
	}
}

// Start starts the ngrok tunnel pointing to the given port.
// If a tunnel is already running, it will be stopped first.
func (m *Manager) Start(port int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop existing tunnel if running
	if m.tunnel != nil {
		m.stopTunnelLocked()
	}

	// Build ngrok command
	args := []string{"http", strconv.Itoa(port)}

	// Add subdomain if specified
	if m.subdomain != "" {
		args = append(args, "--subdomain", m.subdomain)
	}

	// Add auth token if specified
	if m.authToken != "" {
		args = append(args, "--authtoken", m.authToken)
	}

	// Add region if specified
	if m.region != "" {
		args = append(args, "--region", m.region)
	}

	args = append(args, "--log", "stdout", "--log-format", "json")

	m.logger.Debug("starting ngrok tunnel",
		zap.Int("port", port),
		zap.String("subdomain", m.subdomain),
		zap.Strings("args", args),
	)

	cmd := exec.Command("ngrok", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Discard output (ngrok logs to its own file)
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ngrok: %w", err)
	}

	m.tunnel = &Tunnel{
		Port:      port,
		Subdomain: m.subdomain,
		cmd:       cmd,
	}
	m.currentPort = port

	// Wait for ngrok to start and get public URL
	publicURL, err := m.waitForTunnel(port, 10*time.Second)
	if err != nil {
		m.logger.Warn("failed to get ngrok public URL, tunnel may still be starting",
			zap.Int("port", port),
			zap.Error(err),
		)
	} else {
		m.tunnel.PublicURL = publicURL
		m.logger.Info("ngrok tunnel started",
			zap.Int("port", port),
			zap.String("public_url", publicURL),
		)
	}

	return nil
}

// Reroute stops the current tunnel and starts a new one pointing to a different port.
// Returns the new public URL.
func (m *Manager) Reroute(newPort int) (string, error) {
	m.mu.Lock()

	// If same port, just return current URL
	if m.tunnel != nil && m.currentPort == newPort {
		url := m.tunnel.PublicURL
		m.mu.Unlock()
		return url, nil
	}

	m.mu.Unlock()

	// Start will stop existing tunnel first
	if err := m.Start(newPort); err != nil {
		return "", err
	}

	return m.GetPublicURL(), nil
}

// Stop stops the ngrok tunnel.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tunnel == nil {
		return nil
	}

	m.stopTunnelLocked()
	return nil
}

// stopTunnelLocked stops the tunnel (caller must hold mutex).
func (m *Manager) stopTunnelLocked() {
	if m.tunnel == nil {
		return
	}

	if m.tunnel.cmd != nil && m.tunnel.cmd.Process != nil {
		m.logger.Debug("stopping ngrok tunnel", zap.Int("port", m.currentPort))

		// Send SIGTERM to process group
		pgid, err := syscall.Getpgid(m.tunnel.cmd.Process.Pid)
		if err == nil {
			syscall.Kill(-pgid, syscall.SIGTERM)
		} else {
			m.tunnel.cmd.Process.Signal(os.Interrupt)
		}

		// Wait for process to exit (with timeout)
		done := make(chan error, 1)
		go func() {
			done <- m.tunnel.cmd.Wait()
		}()

		select {
		case <-done:
			// Process exited
		case <-time.After(5 * time.Second):
			// Force kill
			if pgid, err := syscall.Getpgid(m.tunnel.cmd.Process.Pid); err == nil {
				syscall.Kill(-pgid, syscall.SIGKILL)
			} else {
				m.tunnel.cmd.Process.Kill()
			}
		}
	}

	m.tunnel = nil
	m.currentPort = 0
}

// waitForTunnel waits for the ngrok tunnel to be ready and returns the public URL.
func (m *Manager) waitForTunnel(port int, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		url, err := m.getTunnelURL(port)
		if err == nil && url != "" {
			return url, nil
		}

		time.Sleep(500 * time.Millisecond)
	}

	return "", fmt.Errorf("timeout waiting for ngrok tunnel on port %d", port)
}

// getTunnelURL queries the ngrok API to get the public URL for a port.
func (m *Manager) getTunnelURL(port int) (string, error) {
	// ngrok's local API endpoint
	apiURL := "http://localhost:4040/api/tunnels"

	resp, err := http.Get(apiURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var tunnelsResp struct {
		Tunnels []struct {
			Name      string `json:"name"`
			PublicURL string `json:"public_url"`
			Config    struct {
				Addr string `json:"addr"`
			} `json:"config"`
		} `json:"tunnels"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tunnelsResp); err != nil {
		return "", err
	}

	// Find the tunnel for our port
	portStr := strconv.Itoa(port)
	for _, t := range tunnelsResp.Tunnels {
		// Check if the tunnel's addr contains our port
		if t.Config.Addr == fmt.Sprintf("http://localhost:%d", port) ||
			t.Config.Addr == fmt.Sprintf("localhost:%d", port) ||
			t.Config.Addr == portStr {
			return t.PublicURL, nil
		}
	}

	return "", fmt.Errorf("no tunnel found for port %d", port)
}

// GetPublicURL returns the current public URL of the tunnel.
func (m *Manager) GetPublicURL() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.tunnel != nil {
		return m.tunnel.PublicURL
	}
	return ""
}

// GetCurrentPort returns the port the tunnel is currently pointing to.
func (m *Manager) GetCurrentPort() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentPort
}

// IsRunning checks if the tunnel is currently running.
func (m *Manager) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.tunnel == nil || m.tunnel.cmd == nil || m.tunnel.cmd.Process == nil {
		return false
	}

	// Try to send signal 0 to check if process exists
	err := m.tunnel.cmd.Process.Signal(syscall.Signal(0))
	return err == nil
}

// GetSubdomain returns the configured subdomain.
func (m *Manager) GetSubdomain() string {
	return m.subdomain
}

// RefreshURL updates the public URL by querying the ngrok API.
func (m *Manager) RefreshURL() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tunnel == nil {
		return nil
	}

	url, err := m.getTunnelURL(m.currentPort)
	if err != nil {
		return err
	}

	m.tunnel.PublicURL = url
	return nil
}
