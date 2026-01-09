// Package worktree provides utilities for working with git worktrees.
package worktree

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Worktree represents a git worktree.
type Worktree struct {
	Path   string
	Branch string
	HEAD   string
	IsBare bool
}

var (
	// ErrNotFound is returned when a worktree cannot be found.
	ErrNotFound = errors.New("worktree not found")
	// ErrNotInGitRepo is returned when the current directory is not in a git repository.
	ErrNotInGitRepo = errors.New("not in a git repository")
)

// gitExecutor is a function type for executing git commands.
// This allows for mocking in tests.
type gitExecutor func(dir string, args ...string) ([]byte, error)

// defaultGitExecutor runs git commands using os/exec.
var defaultGitExecutor gitExecutor = func(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.Output()
}

// executor is the current git executor, can be replaced for testing.
var executor = defaultGitExecutor

// List returns all worktrees for a given repository.
// It parses the output of `git worktree list --porcelain`.
func List(repoPath string) ([]Worktree, error) {
	output, err := executor(repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("failed to list worktrees: %w", err)
	}

	return parseWorktreeList(output)
}

// parseWorktreeList parses the porcelain output of `git worktree list`.
// The format is:
//
//	worktree /path/to/main
//	HEAD abc123...
//	branch refs/heads/main
//
//	worktree /path/to/feature
//	HEAD def456...
//	branch refs/heads/feature
func parseWorktreeList(output []byte) ([]Worktree, error) {
	var worktrees []Worktree
	var current *Worktree

	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			if current != nil {
				worktrees = append(worktrees, *current)
				current = nil
			}
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 1 {
			continue
		}

		key := parts[0]
		value := ""
		if len(parts) > 1 {
			value = parts[1]
		}

		switch key {
		case "worktree":
			current = &Worktree{Path: value}
		case "HEAD":
			if current != nil {
				current.HEAD = value
			}
		case "branch":
			if current != nil {
				// Extract branch name from refs/heads/...
				current.Branch = strings.TrimPrefix(value, "refs/heads/")
			}
		case "bare":
			if current != nil {
				current.IsBare = true
			}
		case "detached":
			// HEAD is detached, branch will remain empty
		}
	}

	// Don't forget the last worktree if there's no trailing newline
	if current != nil {
		worktrees = append(worktrees, *current)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error parsing worktree list: %w", err)
	}

	return worktrees, nil
}

// Resolve finds a worktree by branch name or path.
// It first tries to match by branch name, then by path.
func Resolve(repoPath, target string) (*Worktree, error) {
	worktrees, err := List(repoPath)
	if err != nil {
		return nil, err
	}

	// Clean up the target path if it looks like a path
	targetPath := target
	if filepath.IsAbs(target) || strings.Contains(target, string(filepath.Separator)) {
		targetPath, _ = filepath.Abs(target)
	}

	// First, try to match by branch name
	for i := range worktrees {
		if worktrees[i].Branch == target {
			return &worktrees[i], nil
		}
	}

	// Then, try to match by path
	for i := range worktrees {
		// Compare cleaned absolute paths
		wtPath, err := filepath.Abs(worktrees[i].Path)
		if err != nil {
			continue
		}
		if wtPath == targetPath || worktrees[i].Path == target {
			return &worktrees[i], nil
		}
	}

	return nil, ErrNotFound
}

// Current returns the worktree for the current working directory.
func Current() (*Worktree, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	repoRoot, err := FindRepoRoot(cwd)
	if err != nil {
		return nil, err
	}

	worktrees, err := List(repoRoot)
	if err != nil {
		return nil, err
	}

	// Find the worktree that contains the current directory
	for i := range worktrees {
		wtPath, err := filepath.Abs(worktrees[i].Path)
		if err != nil {
			continue
		}
		cwdAbs, err := filepath.Abs(cwd)
		if err != nil {
			continue
		}
		// Check if cwd is within or equal to the worktree path
		if cwdAbs == wtPath || strings.HasPrefix(cwdAbs, wtPath+string(filepath.Separator)) {
			return &worktrees[i], nil
		}
	}

	return nil, ErrNotFound
}

// FindRepoRoot finds the root of the git repository starting from the given path.
// It uses `git rev-parse --show-toplevel`.
func FindRepoRoot(startPath string) (string, error) {
	output, err := executor(startPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", ErrNotInGitRepo
	}

	root := strings.TrimSpace(string(output))
	if root == "" {
		return "", ErrNotInGitRepo
	}

	return root, nil
}

// GetMainWorktree returns the main (non-linked) worktree.
// The main worktree is typically listed first in the worktree list.
func GetMainWorktree(repoPath string) (*Worktree, error) {
	worktrees, err := List(repoPath)
	if err != nil {
		return nil, err
	}

	if len(worktrees) == 0 {
		return nil, ErrNotFound
	}

	// The main worktree is the first one listed
	return &worktrees[0], nil
}

// SetExecutor sets a custom git executor (for testing purposes).
func SetExecutor(e gitExecutor) {
	executor = e
}

// ResetExecutor resets the git executor to the default.
func ResetExecutor() {
	executor = defaultGitExecutor
}
