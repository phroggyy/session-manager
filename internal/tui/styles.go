package tui

import "github.com/charmbracelet/lipgloss"

// Color palette
var (
	// Primary colors
	primaryColor   = lipgloss.Color("#7C3AED") // Purple
	secondaryColor = lipgloss.Color("#3B82F6") // Blue
	accentColor    = lipgloss.Color("#10B981") // Green

	// Status colors
	runningColor  = lipgloss.Color("#10B981") // Green
	stoppedColor  = lipgloss.Color("#6B7280") // Gray
	crashedColor  = lipgloss.Color("#EF4444") // Red
	switchingColor = lipgloss.Color("#F59E0B") // Amber

	// Background colors
	statusBarBg       = lipgloss.Color("#1F2937") // Dark gray
	statusBarBgActive = lipgloss.Color("#374151") // Lighter gray when switching
	paneBorderColor   = lipgloss.Color("#4B5563") // Gray for unfocused
	paneFocusedColor  = lipgloss.Color("#7C3AED") // Purple for focused

	// Text colors
	textColor        = lipgloss.Color("#F9FAFB") // Near white
	textMutedColor   = lipgloss.Color("#9CA3AF") // Muted gray
	textDimColor     = lipgloss.Color("#6B7280") // Dim gray
)

// Status bar styles
var (
	StatusBarStyle = lipgloss.NewStyle().
			Background(statusBarBg).
			Foreground(textColor).
			Padding(0, 1)

	StatusBarSwitchingStyle = lipgloss.NewStyle().
				Background(statusBarBgActive).
				Foreground(switchingColor).
				Bold(true).
				Padding(0, 1)

	WorktreeStyle = lipgloss.NewStyle().
			Foreground(secondaryColor).
			Bold(true)

	BranchStyle = lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true)

	StatusTextStyle = lipgloss.NewStyle().
			Foreground(textMutedColor)

	ProcessCountStyle = lipgloss.NewStyle().
				Foreground(textDimColor)
)

// Pane styles
var (
	PaneBorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(paneBorderColor)

	PaneFocusedBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(paneFocusedColor)

	PaneTitleStyle = lipgloss.NewStyle().
			Foreground(textColor).
			Bold(true).
			Padding(0, 1)

	PaneTitleFocusedStyle = lipgloss.NewStyle().
				Foreground(paneFocusedColor).
				Bold(true).
				Padding(0, 1)
)

// Status indicator styles
var (
	StatusRunningStyle = lipgloss.NewStyle().
				Foreground(runningColor).
				Bold(true)

	StatusStoppedStyle = lipgloss.NewStyle().
				Foreground(stoppedColor)

	StatusCrashedStyle = lipgloss.NewStyle().
				Foreground(crashedColor).
				Bold(true)
)

// GetStatusStyle returns the appropriate style for a given status.
func GetStatusStyle(status string) lipgloss.Style {
	switch status {
	case "Running", "running":
		return StatusRunningStyle
	case "Crashed", "crashed":
		return StatusCrashedStyle
	default:
		return StatusStoppedStyle
	}
}

// GetStatusIndicator returns a styled status indicator string.
func GetStatusIndicator(status string) string {
	style := GetStatusStyle(status)
	switch status {
	case "Running", "running":
		return style.Render("● Running")
	case "Crashed", "crashed":
		return style.Render("✗ Crashed")
	default:
		return style.Render("○ Stopped")
	}
}

// Help text styles
var (
	HelpStyle = lipgloss.NewStyle().
			Foreground(textDimColor)

	HelpKeyStyle = lipgloss.NewStyle().
			Foreground(textMutedColor).
			Bold(true)
)

// BuildHelpText builds the help text shown in the status bar.
func BuildHelpText() string {
	return HelpStyle.Render("Tab") + HelpKeyStyle.Render(":switch") +
		HelpStyle.Render(" ↑/↓") + HelpKeyStyle.Render(":scroll") +
		HelpStyle.Render(" q") + HelpKeyStyle.Render(":quit") +
		HelpStyle.Render(" Ctrl+C") + HelpKeyStyle.Render(":stop all")
}
