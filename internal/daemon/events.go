package daemon

// EventType represents the type of event being broadcast.
type EventType string

const (
	EventOutput        EventType = "output"
	EventSwitch        EventType = "switch"
	EventProcessStatus EventType = "process_status"
	EventError         EventType = "error"
)

// Event represents an event that can be broadcast to subscribers.
type Event struct {
	Type    EventType   `json:"type"`
	Process string      `json:"process,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// SwitchData contains information about a worktree switch event.
type SwitchData struct {
	OldWorktree string `json:"old_worktree"`
	NewWorktree string `json:"new_worktree"`
	OldBranch   string `json:"old_branch"`
	NewBranch   string `json:"new_branch"`
}

// ProcessStatusData contains information about a process status change.
type ProcessStatusData struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	PID    int    `json:"pid"`
}
