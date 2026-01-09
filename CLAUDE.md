# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Session Manager (`sm`) is a Go CLI tool for managing long-running processes across git worktrees. It uses a daemon/client architecture with a bubbletea-based TUI.

## Build & Test Commands

```bash
# Build the binary
make build          # Outputs to ./bin/sm

# Run tests
make test           # Standard go test
make test-ginkgo    # Using ginkgo runner

# Lint
make lint           # Requires golangci-lint

# Install to $GOPATH/bin
make install
```

## Architecture

```
cmd/sm/main.go              # CLI entry point with cobra commands
internal/
├── config/                 # YAML/JSON config loading and validation
├── daemon/                 # Background process manager daemon
│   ├── daemon.go          # Main daemon logic, switch orchestration
│   ├── server.go          # Unix socket RPC server
│   ├── client.go          # Client for CLI/TUI to talk to daemon
│   └── events.go          # Event types for broadcasting
├── process/               # Process lifecycle management
│   ├── process.go         # Process type and status enum
│   └── manager.go         # Start/stop/list processes, log capture
├── session/               # Persistent session state
│   ├── state.go           # State structs (JSON serialization)
│   └── session.go         # Session CRUD, paths (~/.sm/sessions/)
├── worktree/              # Git worktree operations
│   └── worktree.go        # List, resolve, current worktree
└── tui/                   # Bubbletea TUI
    ├── model.go           # Main tea.Model
    ├── pane.go            # Process output pane with viewport
    ├── statusbar.go       # Branch/worktree status bar
    └── styles.go          # Lipgloss styles
```

## Key Patterns

### Daemon/Client Communication
- Unix socket at `~/.sm/sessions/<repo-hash>/sm.sock`
- JSON-RPC style messages (Request/Response structs in `daemon/server.go`)
- Events broadcast to subscribed TUI clients for live updates

### Process Management
- Uses `os/exec` with `SysProcAttr{Setpgid: true}` for process groups
- Graceful shutdown: SIGTERM → 5s wait → SIGKILL
- Output captured to log files and streamed to subscribers

### Worktree Resolution
- `worktree.Resolve()` accepts branch name or path
- Parses `git worktree list --porcelain` output

## Common Tasks

### Adding a new CLI command
1. Add cobra command definition in `cmd/sm/main.go`
2. Register in `init()` function
3. Implement `runXxx` function

### Adding daemon RPC method
1. Add handler in `daemon/server.go` `handleRequest()` switch
2. Add client method in `daemon/client.go`
3. Define any new params/response types

### Modifying TUI behavior
1. Add message type in `tui/messages.go`
2. Handle in `model.go` `Update()` method
3. Update `View()` if needed

## Testing

Config and worktree packages have tests. Process manager tests may spawn real processes.

```bash
# Run specific package tests
go test ./internal/config/...
go test ./internal/worktree/...

# With verbose output
go test -v ./internal/process/...
```
