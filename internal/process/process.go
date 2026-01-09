package process

import (
	"os"
	"os/exec"
	"sync"
	"time"
)

// ProcessStatus represents the current state of a process.
type ProcessStatus int

const (
	StatusStopped ProcessStatus = iota
	StatusRunning
	StatusCrashed
)

// String returns a human-readable representation of the process status.
func (s ProcessStatus) String() string {
	switch s {
	case StatusStopped:
		return "stopped"
	case StatusRunning:
		return "running"
	case StatusCrashed:
		return "crashed"
	default:
		return "unknown"
	}
}

// Process represents a managed process with its metadata and runtime state.
type Process struct {
	Name         string
	PID          int
	Status       ProcessStatus
	Command      string
	Cwd          string
	Env          map[string]string
	StartedAt    time.Time
	WorktreePath string

	cmd        *exec.Cmd
	logFile    *os.File
	outputChan chan []byte
	done       chan struct{}

	// subscribers holds channels that receive process output
	subscribers map[chan []byte]struct{}
	subMu       sync.RWMutex
}

// newProcess creates a new Process instance with initialized fields.
func newProcess(name, command, cwd, worktreePath string, env map[string]string) *Process {
	return &Process{
		Name:         name,
		Status:       StatusStopped,
		Command:      command,
		Cwd:          cwd,
		Env:          env,
		WorktreePath: worktreePath,
		outputChan:   make(chan []byte, 1024),
		done:         make(chan struct{}),
		subscribers:  make(map[chan []byte]struct{}),
	}
}

// addSubscriber adds a new subscriber channel to receive output.
func (p *Process) addSubscriber(ch chan []byte) {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	p.subscribers[ch] = struct{}{}
}

// removeSubscriber removes a subscriber channel.
func (p *Process) removeSubscriber(ch chan []byte) {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	delete(p.subscribers, ch)
	close(ch)
}

// broadcast sends data to all subscribers.
func (p *Process) broadcast(data []byte) {
	p.subMu.RLock()
	defer p.subMu.RUnlock()

	for ch := range p.subscribers {
		// Non-blocking send to avoid slow subscribers blocking others
		select {
		case ch <- data:
		default:
			// Drop message if subscriber buffer is full
		}
	}
}
