package tui

// OutputMsg is sent when a process produces output.
type OutputMsg struct {
	Process string
	Data    []byte
}

// SwitchMsg is sent when switching between worktrees.
type SwitchMsg struct {
	OldWorktree string
	NewWorktree string
	OldBranch   string
	NewBranch   string
}

// RouteMsg is sent when ngrok routing changes.
type RouteMsg struct {
	SessionName string
	Port        int
	PublicURL   string
}

// ProcessStatusMsg is sent when a process status changes.
type ProcessStatusMsg struct {
	Name   string
	Status string
	Ports  []uint32
}

// ErrorMsg is sent when an error occurs.
type ErrorMsg struct {
	Err error
}

// QuitMsg is sent when the user wants to quit.
type QuitMsg struct{}

// StopAllMsg is sent when the user wants to stop all processes.
type StopAllMsg struct{}

// TickMsg is sent periodically for status updates.
type TickMsg struct{}
