package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
)

// Pane represents a scrollable output pane for a process.
type Pane struct {
	name     string
	viewport viewport.Model
	content  strings.Builder
	status   string // Running, Stopped, Crashed
	ports    []uint32
	focused  bool
}

// NewPane creates a new Pane with the given name and dimensions.
func NewPane(name string, width, height int) Pane {
	// Account for border (2 chars on each side) and title line
	contentWidth := width - 4
	contentHeight := height - 3 // Border top/bottom + title

	if contentWidth < 1 {
		contentWidth = 1
	}
	if contentHeight < 1 {
		contentHeight = 1
	}

	vp := viewport.New(contentWidth, contentHeight)
	vp.Style = lipgloss.NewStyle()

	return Pane{
		name:     name,
		viewport: vp,
		status:   "Stopped",
		focused:  false,
	}
}

// View renders the pane with its border and content.
func (p *Pane) View() string {
	// Choose border style based on focus
	var borderStyle lipgloss.Style
	var titleStyle lipgloss.Style

	if p.focused {
		borderStyle = PaneFocusedBorderStyle
		titleStyle = PaneTitleFocusedStyle
	} else {
		borderStyle = PaneBorderStyle
		titleStyle = PaneTitleStyle
	}

	// Build title with status indicator and ports
	statusIndicator := GetStatusIndicator(p.status)
	var title string
	if len(p.ports) > 0 {
		portsStr := formatPorts(p.ports)
		title = titleStyle.Render(fmt.Sprintf("[%s:%s]", p.name, portsStr)) + " " + statusIndicator
	} else {
		title = titleStyle.Render(p.name) + " " + statusIndicator
	}

	// Calculate width for the title bar
	titleWidth := p.viewport.Width

	// Create title bar
	titleBar := lipgloss.NewStyle().
		Width(titleWidth).
		Render(title)

	// Render viewport content
	viewportContent := p.viewport.View()

	// Combine title and viewport
	content := lipgloss.JoinVertical(lipgloss.Left, titleBar, viewportContent)

	return borderStyle.Render(content)
}

// AppendOutput appends new output data to the pane.
func (p *Pane) AppendOutput(data []byte) {
	if len(data) == 0 {
		return
	}

	// Append the new data
	p.content.Write(data)
	p.content.WriteString("\n")

	// Update viewport content
	p.viewport.SetContent(p.content.String())

	// Auto-scroll to bottom if we were at the bottom
	p.viewport.GotoBottom()
}

// SetStatus updates the pane's status indicator.
func (p *Pane) SetStatus(status string) {
	p.status = status
}

// SetFocused sets whether this pane is focused.
func (p *Pane) SetFocused(focused bool) {
	p.focused = focused
}

// IsFocused returns whether this pane is currently focused.
func (p *Pane) IsFocused() bool {
	return p.focused
}

// GetName returns the pane's name.
func (p *Pane) GetName() string {
	return p.name
}

// GetStatus returns the pane's current status.
func (p *Pane) GetStatus() string {
	return p.status
}

// SetPorts updates the pane's detected ports.
func (p *Pane) SetPorts(ports []uint32) {
	p.ports = ports
}

// GetPorts returns the pane's current ports.
func (p *Pane) GetPorts() []uint32 {
	return p.ports
}

// SetSize updates the pane dimensions.
func (p *Pane) SetSize(width, height int) {
	// Account for border and title
	contentWidth := width - 4
	contentHeight := height - 3

	if contentWidth < 1 {
		contentWidth = 1
	}
	if contentHeight < 1 {
		contentHeight = 1
	}

	p.viewport.Width = contentWidth
	p.viewport.Height = contentHeight

	// Re-set content to trigger wrapping
	p.viewport.SetContent(p.content.String())
}

// ScrollUp scrolls the viewport up by one line.
func (p *Pane) ScrollUp() {
	p.viewport.ScrollUp(1)
}

// ScrollDown scrolls the viewport down by one line.
func (p *Pane) ScrollDown() {
	p.viewport.ScrollDown(1)
}

// PageUp scrolls the viewport up by one page.
func (p *Pane) PageUp() {
	p.viewport.PageUp()
}

// PageDown scrolls the viewport down by one page.
func (p *Pane) PageDown() {
	p.viewport.PageDown()
}

// HalfPageUp scrolls the viewport up by half a page.
func (p *Pane) HalfPageUp() {
	p.viewport.HalfPageUp()
}

// HalfPageDown scrolls the viewport down by half a page.
func (p *Pane) HalfPageDown() {
	p.viewport.HalfPageDown()
}

// GotoTop scrolls to the top of the content.
func (p *Pane) GotoTop() {
	p.viewport.GotoTop()
}

// GotoBottom scrolls to the bottom of the content.
func (p *Pane) GotoBottom() {
	p.viewport.GotoBottom()
}

// Clear clears the pane content.
func (p *Pane) Clear() {
	p.content.Reset()
	p.viewport.SetContent("")
}

// ScrollPercent returns the current scroll position as a percentage.
func (p *Pane) ScrollPercent() float64 {
	return p.viewport.ScrollPercent()
}

// String implements fmt.Stringer for debugging.
func (p *Pane) String() string {
	return fmt.Sprintf("Pane{name: %s, status: %s, focused: %v}", p.name, p.status, p.focused)
}

// formatPorts formats a list of ports for display in the pane title.
func formatPorts(ports []uint32) string {
	if len(ports) == 0 {
		return ""
	}
	if len(ports) == 1 {
		return fmt.Sprintf("%d", ports[0])
	}
	if len(ports) == 2 {
		return fmt.Sprintf("%d,%d", ports[0], ports[1])
	}
	return fmt.Sprintf("%d,%d+%d", ports[0], ports[1], len(ports)-2)
}
