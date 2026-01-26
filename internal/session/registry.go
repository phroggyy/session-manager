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

// SessionEntry represents a registered session in the registry.
// Sessions are identified by their worktree path, with branch name for display/lookup.
type SessionEntry struct {
	Index     int       `json:"index"`
	Worktree  string    `json:"worktree"` // Primary identifier (absolute path)
	Branch    string    `json:"branch"`   // For display and branch-based lookup
	CreatedAt time.Time `json:"created_at"`
}

// Registry manages all sessions for a repository.
type Registry struct {
	RepoPath      string                  `json:"repo_path"`
	NextIndex     int                     `json:"next_index"`
	Sessions      map[string]SessionEntry `json:"sessions"`
	RoutedSession string                  `json:"routed_session,omitempty"` // Currently routed session for ngrok

	registryDir string
	mu          sync.RWMutex
}

// GetRegistryDir returns the registry directory path for a given repo path.
// This is the parent directory containing the registry.json and sessions/ subdirectory.
func GetRegistryDir(repoPath string) string {
	return GetSessionDir(repoPath)
}

// LoadRegistry loads or creates a registry for the given repository path.
func LoadRegistry(repoPath string) (*Registry, error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	registryDir := GetRegistryDir(absPath)
	registryFile := filepath.Join(registryDir, "registry.json")

	// Check if migration is needed (old structure exists)
	if err := migrateIfNeeded(registryDir); err != nil {
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	// Try to load existing registry
	data, err := os.ReadFile(registryFile)
	if err == nil {
		var registry Registry
		if err := json.Unmarshal(data, &registry); err != nil {
			return nil, fmt.Errorf("failed to parse registry file: %w", err)
		}
		registry.registryDir = registryDir
		if registry.Sessions == nil {
			registry.Sessions = make(map[string]SessionEntry)
		}
		return &registry, nil
	}

	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read registry file: %w", err)
	}

	// Create new registry
	if err := os.MkdirAll(registryDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create registry directory: %w", err)
	}

	registry := &Registry{
		RepoPath:    absPath,
		NextIndex:   0,
		Sessions:    make(map[string]SessionEntry),
		registryDir: registryDir,
	}

	if err := registry.Save(); err != nil {
		return nil, fmt.Errorf("failed to save new registry: %w", err)
	}

	return registry, nil
}

// registryData is a struct for JSON serialization without the mutex.
type registryData struct {
	RepoPath      string                  `json:"repo_path"`
	NextIndex     int                     `json:"next_index"`
	Sessions      map[string]SessionEntry `json:"sessions"`
	RoutedSession string                  `json:"routed_session,omitempty"`
}

// Save persists the registry to disk.
func (r *Registry) Save() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	registryFile := filepath.Join(r.registryDir, "registry.json")

	data, err := json.MarshalIndent(registryData{
		RepoPath:      r.RepoPath,
		NextIndex:     r.NextIndex,
		Sessions:      r.Sessions,
		RoutedSession: r.RoutedSession,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal registry: %w", err)
	}

	if err := os.WriteFile(registryFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write registry file: %w", err)
	}

	return nil
}

// AddSession adds a new session to the registry and returns the entry.
// The worktree path is the primary key. Branch is used for display and lookup.
// Returns an error if a session for the given worktree already exists.
func (r *Registry) AddSession(worktree, branch string) (*SessionEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if worktree == "" {
		return nil, fmt.Errorf("worktree path cannot be empty")
	}

	// Normalize worktree path
	absWorktree, err := filepath.Abs(worktree)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve worktree path: %w", err)
	}

	if _, exists := r.Sessions[absWorktree]; exists {
		return nil, fmt.Errorf("session for worktree %q already exists", absWorktree)
	}

	entry := SessionEntry{
		Index:     r.NextIndex,
		Worktree:  absWorktree,
		Branch:    branch,
		CreatedAt: time.Now().UTC(),
	}

	r.Sessions[absWorktree] = entry
	r.NextIndex++

	// Create session directory using hash of worktree path
	sessionDir := filepath.Join(r.registryDir, "sessions", worktreeHash(absWorktree))
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		// Rollback
		delete(r.Sessions, absWorktree)
		r.NextIndex--
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	// Create logs subdirectory
	logsDir := filepath.Join(sessionDir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		// Rollback
		delete(r.Sessions, absWorktree)
		r.NextIndex--
		os.RemoveAll(sessionDir)
		return nil, fmt.Errorf("failed to create logs directory: %w", err)
	}

	return &entry, nil
}

// worktreeHash returns a short hash for a worktree path (for directory naming).
func worktreeHash(worktree string) string {
	hash := md5.Sum([]byte(worktree))
	return hex.EncodeToString(hash[:])[:12]
}

// RemoveSession removes a session from the registry by worktree path.
// Returns an error if the session doesn't exist.
func (r *Registry) RemoveSession(worktree string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Normalize worktree path
	absWorktree, _ := filepath.Abs(worktree)

	if _, exists := r.Sessions[absWorktree]; !exists {
		return fmt.Errorf("session for worktree %q not found", absWorktree)
	}

	delete(r.Sessions, absWorktree)

	// Remove session directory
	sessionDir := filepath.Join(r.registryDir, "sessions", worktreeHash(absWorktree))
	if err := os.RemoveAll(sessionDir); err != nil {
		return fmt.Errorf("failed to remove session directory: %w", err)
	}

	return nil
}

// GetSession returns the session entry for the given worktree path.
// Returns nil and false if the session doesn't exist.
func (r *Registry) GetSession(worktree string) (*SessionEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Normalize worktree path
	absWorktree, _ := filepath.Abs(worktree)

	entry, exists := r.Sessions[absWorktree]
	if !exists {
		return nil, false
	}
	return &entry, true
}

// UpdateSessionBranch updates the branch name for a session.
// Returns an error if the session doesn't exist.
func (r *Registry) UpdateSessionBranch(worktree, branch string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Normalize worktree path
	absWorktree, _ := filepath.Abs(worktree)

	entry, exists := r.Sessions[absWorktree]
	if !exists {
		return fmt.Errorf("session for worktree %q not found", absWorktree)
	}

	entry.Branch = branch
	r.Sessions[absWorktree] = entry
	return nil
}

// SetRoutedSession sets the currently routed session for ngrok (by worktree path).
func (r *Registry) SetRoutedSession(worktree string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Normalize worktree path
	absWorktree, _ := filepath.Abs(worktree)
	r.RoutedSession = absWorktree
}

// GetRoutedSession returns the currently routed session's worktree path.
func (r *Registry) GetRoutedSession() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.RoutedSession
}

// ListSessions returns all session entries sorted by index.
func (r *Registry) ListSessions() []SessionEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entries := make([]SessionEntry, 0, len(r.Sessions))
	for _, entry := range r.Sessions {
		entries = append(entries, entry)
	}

	// Sort by index
	for i := 0; i < len(entries)-1; i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[i].Index > entries[j].Index {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	return entries
}

// GetSessionDir returns the directory path for a specific session.
func (r *Registry) GetSessionDir(worktree string) string {
	// Normalize worktree path
	absWorktree, _ := filepath.Abs(worktree)
	return filepath.Join(r.registryDir, "sessions", worktreeHash(absWorktree))
}

// GetSessionSocketPath returns the socket path for a specific session.
// Uses a short path in /tmp to avoid exceeding Unix socket path limits (104 chars on macOS).
func (r *Registry) GetSessionSocketPath(worktree string) string {
	// Normalize worktree path
	absWorktree, _ := filepath.Abs(worktree)

	entry, exists := r.Sessions[absWorktree]
	if !exists {
		// Fallback to temp path if session doesn't exist yet
		return filepath.Join(os.TempDir(), fmt.Sprintf("sm-%s.sock", worktreeHash(absWorktree)[:8]))
	}

	// Create short hash from repo path (first 8 chars of MD5)
	hash := md5.Sum([]byte(r.RepoPath))
	shortHash := hex.EncodeToString(hash[:])[:8]

	// Socket path: /tmp/sm-<8-char-hash>-<index>.sock
	// This keeps the path well under the 104 char limit
	return filepath.Join(os.TempDir(), fmt.Sprintf("sm-%s-%d.sock", shortHash, entry.Index))
}

// FindSessionByWorktree finds a session that matches the given worktree path.
// This is now just an alias for GetSession since worktree is the primary key.
func (r *Registry) FindSessionByWorktree(worktreePath string) *SessionEntry {
	entry, _ := r.GetSession(worktreePath)
	return entry
}

// FindSessionByBranch finds a session by branch name.
// Returns the first matching session entry, or nil if not found.
func (r *Registry) FindSessionByBranch(branch string) *SessionEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, entry := range r.Sessions {
		if entry.Branch == branch {
			entryCopy := entry
			return &entryCopy
		}
	}

	return nil
}

// migrateIfNeeded handles migration from old session structures.
// With the new worktree-based identification, we simply skip migration and start fresh.
func migrateIfNeeded(registryDir string) error {
	// No migration needed - users should start fresh with `rm -rf ~/.sm`
	return nil
}
