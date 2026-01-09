package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/leosjoberg/session-manager/internal/daemon"
)

const (
	// tickInterval is how often we poll for updates
	tickInterval = 100 * time.Millisecond
)

// Model is the main bubbletea model for the TUI.
type Model struct {
	client     *daemon.Client
	eventCh    <-chan daemon.Event
	panes      []Pane
	statusBar  StatusBar
	focusedIdx int
	width      int
	height     int
	ready      bool
	err        error
	quitting   bool
}

// New creates a new TUI Model connected to the given daemon client.
func New(client *daemon.Client) *Model {
	return &Model{
		client:     client,
		panes:      []Pane{},
		statusBar:  NewStatusBar(),
		focusedIdx: 0,
		ready:      false,
	}
}

// NewWithStatus creates a new TUI Model with initial status from the daemon.
func NewWithStatus(client *daemon.Client, status *daemon.StatusResponse) *Model {
	m := &Model{
		client:     client,
		panes:      []Pane{},
		statusBar:  NewStatusBar(),
		focusedIdx: 0,
		ready:      false,
	}

	// Set initial status bar info
	runningCount := 0
	for _, proc := range status.Processes {
		if proc.Status == "running" {
			runningCount++
		}
	}
	m.statusBar.SetRunning(status.CurrentWorktree, status.CurrentBranch, runningCount)

	// Create panes with initial status
	for i, proc := range status.Processes {
		pane := NewPane(proc.Name, 80, 24) // Default size, will be resized
		pane.SetStatus(proc.Status)
		if i == 0 {
			pane.SetFocused(true)
		}
		m.panes = append(m.panes, pane)
	}

	return m
}

// Init initializes the model and returns the initial command.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		tea.EnterAltScreen,
		m.tickCmd(),
		m.subscribeToEvents(),
	)
}

// tickCmd returns a command that sends a tick message after the tick interval.
func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg {
		return TickMsg{}
	})
}

// subscribeToEvents subscribes to daemon events and returns output messages.
func (m *Model) subscribeToEvents() tea.Cmd {
	if m.client == nil {
		return nil
	}

	// Subscribe if not already subscribed
	if m.eventCh == nil {
		eventCh, err := m.client.Subscribe()
		if err != nil {
			return func() tea.Msg {
				return ErrorMsg{Err: fmt.Errorf("failed to subscribe: %w", err)}
			}
		}
		m.eventCh = eventCh
	}

	return m.waitForEvent()
}

// waitForEvent waits for the next event from the daemon.
func (m Model) waitForEvent() tea.Cmd {
	if m.eventCh == nil {
		return nil
	}

	return func() tea.Msg {
		event, ok := <-m.eventCh
		if !ok {
			// Channel closed - daemon disconnected, just stop listening
			// Don't treat as error, the tick will continue to poll status
			return nil
		}

		return m.convertEvent(event)
	}
}

// convertEvent converts a daemon event to a TUI message.
func (m Model) convertEvent(event daemon.Event) tea.Msg {
	switch event.Type {
	case daemon.EventOutput:
		if data, ok := event.Data.(string); ok {
			return OutputMsg{Process: event.Process, Data: []byte(data)}
		}
		if data, ok := event.Data.([]byte); ok {
			return OutputMsg{Process: event.Process, Data: data}
		}
	case daemon.EventSwitch:
		if data, ok := event.Data.(daemon.SwitchData); ok {
			return SwitchMsg{
				OldWorktree: data.OldWorktree,
				NewWorktree: data.NewWorktree,
				OldBranch:   data.OldBranch,
				NewBranch:   data.NewBranch,
			}
		}
		// Handle map[string]interface{} from JSON decoding
		if data, ok := event.Data.(map[string]interface{}); ok {
			return SwitchMsg{
				OldWorktree: getStringFromMap(data, "old_worktree"),
				NewWorktree: getStringFromMap(data, "new_worktree"),
				OldBranch:   getStringFromMap(data, "old_branch"),
				NewBranch:   getStringFromMap(data, "new_branch"),
			}
		}
	case daemon.EventProcessStatus:
		if data, ok := event.Data.(daemon.ProcessStatusData); ok {
			return ProcessStatusMsg{Name: data.Name, Status: data.Status}
		}
		// Handle map[string]interface{} from JSON decoding
		if data, ok := event.Data.(map[string]interface{}); ok {
			return ProcessStatusMsg{
				Name:   getStringFromMap(data, "name"),
				Status: getStringFromMap(data, "status"),
			}
		}
	case daemon.EventError:
		if errStr, ok := event.Data.(string); ok {
			return ErrorMsg{Err: fmt.Errorf("%s", errStr)}
		}
	}
	return nil
}

// getStringFromMap safely extracts a string from a map.
func getStringFromMap(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Update handles messages and updates the model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle Ctrl+C by type for more reliable detection
		if msg.Type == tea.KeyCtrlC {
			m.quitting = true
			if m.client != nil {
				go m.client.Stop()
			}
			return m, tea.Quit
		}

		switch msg.String() {
		case "q", "esc":
			m.quitting = true
			return m, tea.Quit

		case "tab":
			// Switch focus to next pane
			if len(m.panes) > 0 {
				m.panes[m.focusedIdx].SetFocused(false)
				m.focusedIdx = (m.focusedIdx + 1) % len(m.panes)
				m.panes[m.focusedIdx].SetFocused(true)
			}

		case "shift+tab":
			// Switch focus to previous pane
			if len(m.panes) > 0 {
				m.panes[m.focusedIdx].SetFocused(false)
				m.focusedIdx--
				if m.focusedIdx < 0 {
					m.focusedIdx = len(m.panes) - 1
				}
				m.panes[m.focusedIdx].SetFocused(true)
			}

		case "up", "k":
			// Scroll focused pane up
			if len(m.panes) > 0 && m.focusedIdx < len(m.panes) {
				m.panes[m.focusedIdx].ScrollUp()
			}

		case "down", "j":
			// Scroll focused pane down
			if len(m.panes) > 0 && m.focusedIdx < len(m.panes) {
				m.panes[m.focusedIdx].ScrollDown()
			}

		case "pgup", "ctrl+b":
			// Page up in focused pane
			if len(m.panes) > 0 && m.focusedIdx < len(m.panes) {
				m.panes[m.focusedIdx].PageUp()
			}

		case "pgdown", "ctrl+f":
			// Page down in focused pane
			if len(m.panes) > 0 && m.focusedIdx < len(m.panes) {
				m.panes[m.focusedIdx].PageDown()
			}

		case "ctrl+u":
			// Half page up
			if len(m.panes) > 0 && m.focusedIdx < len(m.panes) {
				m.panes[m.focusedIdx].HalfPageUp()
			}

		case "ctrl+d":
			// Half page down
			if len(m.panes) > 0 && m.focusedIdx < len(m.panes) {
				m.panes[m.focusedIdx].HalfPageDown()
			}

		case "home", "g":
			// Go to top
			if len(m.panes) > 0 && m.focusedIdx < len(m.panes) {
				m.panes[m.focusedIdx].GotoTop()
			}

		case "end", "G":
			// Go to bottom
			if len(m.panes) > 0 && m.focusedIdx < len(m.panes) {
				m.panes[m.focusedIdx].GotoBottom()
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.resizePanes()

	case OutputMsg:
		// Find the pane for this process and append output
		for i := range m.panes {
			if m.panes[i].GetName() == msg.Process {
				m.panes[i].AppendOutput(msg.Data)
				break
			}
		}
		// Continue subscribing to events
		cmds = append(cmds, m.subscribeToEvents())

	case SwitchMsg:
		// Update status bar for switch
		m.statusBar.SetSwitching()
		// After switch completes, update with new worktree info
		m.statusBar.SetWorktree(msg.NewWorktree)
		m.statusBar.SetBranch(msg.NewBranch)
		// Clear pane contents for new worktree
		for i := range m.panes {
			m.panes[i].Clear()
		}
		cmds = append(cmds, m.subscribeToEvents())

	case ProcessStatusMsg:
		// Update pane status
		for i := range m.panes {
			if m.panes[i].GetName() == msg.Name {
				m.panes[i].SetStatus(msg.Status)
				break
			}
		}
		// Update process count in status bar
		m.updateStatusBarProcessCount()
		cmds = append(cmds, m.subscribeToEvents())

	case ErrorMsg:
		m.err = msg.Err
		cmds = append(cmds, m.subscribeToEvents())

	case TickMsg:
		// Periodic update - refresh status from daemon
		if m.client != nil && !m.quitting {
			m.refreshFromDaemon()
		}
		cmds = append(cmds, m.tickCmd())
	}

	return m, tea.Batch(cmds...)
}

// resizePanes resizes all panes to fit the current window size.
func (m *Model) resizePanes() {
	if len(m.panes) == 0 {
		return
	}

	// Reserve space for status bar (1 line + padding)
	statusBarHeight := 1
	availableHeight := m.height - statusBarHeight

	// Calculate pane dimensions
	numPanes := len(m.panes)
	paneWidth := m.width / numPanes
	remainingWidth := m.width % numPanes

	for i := range m.panes {
		width := paneWidth
		// Distribute remaining width to last pane
		if i == numPanes-1 {
			width += remainingWidth
		}
		m.panes[i].SetSize(width, availableHeight)
	}
}

// updateStatusBarProcessCount updates the status bar with current process count.
func (m *Model) updateStatusBarProcessCount() {
	runningCount := 0
	for _, pane := range m.panes {
		if pane.GetStatus() == "Running" || pane.GetStatus() == "running" {
			runningCount++
		}
	}
	m.statusBar.SetProcessCount(runningCount)

	if runningCount > 0 {
		m.statusBar.status = "Running"
	} else {
		m.statusBar.status = "Stopped"
	}
}

// refreshFromDaemon refreshes the model state from the daemon.
func (m *Model) refreshFromDaemon() {
	if m.client == nil {
		return
	}

	status, err := m.client.Status()
	if err != nil {
		return
	}

	// Update status bar
	runningCount := 0
	for _, proc := range status.Processes {
		if proc.Status == "running" {
			runningCount++
		}
		// Update pane status
		for i := range m.panes {
			if m.panes[i].GetName() == proc.Name {
				m.panes[i].SetStatus(proc.Status)
				break
			}
		}
	}

	m.statusBar.SetRunning(status.CurrentWorktree, status.CurrentBranch, runningCount)
}

// View renders the TUI.
func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	if m.quitting {
		return "Goodbye!\n"
	}

	if m.err != nil {
		return lipgloss.NewStyle().
			Foreground(crashedColor).
			Render("Error: " + m.err.Error())
	}

	// Render panes horizontally
	var paneViews []string
	for _, pane := range m.panes {
		paneViews = append(paneViews, pane.View())
	}

	panesRow := lipgloss.JoinHorizontal(lipgloss.Top, paneViews...)

	// Render status bar
	statusBar := m.statusBar.View(m.width)

	// Combine panes and status bar
	return lipgloss.JoinVertical(lipgloss.Left, panesRow, statusBar)
}

// AddPane adds a new pane for a process.
func (m *Model) AddPane(name string) {
	// Calculate dimensions based on current size
	numPanes := len(m.panes) + 1
	paneWidth := m.width / numPanes
	paneHeight := m.height - 1 // Reserve for status bar

	if paneWidth < 20 {
		paneWidth = 20
	}
	if paneHeight < 5 {
		paneHeight = 5
	}

	pane := NewPane(name, paneWidth, paneHeight)
	if len(m.panes) == 0 {
		pane.SetFocused(true)
	}
	m.panes = append(m.panes, pane)

	// Resize all panes to distribute space evenly
	if m.ready {
		m.resizePanes()
	}
}

// SetPanes sets the panes for the model.
func (m *Model) SetPanes(names []string) {
	m.panes = make([]Pane, 0, len(names))
	for _, name := range names {
		m.AddPane(name)
	}
	if len(m.panes) > 0 {
		m.focusedIdx = 0
		m.panes[0].SetFocused(true)
	}
}

// GetFocusedPane returns the currently focused pane, or nil if none.
func (m *Model) GetFocusedPane() *Pane {
	if len(m.panes) == 0 || m.focusedIdx >= len(m.panes) {
		return nil
	}
	return &m.panes[m.focusedIdx]
}

// SetError sets an error to display.
func (m *Model) SetError(err error) {
	m.err = err
}

// ClearError clears any displayed error.
func (m *Model) ClearError() {
	m.err = nil
}
