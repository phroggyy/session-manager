package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParseWorktreeList(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []Worktree
	}{
		{
			name: "single worktree",
			input: `worktree /path/to/main
HEAD abc123def456
branch refs/heads/main
`,
			expected: []Worktree{
				{
					Path:   "/path/to/main",
					Branch: "main",
					HEAD:   "abc123def456",
					IsBare: false,
				},
			},
		},
		{
			name: "multiple worktrees",
			input: `worktree /path/to/main
HEAD abc123def456
branch refs/heads/main

worktree /path/to/feature
HEAD def456abc123
branch refs/heads/feature-branch
`,
			expected: []Worktree{
				{
					Path:   "/path/to/main",
					Branch: "main",
					HEAD:   "abc123def456",
					IsBare: false,
				},
				{
					Path:   "/path/to/feature",
					Branch: "feature-branch",
					HEAD:   "def456abc123",
					IsBare: false,
				},
			},
		},
		{
			name: "bare worktree",
			input: `worktree /path/to/bare
bare
`,
			expected: []Worktree{
				{
					Path:   "/path/to/bare",
					Branch: "",
					HEAD:   "",
					IsBare: true,
				},
			},
		},
		{
			name: "detached head",
			input: `worktree /path/to/detached
HEAD abc123def456
detached
`,
			expected: []Worktree{
				{
					Path:   "/path/to/detached",
					Branch: "",
					HEAD:   "abc123def456",
					IsBare: false,
				},
			},
		},
		{
			name: "no trailing newline",
			input: `worktree /path/to/main
HEAD abc123
branch refs/heads/main`,
			expected: []Worktree{
				{
					Path:   "/path/to/main",
					Branch: "main",
					HEAD:   "abc123",
					IsBare: false,
				},
			},
		},
		{
			name:     "empty input",
			input:    "",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseWorktreeList([]byte(tt.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d worktrees, got %d", len(tt.expected), len(result))
			}

			for i, wt := range result {
				if wt.Path != tt.expected[i].Path {
					t.Errorf("worktree[%d].Path = %q, expected %q", i, wt.Path, tt.expected[i].Path)
				}
				if wt.Branch != tt.expected[i].Branch {
					t.Errorf("worktree[%d].Branch = %q, expected %q", i, wt.Branch, tt.expected[i].Branch)
				}
				if wt.HEAD != tt.expected[i].HEAD {
					t.Errorf("worktree[%d].HEAD = %q, expected %q", i, wt.HEAD, tt.expected[i].HEAD)
				}
				if wt.IsBare != tt.expected[i].IsBare {
					t.Errorf("worktree[%d].IsBare = %v, expected %v", i, wt.IsBare, tt.expected[i].IsBare)
				}
			}
		})
	}
}

func TestList(t *testing.T) {
	// Set up mock executor
	mockOutput := `worktree /repo/main
HEAD abc123
branch refs/heads/main

worktree /repo/feature
HEAD def456
branch refs/heads/feature
`
	SetExecutor(func(dir string, args ...string) ([]byte, error) {
		if len(args) >= 3 && args[0] == "worktree" && args[1] == "list" && args[2] == "--porcelain" {
			return []byte(mockOutput), nil
		}
		return nil, errors.New("unexpected command")
	})
	defer ResetExecutor()

	worktrees, err := List("/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(worktrees) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(worktrees))
	}

	if worktrees[0].Branch != "main" {
		t.Errorf("expected first worktree branch to be 'main', got %q", worktrees[0].Branch)
	}

	if worktrees[1].Branch != "feature" {
		t.Errorf("expected second worktree branch to be 'feature', got %q", worktrees[1].Branch)
	}
}

func TestListError(t *testing.T) {
	SetExecutor(func(dir string, args ...string) ([]byte, error) {
		return nil, errors.New("git command failed")
	})
	defer ResetExecutor()

	_, err := List("/repo")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestResolve(t *testing.T) {
	mockOutput := `worktree /repo/main
HEAD abc123
branch refs/heads/main

worktree /repo/feature
HEAD def456
branch refs/heads/feature
`
	SetExecutor(func(dir string, args ...string) ([]byte, error) {
		if len(args) >= 3 && args[0] == "worktree" && args[1] == "list" && args[2] == "--porcelain" {
			return []byte(mockOutput), nil
		}
		return nil, errors.New("unexpected command")
	})
	defer ResetExecutor()

	t.Run("resolve by branch name", func(t *testing.T) {
		wt, err := Resolve("/repo", "feature")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if wt.Branch != "feature" {
			t.Errorf("expected branch 'feature', got %q", wt.Branch)
		}
		if wt.Path != "/repo/feature" {
			t.Errorf("expected path '/repo/feature', got %q", wt.Path)
		}
	})

	t.Run("resolve by path", func(t *testing.T) {
		wt, err := Resolve("/repo", "/repo/main")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if wt.Branch != "main" {
			t.Errorf("expected branch 'main', got %q", wt.Branch)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := Resolve("/repo", "nonexistent")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}

func TestFindRepoRoot(t *testing.T) {
	t.Run("valid repo", func(t *testing.T) {
		SetExecutor(func(dir string, args ...string) ([]byte, error) {
			if len(args) >= 2 && args[0] == "rev-parse" && args[1] == "--show-toplevel" {
				return []byte("/path/to/repo\n"), nil
			}
			return nil, errors.New("unexpected command")
		})
		defer ResetExecutor()

		root, err := FindRepoRoot("/path/to/repo/subdir")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if root != "/path/to/repo" {
			t.Errorf("expected '/path/to/repo', got %q", root)
		}
	})

	t.Run("not in git repo", func(t *testing.T) {
		SetExecutor(func(dir string, args ...string) ([]byte, error) {
			return nil, errors.New("fatal: not a git repository")
		})
		defer ResetExecutor()

		_, err := FindRepoRoot("/not/a/repo")
		if !errors.Is(err, ErrNotInGitRepo) {
			t.Errorf("expected ErrNotInGitRepo, got %v", err)
		}
	})
}

func TestGetMainWorktree(t *testing.T) {
	mockOutput := `worktree /repo/main
HEAD abc123
branch refs/heads/main

worktree /repo/feature
HEAD def456
branch refs/heads/feature
`
	SetExecutor(func(dir string, args ...string) ([]byte, error) {
		if len(args) >= 3 && args[0] == "worktree" && args[1] == "list" && args[2] == "--porcelain" {
			return []byte(mockOutput), nil
		}
		return nil, errors.New("unexpected command")
	})
	defer ResetExecutor()

	wt, err := GetMainWorktree("/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if wt.Path != "/repo/main" {
		t.Errorf("expected path '/repo/main', got %q", wt.Path)
	}

	if wt.Branch != "main" {
		t.Errorf("expected branch 'main', got %q", wt.Branch)
	}
}

func TestGetMainWorktreeEmpty(t *testing.T) {
	SetExecutor(func(dir string, args ...string) ([]byte, error) {
		return []byte(""), nil
	})
	defer ResetExecutor()

	_, err := GetMainWorktree("/repo")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCurrent(t *testing.T) {
	// Get a temporary directory that exists
	tmpDir := t.TempDir()

	// Change to the temp directory for this test
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change directory: %v", err)
	}

	// Get the absolute path (resolving symlinks on macOS)
	tmpDirAbs, err := filepath.EvalSymlinks(tmpDir)
	if err != nil {
		tmpDirAbs = tmpDir
	}

	mockOutput := "worktree " + tmpDirAbs + `
HEAD abc123
branch refs/heads/main
`
	SetExecutor(func(dir string, args ...string) ([]byte, error) {
		if len(args) >= 2 && args[0] == "rev-parse" && args[1] == "--show-toplevel" {
			return []byte(tmpDirAbs + "\n"), nil
		}
		if len(args) >= 3 && args[0] == "worktree" && args[1] == "list" && args[2] == "--porcelain" {
			return []byte(mockOutput), nil
		}
		return nil, errors.New("unexpected command")
	})
	defer ResetExecutor()

	wt, err := Current()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if wt.Branch != "main" {
		t.Errorf("expected branch 'main', got %q", wt.Branch)
	}
}

func TestCurrentNotInRepo(t *testing.T) {
	SetExecutor(func(dir string, args ...string) ([]byte, error) {
		return nil, errors.New("fatal: not a git repository")
	})
	defer ResetExecutor()

	_, err := Current()
	if !errors.Is(err, ErrNotInGitRepo) {
		t.Errorf("expected ErrNotInGitRepo, got %v", err)
	}
}

// Integration test - only runs if in an actual git repository
func TestIntegration(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION_TESTS") == "" {
		t.Skip("Skipping integration test; set RUN_INTEGRATION_TESTS=1 to run")
	}

	// Reset to default executor for integration tests
	ResetExecutor()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}

	root, err := FindRepoRoot(cwd)
	if err != nil {
		t.Skipf("Not in a git repository: %v", err)
	}

	t.Logf("Repository root: %s", root)

	worktrees, err := List(root)
	if err != nil {
		t.Fatalf("failed to list worktrees: %v", err)
	}

	t.Logf("Found %d worktree(s)", len(worktrees))
	for _, wt := range worktrees {
		t.Logf("  Path: %s, Branch: %s, HEAD: %s, Bare: %v", wt.Path, wt.Branch, wt.HEAD, wt.IsBare)
	}

	mainWt, err := GetMainWorktree(root)
	if err != nil {
		t.Fatalf("failed to get main worktree: %v", err)
	}
	t.Logf("Main worktree: %s (%s)", mainWt.Path, mainWt.Branch)
}
