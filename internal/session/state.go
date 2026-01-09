package session

// ProcessState represents the state of a managed process.
type ProcessState struct {
	Name      string `json:"name"`
	PID       int    `json:"pid"`
	Status    string `json:"status"`
	StartedAt string `json:"started_at"`
}

// State represents the persistent state of a session.
type State struct {
	ID              string                  `json:"id"`
	RepoPath        string                  `json:"repo_path"`
	CurrentWorktree string                  `json:"current_worktree"`
	CurrentBranch   string                  `json:"current_branch"`
	ConfigPath      string                  `json:"config_path"`
	Processes       map[string]ProcessState `json:"processes"`
	StartedAt       string                  `json:"started_at"`
}
