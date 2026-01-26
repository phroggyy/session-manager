package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryBasicOperations(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "registry-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a fake repo path
	repoPath := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		t.Fatalf("failed to create repo dir: %v", err)
	}

	// Load (create) registry
	registry, err := LoadRegistry(repoPath)
	if err != nil {
		t.Fatalf("LoadRegistry() error = %v", err)
	}

	if registry.NextIndex != 0 {
		t.Errorf("NextIndex = %d, want 0", registry.NextIndex)
	}

	// Add first session (worktree, branch)
	worktree1 := filepath.Join(tmpDir, "worktree1")
	if err := os.MkdirAll(worktree1, 0755); err != nil {
		t.Fatalf("failed to create worktree1 dir: %v", err)
	}
	entry1, err := registry.AddSession(worktree1, "feature/first")
	if err != nil {
		t.Fatalf("AddSession() error = %v", err)
	}

	if entry1.Index != 0 {
		t.Errorf("entry1.Index = %d, want 0", entry1.Index)
	}
	if entry1.Branch != "feature/first" {
		t.Errorf("entry1.Branch = %q, want %q", entry1.Branch, "feature/first")
	}
	if entry1.Worktree != worktree1 {
		t.Errorf("entry1.Worktree = %q, want %q", entry1.Worktree, worktree1)
	}

	// Add second session
	worktree2 := filepath.Join(tmpDir, "worktree2")
	if err := os.MkdirAll(worktree2, 0755); err != nil {
		t.Fatalf("failed to create worktree2 dir: %v", err)
	}
	entry2, err := registry.AddSession(worktree2, "feature/second")
	if err != nil {
		t.Fatalf("AddSession() error = %v", err)
	}

	if entry2.Index != 1 {
		t.Errorf("entry2.Index = %d, want 1", entry2.Index)
	}

	// Try to add duplicate session (same worktree)
	_, err = registry.AddSession(worktree1, "different-branch")
	if err == nil {
		t.Error("AddSession() expected error for duplicate worktree")
	}

	// Save registry
	if err := registry.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Reload registry
	registry2, err := LoadRegistry(repoPath)
	if err != nil {
		t.Fatalf("LoadRegistry() reload error = %v", err)
	}

	if registry2.NextIndex != 2 {
		t.Errorf("reloaded NextIndex = %d, want 2", registry2.NextIndex)
	}

	if len(registry2.Sessions) != 2 {
		t.Errorf("reloaded sessions count = %d, want 2", len(registry2.Sessions))
	}
}

func TestRegistryListSessions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "registry-list-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	repoPath := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		t.Fatalf("failed to create repo dir: %v", err)
	}

	registry, err := LoadRegistry(repoPath)
	if err != nil {
		t.Fatalf("LoadRegistry() error = %v", err)
	}

	// Add sessions - worktrees with different branches
	worktree1 := filepath.Join(tmpDir, "wt-charlie")
	worktree2 := filepath.Join(tmpDir, "wt-alpha")
	worktree3 := filepath.Join(tmpDir, "wt-bravo")
	os.MkdirAll(worktree1, 0755)
	os.MkdirAll(worktree2, 0755)
	os.MkdirAll(worktree3, 0755)

	registry.AddSession(worktree1, "charlie")
	registry.AddSession(worktree2, "alpha")
	registry.AddSession(worktree3, "bravo")

	sessions := registry.ListSessions()

	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}

	// Should be sorted by index (order added)
	expectedBranches := []string{"charlie", "alpha", "bravo"}
	for i, sess := range sessions {
		if sess.Branch != expectedBranches[i] {
			t.Errorf("session[%d].Branch = %q, want %q", i, sess.Branch, expectedBranches[i])
		}
		if sess.Index != i {
			t.Errorf("session[%d].Index = %d, want %d", i, sess.Index, i)
		}
	}
}

func TestRegistryRemoveSession(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "registry-remove-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	repoPath := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		t.Fatalf("failed to create repo dir: %v", err)
	}

	registry, err := LoadRegistry(repoPath)
	if err != nil {
		t.Fatalf("LoadRegistry() error = %v", err)
	}

	worktree := filepath.Join(tmpDir, "test-worktree")
	os.MkdirAll(worktree, 0755)
	registry.AddSession(worktree, "test-branch")

	// Verify session exists
	_, exists := registry.GetSession(worktree)
	if !exists {
		t.Error("session should exist before removal")
	}

	// Remove session
	if err := registry.RemoveSession(worktree); err != nil {
		t.Fatalf("RemoveSession() error = %v", err)
	}

	// Verify session is gone
	_, exists = registry.GetSession(worktree)
	if exists {
		t.Error("session should not exist after removal")
	}

	// Try to remove non-existent session
	err = registry.RemoveSession("/nonexistent/path")
	if err == nil {
		t.Error("RemoveSession() expected error for non-existent session")
	}
}

func TestRegistryFindByWorktree(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "registry-find-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	repoPath := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		t.Fatalf("failed to create repo dir: %v", err)
	}

	registry, err := LoadRegistry(repoPath)
	if err != nil {
		t.Fatalf("LoadRegistry() error = %v", err)
	}

	worktree1 := filepath.Join(tmpDir, "worktree1")
	worktree2 := filepath.Join(tmpDir, "worktree2")
	os.MkdirAll(worktree1, 0755)
	os.MkdirAll(worktree2, 0755)

	registry.AddSession(worktree1, "branch1")
	registry.AddSession(worktree2, "branch2")

	entry := registry.FindSessionByWorktree(worktree1)
	if entry == nil {
		t.Fatal("FindSessionByWorktree() entry should not be nil")
	}
	if entry.Branch != "branch1" {
		t.Errorf("FindSessionByWorktree() Branch = %q, want %q", entry.Branch, "branch1")
	}

	entry = registry.FindSessionByWorktree("/path/not/found")
	if entry != nil {
		t.Error("FindSessionByWorktree() should return nil for non-existent worktree")
	}
}

func TestRegistryFindByBranch(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "registry-find-branch-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	repoPath := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		t.Fatalf("failed to create repo dir: %v", err)
	}

	registry, err := LoadRegistry(repoPath)
	if err != nil {
		t.Fatalf("LoadRegistry() error = %v", err)
	}

	worktree1 := filepath.Join(tmpDir, "worktree1")
	worktree2 := filepath.Join(tmpDir, "worktree2")
	os.MkdirAll(worktree1, 0755)
	os.MkdirAll(worktree2, 0755)

	registry.AddSession(worktree1, "feature/auth")
	registry.AddSession(worktree2, "main")

	// Find by branch
	entry := registry.FindSessionByBranch("feature/auth")
	if entry == nil {
		t.Fatal("FindSessionByBranch() entry should not be nil")
	}
	if entry.Worktree != worktree1 {
		t.Errorf("FindSessionByBranch() Worktree = %q, want %q", entry.Worktree, worktree1)
	}

	// Find main branch
	entry = registry.FindSessionByBranch("main")
	if entry == nil {
		t.Fatal("FindSessionByBranch('main') entry should not be nil")
	}
	if entry.Worktree != worktree2 {
		t.Errorf("FindSessionByBranch('main') Worktree = %q, want %q", entry.Worktree, worktree2)
	}

	// Non-existent branch
	entry = registry.FindSessionByBranch("nonexistent")
	if entry != nil {
		t.Error("FindSessionByBranch() should return nil for non-existent branch")
	}
}

func TestRegistryUpdateBranch(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "registry-update-branch-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	repoPath := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		t.Fatalf("failed to create repo dir: %v", err)
	}

	registry, err := LoadRegistry(repoPath)
	if err != nil {
		t.Fatalf("LoadRegistry() error = %v", err)
	}

	worktree := filepath.Join(tmpDir, "worktree")
	os.MkdirAll(worktree, 0755)
	registry.AddSession(worktree, "old-branch")

	// Update branch
	err = registry.UpdateSessionBranch(worktree, "new-branch")
	if err != nil {
		t.Fatalf("UpdateSessionBranch() error = %v", err)
	}

	// Verify update
	entry, _ := registry.GetSession(worktree)
	if entry.Branch != "new-branch" {
		t.Errorf("Branch = %q, want %q", entry.Branch, "new-branch")
	}
}
