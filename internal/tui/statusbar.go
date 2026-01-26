package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// StatusBar represents the bottom status bar of the TUI.
type StatusBar struct {
	sessionName   string
	sessionIndex  int
	worktree      string
	branch        string
	status        string // "Running", "Switching...", "Stopped"
	processInfo   string // "3 processes running"
	ngrokCount    int    // Number of active ngrok tunnels
	routedSession string // Name of session ngrok is routing to
	ngrokURL      string // Public ngrok URL
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

	// Left section: session info, worktree and branch info
	leftParts := []string{}

	// Session badge [name:index]
	if s.sessionName != "" {
		sessionBadge := SessionBadgeStyle.Render(fmt.Sprintf("[%s:%d]", s.sessionName, s.sessionIndex))
		leftParts = append(leftParts, sessionBadge)
	}

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

	// Right section: ngrok routing info, process info
	rightParts := []string{}
	if s.routedSession != "" {
		// Show which session is being routed
		routeText := fmt.Sprintf("🌐 → %s", s.routedSession)
		rightParts = append(rightParts, NgrokIndicatorStyle.Render(routeText))
	} else if s.ngrokCount > 0 {
		// Fallback to tunnel count if no routing info
		ngrokText := fmt.Sprintf("🌐 %d tunnel", s.ngrokCount)
		if s.ngrokCount != 1 {
			ngrokText += "s"
		}
		rightParts = append(rightParts, NgrokIndicatorStyle.Render(ngrokText))
	}
	if s.processInfo != "" {
		rightParts = append(rightParts, ProcessCountStyle.Render(s.processInfo))
	}
	right := strings.Join(rightParts, " ")

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

// SetSession updates the session name and index.
func (s *StatusBar) SetSession(name string, index int) {
	s.sessionName = name
	s.sessionIndex = index
}

// SetNgrokCount updates the ngrok tunnel count.
func (s *StatusBar) SetNgrokCount(count int) {
	s.ngrokCount = count
}

// SetRoutedSession updates the name of the session ngrok is routing to.
func (s *StatusBar) SetRoutedSession(name string) {
	s.routedSession = name
}

// SetNgrokURL updates the public ngrok URL.
func (s *StatusBar) SetNgrokURL(url string) {
	s.ngrokURL = url
}
