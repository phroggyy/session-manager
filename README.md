# Session Manager (sm)

A CLI tool to manage long-running processes across git worktrees with a live-updating TUI.

## Overview

Session Manager allows you to define a set of processes (web servers, build watchers, etc.) and seamlessly switch them between different git worktrees. This is particularly useful when:

- Working with multiple feature branches simultaneously
- Verifying changes made by AI agents in different worktrees
- Running the same development environment across different branches

## Installation

```bash
go install github.com/leosjoberg/session-manager/cmd/sm@latest
```

Or build from source:

```bash
git clone https://github.com/leosjoberg/session-manager.git
cd session-manager
make build
# Binary will be at ./bin/sm
```

## Quick Start

1. Create a configuration file `sm.yaml` in your repository root:

```yaml
processes:
  - name: web-server
    command: make run
    cwd: ./server
  - name: dashboard
    command: npm run dev
    cwd: ./dashboard
```

2. Start the session manager:

```bash
sm start
```

3. Switch to a different worktree from another terminal:

```bash
sm switch feature-branch
# or
sm switch ../my-repo-feature
```

The TUI will automatically update to show processes running in the new worktree.

## Commands

| Command | Description |
|---------|-------------|
| `sm start` | Start daemon and processes, then attach TUI |
| `sm stop` | Stop all processes and daemon |
| `sm switch <target>` | Switch to different worktree (by branch name or path) |
| `sm status` | Show current state (non-TUI) |
| `sm attach` | Attach TUI to running daemon |
| `sm list` | List available git worktrees |
| `sm logs <process>` | View logs for a specific process |

## Configuration

Create an `sm.yaml`, `sm.yml`, or `sm.json` file in your repository. The tool will search from the current directory up to the git root.

### YAML Format

```yaml
processes:
  - name: backend
    command: go run ./cmd/server
    cwd: .
    env:
      DEBUG: "true"
      PORT: "8080"

  - name: frontend
    command: npm run dev
    cwd: ./frontend
    env:
      VITE_API_URL: http://localhost:8080
```

### JSON Format

```json
{
  "processes": [
    {
      "name": "backend",
      "command": "go run ./cmd/server",
      "cwd": ".",
      "env": {
        "DEBUG": "true"
      }
    }
  ]
}
```

### Process Configuration Fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Unique identifier for the process |
| `command` | Yes | Shell command to execute |
| `cwd` | No | Working directory relative to worktree root |
| `env` | No | Environment variables for the process |

## TUI Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `Tab` | Switch focus between panes |
| `Shift+Tab` | Switch focus to previous pane |
| `Up/k` | Scroll up |
| `Down/j` | Scroll down |
| `PgUp/Ctrl+B` | Page up |
| `PgDown/Ctrl+F` | Page down |
| `Ctrl+U` | Half page up |
| `Ctrl+D` | Half page down |
| `Home/g` | Go to top |
| `End/G` | Go to bottom |
| `q/Esc` | Detach (quit TUI, keep daemon running) |
| `Ctrl+C` | Stop all processes and quit |

## Architecture

Session Manager uses a daemon/client architecture:

- **Daemon**: Runs in the background, manages processes, handles switching
- **TUI Client**: Connects to daemon, displays live output, receives updates
- **CLI Commands**: Communicate with daemon via Unix socket

This allows multiple terminals to attach to the same session and see live updates when switching worktrees.

### State Storage

Session state is stored in `~/.sm/sessions/<repo-hash>/`:

```
~/.sm/sessions/<repo-hash>/
├── state.json      # Session state
├── sm.sock         # Unix socket for IPC
└── logs/
    ├── backend.log
    └── frontend.log
```

## Development

```bash
# Build
make build

# Run tests
make test

# Run with ginkgo
make test-ginkgo

# Lint
make lint

# Install locally
make install
```

## License

MIT
