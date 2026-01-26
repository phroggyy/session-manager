package session

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Session manages a session's state and directories.
type Session struct {
	state    *State
	worktree string
	index    int
	stateDir string // ~/.sm/sessions/<repo-hash>/sessions/<worktree-hash>/
	mu       sync.RWMutex
}

// GetSessionDir returns the session directory path for a given repo path.
// The directory is located at ~/.sm/sessions/<md5-hash-of-repo-path>/
func GetSessionDir(repoPath string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = os.TempDir()
	}

	hash := md5.Sum([]byte(repoPath))
	hashStr := hex.EncodeToString(hash[:])

	return filepath.Join(homeDir, ".sm", "sessions", hashStr)
}

// NewSessionForWorktree creates a new session for the given worktree.
func NewSessionForWorktree(repoPath, worktreePath string, index int) (*Session, error) {
	absRepoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute repo path: %w", err)
	}

	absWorktree, err := filepath.Abs(worktreePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute worktree path: %w", err)
	}

	sessionDir := GetWorktreeSessionDir(absRepoPath, absWorktree)

	// Create session directory
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	// Create logs directory
	logsDir := filepath.Join(sessionDir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Generate session ID from hash of worktree path
	hash := md5.Sum([]byte(absWorktree))
	sessionID := hex.EncodeToString(hash[:])[:12]

	state := &State{
		ID:              sessionID,
		Index:           index,
		RepoPath:        absRepoPath,
		CurrentWorktree: absWorktree,
		Processes:       make(map[string]ProcessState),
		StartedAt:       time.Now().UTC().Format(time.RFC3339),
	}

	s := &Session{
		state:    state,
		worktree: absWorktree,
		index:    index,
		stateDir: sessionDir,
	}

	// Save initial state
	if err := s.Save(); err != nil {
		return nil, fmt.Errorf("failed to save initial state: %w", err)
	}

	return s, nil
}

// GetWorktreeSessionDir returns the session directory path for a worktree-based session.
// The directory is located at ~/.sm/sessions/<md5-hash-of-repo-path>/sessions/<worktree-hash>/
func GetWorktreeSessionDir(repoPath, worktreePath string) string {
	baseDir := GetSessionDir(repoPath)
	hash := md5.Sum([]byte(worktreePath))
	worktreeHash := hex.EncodeToString(hash[:])[:12]
	return filepath.Join(baseDir, "sessions", worktreeHash)
}

// LoadSessionForWorktree loads an existing session for a worktree from disk.
func LoadSessionForWorktree(repoPath, worktreePath string) (*Session, error) {
	absRepoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute repo path: %w", err)
	}

	absWorktree, err := filepath.Abs(worktreePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute worktree path: %w", err)
	}

	sessionDir := GetWorktreeSessionDir(absRepoPath, absWorktree)
	stateFile := filepath.Join(sessionDir, "state.json")

	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}

	// Initialize Processes map if nil
	if state.Processes == nil {
		state.Processes = make(map[string]ProcessState)
	}

	return &Session{
		state:    &state,
		worktree: absWorktree,
		index:    state.Index,
		stateDir: sessionDir,
	}, nil
}

// Save persists the session state to state.json.
func (s *Session) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stateFile := filepath.Join(s.stateDir, "state.json")

	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(stateFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}

// GetState returns a copy of the current state.
func (s *Session) GetState() *State {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Create a deep copy
	stateCopy := &State{
		ID:              s.state.ID,
		Index:           s.state.Index,
		RepoPath:        s.state.RepoPath,
		CurrentWorktree: s.state.CurrentWorktree,
		CurrentBranch:   s.state.CurrentBranch,
		ConfigPath:      s.state.ConfigPath,
		Processes:       make(map[string]ProcessState),
		StartedAt:       s.state.StartedAt,
	}

	for k, v := range s.state.Processes {
		stateCopy.Processes[k] = v
	}

	// Copy ngrok tunnels if present
	if len(s.state.NgrokTunnels) > 0 {
		stateCopy.NgrokTunnels = make([]NgrokTunnel, len(s.state.NgrokTunnels))
		copy(stateCopy.NgrokTunnels, s.state.NgrokTunnels)
	}

	return stateCopy
}

// GetWorktree returns the session's worktree path.
func (s *Session) GetWorktree() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.worktree
}

// GetIndex returns the session index.
func (s *Session) GetIndex() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.index
}

// UpdateWorktree updates the current worktree path and branch.
func (s *Session) UpdateWorktree(worktreePath, branch string) error {
	s.mu.Lock()
	s.state.CurrentWorktree = worktreePath
	s.state.CurrentBranch = branch
	s.mu.Unlock()

	return s.Save()
}

// UpdateProcess updates or adds a process state.
func (s *Session) UpdateProcess(name string, state ProcessState) error {
	s.mu.Lock()
	if s.state.Processes == nil {
		s.state.Processes = make(map[string]ProcessState)
	}
	s.state.Processes[name] = state
	s.mu.Unlock()

	return s.Save()
}

// UpdateNgrokTunnels updates the list of active ngrok tunnels.
func (s *Session) UpdateNgrokTunnels(tunnels []NgrokTunnel) error {
	s.mu.Lock()
	s.state.NgrokTunnels = tunnels
	s.mu.Unlock()

	return s.Save()
}

// GetSocketPath returns the Unix socket path for the daemon.
func (s *Session) GetSocketPath() string {
	return filepath.Join(s.stateDir, "sm.sock")
}

// GetLogDir returns the log directory path.
func (s *Session) GetLogDir() string {
	return filepath.Join(s.stateDir, "logs")
}

// Cleanup removes the session directory and all its contents.
func (s *Session) Cleanup() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.RemoveAll(s.stateDir); err != nil {
		return fmt.Errorf("failed to remove session directory: %w", err)
	}

	return nil
}
