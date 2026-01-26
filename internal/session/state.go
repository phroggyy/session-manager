package session

// ProcessState represents the state of a managed process.
type ProcessState struct {
	Name      string `json:"name"`
	PID       int    `json:"pid"`
	Status    string `json:"status"`
	StartedAt string `json:"started_at"`
}

// State represents the persistent state of a session.
// Sessions are identified by their worktree path (stored in CurrentWorktree).
type State struct {
	ID              string                  `json:"id"`
	Index           int                     `json:"index"`
	RepoPath        string                  `json:"repo_path"`
	CurrentWorktree string                  `json:"current_worktree"` // Primary identifier
	CurrentBranch   string                  `json:"current_branch"`
	ConfigPath      string                  `json:"config_path"`
	Processes       map[string]ProcessState `json:"processes"`
	NgrokTunnels    []NgrokTunnel           `json:"ngrok_tunnels,omitempty"`
	StartedAt       string                  `json:"started_at"`
}

// NgrokTunnel represents an active ngrok tunnel.
type NgrokTunnel struct {
	Port      int    `json:"port"`
	PublicURL string `json:"public_url"`
	Subdomain string `json:"subdomain,omitempty"`
}
