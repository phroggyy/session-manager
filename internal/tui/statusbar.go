package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// StatusBar represents the bottom status bar of the TUI.
type StatusBar struct {
	worktree    string
	branch      string
	status      string // "Running", "Switching...", "Stopped"
	processInfo string // "3 processes running"
}

// NewStatusBar creates a new StatusBar with default values.
func NewStatusBar() StatusBar {
	return StatusBar{
		status: "Stopped",
	}
}

// View renders the status bar to fit the given width.
func (s *StatusBar) View(width int) string {
	if width <= 0 {
		return ""
	}

	// Choose base style based on status
	var baseStyle lipgloss.Style
	if s.status == "Switching..." {
		baseStyle = StatusBarSwitchingStyle.Width(width)
	} else {
		baseStyle = StatusBarStyle.Width(width)
	}

	// Left section: worktree and branch info
	leftParts := []string{}

	if s.worktree != "" {
		worktreeText := WorktreeStyle.Render(s.worktree)
		leftParts = append(leftParts, worktreeText)
	}

	if s.branch != "" {
		branchText := BranchStyle.Render("⎇ " + s.branch)
		leftParts = append(leftParts, branchText)
	}

	left := strings.Join(leftParts, " ")

	// Center section: status
	var statusText string
	switch s.status {
	case "Running":
		statusText = StatusRunningStyle.Render("● " + s.status)
	case "Switching...":
		statusText = lipgloss.NewStyle().Foreground(switchingColor).Bold(true).Render("⟳ " + s.status)
	default:
		statusText = StatusStoppedStyle.Render("○ " + s.status)
	}

	// Right section: process info and help
	right := ""
	if s.processInfo != "" {
		right = ProcessCountStyle.Render(s.processInfo)
	}

	// Calculate available space
	leftWidth := lipgloss.Width(left)
	statusWidth := lipgloss.Width(statusText)
	rightWidth := lipgloss.Width(right)

	// Build the final bar with proper spacing
	// Format: [left] [status centered] [right]
	totalContentWidth := leftWidth + statusWidth + rightWidth
	if totalContentWidth >= width-4 {
		// Not enough space, truncate
		content := left + " " + statusText
		if rightWidth > 0 {
			content += " " + right
		}
		return baseStyle.Render(content)
	}

	// Calculate gaps
	availableSpace := width - totalContentWidth - 4 // Account for padding
	leftGap := availableSpace / 2
	rightGap := availableSpace - leftGap

	var sb strings.Builder
	sb.WriteString(left)
	sb.WriteString(strings.Repeat(" ", leftGap))
	sb.WriteString(statusText)
	sb.WriteString(strings.Repeat(" ", rightGap))
	sb.WriteString(right)

	return baseStyle.Render(sb.String())
}

// SetSwitching sets the status bar to "Switching..." mode.
func (s *StatusBar) SetSwitching() {
	s.status = "Switching..."
}

// SetRunning sets the status bar to running mode with worktree and process info.
func (s *StatusBar) SetRunning(worktree, branch string, processCount int) {
	s.worktree = worktree
	s.branch = branch
	s.status = "Running"
	s.processInfo = fmt.Sprintf("%d process", processCount)
	if processCount != 1 {
		s.processInfo += "es"
	}
	s.processInfo += " running"
}

// SetStopped sets the status bar to stopped mode.
func (s *StatusBar) SetStopped() {
	s.status = "Stopped"
	s.processInfo = ""
}

// SetWorktree updates the current worktree path.
func (s *StatusBar) SetWorktree(worktree string) {
	s.worktree = worktree
}

// SetBranch updates the current branch name.
func (s *StatusBar) SetBranch(branch string) {
	s.branch = branch
}

// SetProcessCount updates the process count info.
func (s *StatusBar) SetProcessCount(count int) {
	s.processInfo = fmt.Sprintf("%d process", count)
	if count != 1 {
		s.processInfo += "es"
	}
	s.processInfo += " running"
}
