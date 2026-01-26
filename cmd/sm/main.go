package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/phroggyy/session-manager/internal/config"
	"github.com/phroggyy/session-manager/internal/daemon"
	"github.com/phroggyy/session-manager/internal/session"
	"github.com/phroggyy/session-manager/internal/tui"
	"github.com/phroggyy/session-manager/internal/worktree"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	verbose      bool
	configPath   string
	follow       bool
	sessionFlag  string // Branch name or worktree path to identify session
	logger       *zap.Logger
)

var rootCmd = &cobra.Command{
	Use:   "sm",
	Short: "Session Manager - manage processes across git worktrees",
	Long: `Session Manager (sm) is a CLI tool for managing development processes
across git worktrees. It provides a daemon-based architecture for running
multiple processes, switching between worktrees, and viewing logs.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		initLogger()
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		if logger != nil {
			logger.Sync()
		}
	},
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start daemon and processes, then attach TUI",
	Long: `Start the session manager daemon and all configured processes,
then attach the TUI for interactive management.`,
	RunE: runStart,
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop all processes and daemon",
	Long:  `Stop all running processes and shut down the daemon.`,
	RunE:  runStop,
}

var switchCmd = &cobra.Command{
	Use:   "switch <target>",
	Short: "Switch to different worktree",
	Long: `Switch to a different git worktree by branch name or path.
All processes will be restarted in the new worktree context.`,
	Args: cobra.ExactArgs(1),
	RunE: runSwitch,
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current state (non-TUI)",
	Long:  `Display the current session status including worktree and process information.`,
	RunE:  runStatus,
}

var attachCmd = &cobra.Command{
	Use:   "attach",
	Short: "Attach TUI to running daemon",
	Long:  `Connect to a running daemon and launch the interactive TUI.`,
	RunE:  runAttach,
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List available git worktrees",
	Long:  `List all git worktrees in the repository with their paths, branches, and HEAD commits.`,
	RunE:  runList,
}

var logsCmd = &cobra.Command{
	Use:   "logs <process>",
	Short: "Tail logs for a process",
	Long:  `View logs for a specific process. Use -f/--follow to continuously follow new output.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runLogs,
}

var addCmd = &cobra.Command{
	Use:   "add [target]",
	Short: "Add a new session for a worktree",
	Long: `Add a new session for a worktree. The session will be associated
with the specified worktree (by branch name or path). If no target is specified,
the current directory's worktree is used.

Sessions are identified by their worktree path. You can reference them by branch name.
Each session gets a unique index (0, 1, 2, ...) that can be used in config templates.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runAdd,
}

var sessionsCmd = &cobra.Command{
	Use:     "sessions",
	Aliases: []string{"list-sessions"},
	Short:   "List all sessions for this repository",
	Long:    `List all registered sessions for the current repository, showing their names, indices, and associated worktrees.`,
	RunE:    runSessions,
}

var routeCmd = &cobra.Command{
	Use:   "route <session>",
	Short: "Route ngrok tunnel to a session",
	Long: `Route the ngrok tunnel to a specific session's port. This restarts
the ngrok process pointing to the target session's port as defined in the config.

You can specify a session by:
  - Name: 'sm route myname'
  - Current worktree: 'sm route .'
  - Path to worktree: 'sm route /path/to/worktree'`,
	Args: cobra.ExactArgs(1),
	RunE: runRoute,
}

// daemonCmd is a hidden command used internally to run the daemon process
var daemonCmd = &cobra.Command{
	Use:    "daemon",
	Short:  "Run the daemon (internal use)",
	Hidden: true,
	RunE:   runDaemon,
}

// Daemon flags
var (
	daemonRepoPath     string
	daemonSocketPath   string
	daemonConfigPath   string
	daemonSessionIndex int
	daemonWorktreePath string
)

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose/debug logging")

	startCmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to config file")
	startCmd.Flags().StringVarP(&sessionFlag, "session", "s", "", "Branch name or worktree path (default: current worktree)")
	rootCmd.AddCommand(startCmd)

	stopCmd.Flags().StringVarP(&sessionFlag, "session", "s", "", "Branch name or worktree path (default: current worktree)")
	rootCmd.AddCommand(stopCmd)

	switchCmd.Flags().StringVarP(&sessionFlag, "session", "s", "", "Branch name or worktree path (default: current worktree)")
	rootCmd.AddCommand(switchCmd)

	statusCmd.Flags().StringVarP(&sessionFlag, "session", "s", "", "Branch name or worktree path (default: current worktree)")
	rootCmd.AddCommand(statusCmd)

	attachCmd.Flags().StringVarP(&sessionFlag, "session", "s", "", "Branch name or worktree path (default: current worktree)")
	rootCmd.AddCommand(attachCmd)

	rootCmd.AddCommand(listCmd)

	logsCmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output")
	logsCmd.Flags().StringVarP(&sessionFlag, "session", "s", "", "Branch name or worktree path (default: current worktree)")
	rootCmd.AddCommand(logsCmd)

	// Commands for multi-session support
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(sessionsCmd)
	rootCmd.AddCommand(routeCmd)

	// Hidden daemon command
	daemonCmd.Flags().StringVar(&daemonRepoPath, "repo", "", "Repository path")
	daemonCmd.Flags().StringVar(&daemonSocketPath, "socket", "", "Socket path")
	daemonCmd.Flags().StringVar(&daemonConfigPath, "config", "", "Config file path")
	daemonCmd.Flags().IntVar(&daemonSessionIndex, "session-index", 0, "Session index")
	daemonCmd.Flags().StringVar(&daemonWorktreePath, "worktree", "", "Worktree path for the session")
	daemonCmd.MarkFlagRequired("repo")
	daemonCmd.MarkFlagRequired("socket")
	daemonCmd.MarkFlagRequired("worktree")
	rootCmd.AddCommand(daemonCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func initLogger() {
	var cfg zap.Config
	if verbose {
		cfg = zap.NewDevelopmentConfig()
		cfg.Level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
	} else {
		cfg = zap.NewProductionConfig()
		cfg.Level = zap.NewAtomicLevelAt(zapcore.InfoLevel)
	}
	cfg.OutputPaths = []string{"stderr"}

	var err error
	logger, err = cfg.Build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
}

// resolveWorktree resolves the -s flag or current directory to a worktree path and branch.
func resolveWorktree(registry *session.Registry, mainWorktreePath, cwd string) (worktreePath, branch string, err error) {
	if sessionFlag != "" {
		// Try to find existing session by branch name first
		if entry := registry.FindSessionByBranch(sessionFlag); entry != nil {
			return entry.Worktree, entry.Branch, nil
		}
		// Try to resolve as a worktree path or branch name
		wt, err := worktree.Resolve(mainWorktreePath, sessionFlag)
		if err != nil {
			return "", "", fmt.Errorf("failed to resolve %q as branch or worktree: %w", sessionFlag, err)
		}
		return wt.Path, wt.Branch, nil
	}
	// Default to current worktree
	currentWt, err := worktree.Current()
	if err != nil {
		return cwd, "", nil
	}
	return currentWt.Path, currentWt.Branch, nil
}

func runStart(cmd *cobra.Command, args []string) error {
	// Load or discover configuration
	var cfg *config.Config
	var cfgPath string
	var err error

	if configPath != "" {
		cfg, err = config.Load(configPath)
		cfgPath = configPath
	} else {
		cfg, cfgPath, err = config.Discover()
	}
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	logger.Debug("loaded config", zap.String("path", cfgPath), zap.Int("processes", len(cfg.Processes)))

	// Find the main worktree path (consistent across all worktrees)
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	mainWorktreePath, err := worktree.FindMainWorktreePath(cwd)
	if err != nil {
		return fmt.Errorf("failed to find git repository: %w", err)
	}

	// Load or create registry
	registry, err := session.LoadRegistry(mainWorktreePath)
	if err != nil {
		return fmt.Errorf("failed to load session registry: %w", err)
	}

	// Resolve worktree from flag or current directory
	worktreePath, branch, err := resolveWorktree(registry, mainWorktreePath, cwd)
	if err != nil {
		return err
	}

	logger.Debug("starting session manager", zap.String("worktree", worktreePath), zap.String("branch", branch))

	// Get or create session entry in registry
	entry, exists := registry.GetSession(worktreePath)
	if !exists {
		entry, err = registry.AddSession(worktreePath, branch)
		if err != nil {
			return fmt.Errorf("failed to create session: %w", err)
		}
		if err := registry.Save(); err != nil {
			return fmt.Errorf("failed to save registry: %w", err)
		}
	}

	socketPath := registry.GetSessionSocketPath(worktreePath)
	sessionDir := registry.GetSessionDir(worktreePath)

	// Check if daemon is already running
	if isDaemonRunning(socketPath) {
		logger.Info("daemon already running, attaching to existing session")
		return attachTUI(socketPath)
	}

	// Start daemon in background
	logger.Info("starting daemon", zap.String("worktree", entry.Worktree), zap.Int("index", entry.Index))
	if err := startDaemonForWorktree(mainWorktreePath, cfgPath, socketPath, sessionDir, entry.Index, entry.Worktree); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Wait for daemon to be ready
	if err := waitForDaemon(socketPath, 10*time.Second); err != nil {
		return fmt.Errorf("daemon failed to start: %w", err)
	}

	// Attach TUI
	return attachTUI(socketPath)
}

func runStop(cmd *cobra.Command, args []string) error {
	logger.Debug("stopping session manager")

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	mainWorktreePath, err := worktree.FindMainWorktreePath(cwd)
	if err != nil {
		return fmt.Errorf("failed to find git repository: %w", err)
	}

	// Load registry to find session
	registry, err := session.LoadRegistry(mainWorktreePath)
	if err != nil {
		return fmt.Errorf("failed to load session registry: %w", err)
	}

	// Resolve worktree from flag or current directory
	worktreePath, _, err := resolveWorktree(registry, mainWorktreePath, cwd)
	if err != nil {
		return err
	}

	// Check if session exists
	entry := registry.FindSessionByWorktree(worktreePath)
	if entry == nil {
		fmt.Printf("No session found for worktree %q\n", worktreePath)
		return nil
	}

	socketPath := registry.GetSessionSocketPath(worktreePath)

	if !isDaemonRunning(socketPath) {
		fmt.Printf("No daemon running for worktree %q\n", worktreePath)
		return nil
	}

	// Connect to daemon using client
	client, err := daemon.Connect(socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer client.Close()

	// Send stop command
	if err := client.Stop(); err != nil {
		return fmt.Errorf("failed to stop daemon: %w", err)
	}

	// Remove session from registry
	if err := registry.RemoveSession(worktreePath); err != nil {
		logger.Warn("failed to remove session from registry", zap.Error(err))
	} else {
		if err := registry.Save(); err != nil {
			logger.Warn("failed to save registry", zap.Error(err))
		}
	}

	branchInfo := ""
	if entry.Branch != "" {
		branchInfo = fmt.Sprintf(" (branch: %s)", entry.Branch)
	}
	fmt.Printf("Session stopped and removed%s\n", branchInfo)
	return nil
}

func runSwitch(cmd *cobra.Command, args []string) error {
	target := args[0]

	// If target looks like a path, resolve it to absolute before sending to daemon
	if target == "." || target == ".." || filepath.IsAbs(target) || strings.Contains(target, string(filepath.Separator)) {
		absTarget, err := filepath.Abs(target)
		if err == nil {
			target = absTarget
		}
	}

	logger.Debug("switching worktree", zap.String("target", target))

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	mainWorktreePath, err := worktree.FindMainWorktreePath(cwd)
	if err != nil {
		return fmt.Errorf("failed to find git repository: %w", err)
	}

	// Load registry to find session
	registry, err := session.LoadRegistry(mainWorktreePath)
	if err != nil {
		return fmt.Errorf("failed to load session registry: %w", err)
	}

	// Resolve worktree from flag or current directory
	currentWorktree, _, err := resolveWorktree(registry, mainWorktreePath, cwd)
	if err != nil {
		return err
	}

	// Check if session exists
	entry := registry.FindSessionByWorktree(currentWorktree)
	if entry == nil {
		return fmt.Errorf("no session found for current worktree, start one first with 'sm start'")
	}

	socketPath := registry.GetSessionSocketPath(currentWorktree)

	if !isDaemonRunning(socketPath) {
		return fmt.Errorf("no daemon running, start it first with 'sm start'")
	}

	// Connect to daemon using client
	client, err := daemon.Connect(socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer client.Close()

	// Send switch command
	fmt.Printf("Switching to %s...\n", target)
	newWorktreePath, err := client.Switch(target)
	if err != nil {
		return fmt.Errorf("switch failed: %w", err)
	}

	// Update registry with the new branch if worktree changed
	if newWorktreePath != "" {
		// Resolve the new worktree to get branch info
		wt, err := worktree.Resolve(mainWorktreePath, newWorktreePath)
		if err == nil && wt.Branch != "" {
			if err := registry.UpdateSessionBranch(currentWorktree, wt.Branch); err != nil {
				logger.Warn("failed to update session branch in registry", zap.Error(err))
			} else {
				if err := registry.Save(); err != nil {
					logger.Warn("failed to save registry", zap.Error(err))
				}
			}
		}
	}

	fmt.Printf("Switched to %s\n", target)
	return nil
}

func runStatus(cmd *cobra.Command, args []string) error {
	logger.Debug("fetching status")

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	mainWorktreePath, err := worktree.FindMainWorktreePath(cwd)
	if err != nil {
		return fmt.Errorf("failed to find git repository: %w", err)
	}

	// Load registry to find session
	registry, err := session.LoadRegistry(mainWorktreePath)
	if err != nil {
		return fmt.Errorf("failed to load session registry: %w", err)
	}

	// Resolve worktree from flag or current directory
	worktreePath, _, err := resolveWorktree(registry, mainWorktreePath, cwd)
	if err != nil {
		return err
	}

	// Check if session exists
	entry := registry.FindSessionByWorktree(worktreePath)
	if entry == nil {
		return fmt.Errorf("no session found for worktree %q", worktreePath)
	}

	// Load the session to get state
	sess, err := session.LoadSessionForWorktree(mainWorktreePath, worktreePath)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}

	socketPath := registry.GetSessionSocketPath(worktreePath)
	state := sess.GetState()

	// Print status header
	fmt.Printf("Worktree:   %s\n", entry.Worktree)
	if entry.Branch != "" {
		fmt.Printf("Branch:     %s\n", entry.Branch)
	}
	fmt.Printf("Index:      %d\n", entry.Index)
	fmt.Printf("Session ID: %s\n", state.ID)
	fmt.Printf("Repository: %s\n", state.RepoPath)
	fmt.Printf("Started:    %s\n", state.StartedAt)
	fmt.Println()

	// Check daemon status
	daemonStatus := "stopped"
	if isDaemonRunning(socketPath) {
		daemonStatus = "running"
	}
	fmt.Printf("Daemon:     %s\n", daemonStatus)
	fmt.Println()

	// Print process table
	if len(state.Processes) > 0 {
		fmt.Println("Processes:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tPID\tSTATUS\tSTARTED")
		for _, proc := range state.Processes {
			pid := "-"
			if proc.PID > 0 {
				pid = fmt.Sprintf("%d", proc.PID)
			}
			startedAt := "-"
			if proc.StartedAt != "" {
				startedAt = proc.StartedAt
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", proc.Name, pid, proc.Status, startedAt)
		}
		w.Flush()
	} else {
		fmt.Println("No processes configured")
	}

	return nil
}

func runAttach(cmd *cobra.Command, args []string) error {
	logger.Debug("attaching to daemon")

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	mainWorktreePath, err := worktree.FindMainWorktreePath(cwd)
	if err != nil {
		return fmt.Errorf("failed to find git repository: %w", err)
	}

	// Load registry to find session
	registry, err := session.LoadRegistry(mainWorktreePath)
	if err != nil {
		return fmt.Errorf("failed to load session registry: %w", err)
	}

	// Resolve worktree from flag or current directory
	worktreePath, _, err := resolveWorktree(registry, mainWorktreePath, cwd)
	if err != nil {
		return err
	}

	// Check if session exists
	entry := registry.FindSessionByWorktree(worktreePath)
	if entry == nil {
		return fmt.Errorf("no session found, start one first with 'sm start'")
	}

	socketPath := registry.GetSessionSocketPath(worktreePath)

	if !isDaemonRunning(socketPath) {
		return fmt.Errorf("no daemon running, start it first with 'sm start'")
	}

	return attachTUI(socketPath)
}

func runList(cmd *cobra.Command, args []string) error {
	logger.Debug("listing worktrees")

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	repoRoot, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("failed to find git repository: %w", err)
	}

	worktrees, err := worktree.List(repoRoot)
	if err != nil {
		return fmt.Errorf("failed to list worktrees: %w", err)
	}

	if len(worktrees) == 0 {
		fmt.Println("No worktrees found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PATH\tBRANCH\tHEAD")
	for _, wt := range worktrees {
		branch := wt.Branch
		if branch == "" {
			branch = "(detached)"
		}
		head := wt.HEAD
		if len(head) > 8 {
			head = head[:8]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", wt.Path, branch, head)
	}
	w.Flush()

	return nil
}

func runLogs(cmd *cobra.Command, args []string) error {
	processName := args[0]
	logger.Debug("tailing logs", zap.String("process", processName), zap.Bool("follow", follow))

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	mainWorktreePath, err := worktree.FindMainWorktreePath(cwd)
	if err != nil {
		return fmt.Errorf("failed to find git repository: %w", err)
	}

	// Load registry to find session
	registry, err := session.LoadRegistry(mainWorktreePath)
	if err != nil {
		return fmt.Errorf("failed to load session registry: %w", err)
	}

	// Resolve worktree from flag or current directory
	worktreePath, _, err := resolveWorktree(registry, mainWorktreePath, cwd)
	if err != nil {
		return err
	}

	// Check if session exists
	entry := registry.FindSessionByWorktree(worktreePath)
	if entry == nil {
		return fmt.Errorf("no session found for worktree %q", worktreePath)
	}

	// Load session to get log directory
	sess, err := session.LoadSessionForWorktree(mainWorktreePath, worktreePath)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}

	logDir := sess.GetLogDir()
	logPath := filepath.Join(logDir, fmt.Sprintf("%s.log", processName))

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return fmt.Errorf("log file not found for process %q", processName)
	}

	if follow {
		return tailFollow(logPath)
	}

	// Just cat the file
	file, err := os.Open(logPath)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer file.Close()

	_, err = io.Copy(os.Stdout, file)
	return err
}

func runAdd(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	mainWorktreePath, err := worktree.FindMainWorktreePath(cwd)
	if err != nil {
		return fmt.Errorf("failed to find git repository: %w", err)
	}

	// Determine target based on args
	var target string
	switch len(args) {
	case 0:
		// sm add - use current worktree
		target = "."
	case 1:
		// sm add <target> - worktree target (branch name or path)
		target = args[0]
	}

	// Resolve the target worktree
	var worktreePath string
	var branch string
	if target == "." {
		// Use current worktree
		currentWt, err := worktree.Current()
		if err != nil {
			worktreePath = cwd
		} else {
			worktreePath = currentWt.Path
			branch = currentWt.Branch
		}
	} else {
		// Resolve target to a worktree
		wt, err := worktree.Resolve(mainWorktreePath, target)
		if err != nil {
			return fmt.Errorf("failed to resolve worktree %q: %w", target, err)
		}
		worktreePath = wt.Path
		branch = wt.Branch
	}

	logger.Debug("adding session", zap.String("worktree", worktreePath), zap.String("branch", branch))

	// Load or create registry
	registry, err := session.LoadRegistry(mainWorktreePath)
	if err != nil {
		return fmt.Errorf("failed to load session registry: %w", err)
	}

	// Check if session already exists
	var entry *session.SessionEntry
	existingEntry := registry.FindSessionByWorktree(worktreePath)
	if existingEntry != nil {
		entry = existingEntry
		branchInfo := ""
		if entry.Branch != "" {
			branchInfo = fmt.Sprintf(" (branch: %s)", entry.Branch)
		}
		fmt.Printf("Session already exists%s (index %d)\n", branchInfo, entry.Index)
	} else {
		// Add the session
		entry, err = registry.AddSession(worktreePath, branch)
		if err != nil {
			return fmt.Errorf("failed to add session: %w", err)
		}

		// Save registry
		if err := registry.Save(); err != nil {
			return fmt.Errorf("failed to save registry: %w", err)
		}

		branchInfo := ""
		if entry.Branch != "" {
			branchInfo = fmt.Sprintf(" (branch: %s)", entry.Branch)
		}
		fmt.Printf("Added session%s (index %d)\n", branchInfo, entry.Index)
	}

	// Load or discover config to start the daemon
	var cfg *config.Config
	var cfgPath string

	if configPath != "" {
		cfg, err = config.Load(configPath)
		cfgPath = configPath
	} else {
		// Try to discover config from current directory first
		cfg, cfgPath, err = config.Discover()
		if err != nil {
			// Fallback: try to discover from the session's worktree
			cfg, cfgPath, err = config.DiscoverFrom(entry.Worktree)
		}
		if err != nil {
			// Fallback: try to discover from the main repo path
			cfg, cfgPath, err = config.DiscoverFrom(mainWorktreePath)
		}
	}
	if err != nil {
		// No config found, can't start daemon
		fmt.Printf("No config found, session registered but daemon not started: %v\n", err)
		return nil
	}

	logger.Debug("found config", zap.String("path", cfgPath))

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	socketPath := registry.GetSessionSocketPath(entry.Worktree)
	sessionDir := registry.GetSessionDir(entry.Worktree)

	// Check if daemon is already running
	if isDaemonRunning(socketPath) {
		fmt.Println("Daemon already running")
		return nil
	}

	// Start daemon in background
	fmt.Println("Starting daemon...")
	if err := startDaemonForWorktree(mainWorktreePath, cfgPath, socketPath, sessionDir, entry.Index, entry.Worktree); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Wait for daemon to be ready
	if err := waitForDaemon(socketPath, 10*time.Second); err != nil {
		return fmt.Errorf("daemon failed to start: %w", err)
	}

	fmt.Println("Session is now running")
	return nil
}

func runSessions(cmd *cobra.Command, args []string) error {
	logger.Debug("listing sessions")

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	mainWorktreePath, err := worktree.FindMainWorktreePath(cwd)
	if err != nil {
		return fmt.Errorf("failed to find git repository: %w", err)
	}

	// Load registry
	registry, err := session.LoadRegistry(mainWorktreePath)
	if err != nil {
		return fmt.Errorf("failed to load session registry: %w", err)
	}

	sessions := registry.ListSessions()
	if len(sessions) == 0 {
		fmt.Println("No sessions registered")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "INDEX\tBRANCH\tWORKTREE\tSTATUS")
	for _, sess := range sessions {
		// Check if daemon is running
		socketPath := registry.GetSessionSocketPath(sess.Worktree)
		status := "stopped"
		if isDaemonRunning(socketPath) {
			status = "running"
		}
		branch := sess.Branch
		if branch == "" {
			branch = "-"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", sess.Index, branch, sess.Worktree, status)
	}
	w.Flush()

	return nil
}

func runRoute(cmd *cobra.Command, args []string) error {
	target := args[0]
	logger.Debug("routing ngrok to session", zap.String("target", target))

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	mainWorktreePath, err := worktree.FindMainWorktreePath(cwd)
	if err != nil {
		return fmt.Errorf("failed to find git repository: %w", err)
	}

	// Load registry
	registry, err := session.LoadRegistry(mainWorktreePath)
	if err != nil {
		return fmt.Errorf("failed to load session registry: %w", err)
	}

	// Resolve the target session (by branch name, worktree path, or current directory)
	var entry *session.SessionEntry
	if target == "." {
		// Find session by current worktree
		entry = registry.FindSessionByWorktree(cwd)
		if entry == nil {
			return fmt.Errorf("no session found for current worktree")
		}
	} else if filepath.IsAbs(target) || strings.Contains(target, string(filepath.Separator)) {
		// Target is a path, resolve to absolute and find session
		absTarget, err := filepath.Abs(target)
		if err != nil {
			return fmt.Errorf("failed to resolve path: %w", err)
		}
		entry = registry.FindSessionByWorktree(absTarget)
		if entry == nil {
			return fmt.Errorf("no session found for worktree %q", absTarget)
		}
	} else {
		// Target is a branch name - try to find by branch
		entry = registry.FindSessionByBranch(target)
		if entry == nil {
			return fmt.Errorf("no session found for branch %q", target)
		}
	}

	// Load or discover config (use same logic as start command)
	var cfg *config.Config
	var cfgPath string

	if configPath != "" {
		cfg, err = config.Load(configPath)
		cfgPath = configPath
	} else {
		cfg, cfgPath, err = config.Discover()
	}
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	_ = cfgPath // We don't need cfgPath for routing

	// Create template context for the target session
	templateCtx := config.TemplateContext{
		Index: entry.Index,
	}

	// Expand config with session's context
	expandedCfg, err := cfg.Expand(templateCtx)
	if err != nil {
		return fmt.Errorf("failed to expand config: %w", err)
	}

	// Check if ngrok is configured
	if expandedCfg.Ngrok == nil || expandedCfg.Ngrok.Port == "" {
		return fmt.Errorf("ngrok port is not configured in config")
	}

	// Evaluate the port template
	port, err := config.ExpandPortTemplate(expandedCfg, expandedCfg.Ngrok.Port, templateCtx)
	if err != nil {
		return fmt.Errorf("failed to resolve port: %w", err)
	}

	// Find the first running session to connect to (ngrok owner)
	// TODO: This should connect to whichever daemon owns ngrok
	sessions := registry.ListSessions()
	var ownerSocketPath string
	for _, sess := range sessions {
		sp := registry.GetSessionSocketPath(sess.Worktree)
		if isDaemonRunning(sp) {
			ownerSocketPath = sp
			break
		}
	}
	if ownerSocketPath == "" {
		return fmt.Errorf("no running daemon found, start one first with 'sm start'")
	}

	client, err := daemon.Connect(ownerSocketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer client.Close()

	// Send route command
	branchInfo := ""
	if entry.Branch != "" {
		branchInfo = fmt.Sprintf(" (branch: %s)", entry.Branch)
	}
	fmt.Printf("Routing ngrok to session%s (port %d)...\n", branchInfo, port)
	publicURL, err := client.Route(entry.Worktree, port)
	if err != nil {
		return fmt.Errorf("route failed: %w", err)
	}

	// Update registry with routed session
	registry.SetRoutedSession(entry.Worktree)
	if err := registry.Save(); err != nil {
		logger.Warn("failed to save registry", zap.Error(err))
	}

	fmt.Printf("Ngrok tunnel active:\n")
	if entry.Branch != "" {
		fmt.Printf("  Branch:     %s\n", entry.Branch)
	}
	fmt.Printf("  Worktree:   %s\n", entry.Worktree)
	fmt.Printf("  Local port: %d\n", port)
	fmt.Printf("  Public URL: %s\n", publicURL)

	return nil
}

// Helper functions

func isDaemonRunning(socketPath string) bool {
	conn, err := net.DialTimeout("unix", socketPath, 1*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func startDaemonForWorktree(repoRoot, configPath, socketPath, sessionDir string, sessIndex int, worktreePath string) error {
	// Get the path to the current executable
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Determine log file path in session directory
	daemonLogPath := filepath.Join(sessionDir, "daemon.log")

	// Open log file for daemon output
	logFile, err := os.OpenFile(daemonLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to create daemon log file: %w", err)
	}

	// Start daemon process
	cmdArgs := []string{
		"daemon",
		"--repo", repoRoot,
		"--socket", socketPath,
		"--session-index", fmt.Sprintf("%d", sessIndex),
		"--worktree", worktreePath,
		"-v",
	}
	if configPath != "" {
		cmdArgs = append(cmdArgs, "--config", configPath)
	}

	cmd := exec.Command(executable, cmdArgs...)
	cmd.Dir = repoRoot

	// Redirect output to log file
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Start in new process group so signals don't propagate from parent
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("failed to start daemon process: %w", err)
	}

	logger.Info("daemon started", zap.String("log", daemonLogPath))

	// Release the process so it runs independently
	// Note: we don't close logFile here - the daemon process owns it now
	if err := cmd.Process.Release(); err != nil {
		logger.Debug("failed to release daemon process", zap.Error(err))
	}

	return nil
}

func waitForDaemon(socketPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if isDaemonRunning(socketPath) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for daemon to start")
}

func attachTUI(socketPath string) error {
	// Connect to the daemon
	client, err := daemon.Connect(socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer client.Close()

	// Get initial status to set up panes
	status, err := client.Status()
	if err != nil {
		return fmt.Errorf("failed to get daemon status: %w", err)
	}

	// Create TUI model with initial status
	model := tui.NewWithStatus(client, status)

	// Run the TUI
	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

func tailFollow(logPath string) error {
	file, err := os.Open(logPath)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer file.Close()

	// Seek to end of file
	_, err = file.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("failed to seek to end of file: %w", err)
	}

	reader := bufio.NewReader(file)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				// No more data, wait and try again
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return fmt.Errorf("error reading log file: %w", err)
		}
		fmt.Print(line)
	}
}

// runDaemon runs the daemon process (internal command)
func runDaemon(cmd *cobra.Command, args []string) error {
	logger.Info("starting daemon",
		zap.String("repo", daemonRepoPath),
		zap.String("socket", daemonSocketPath),
		zap.String("config", daemonConfigPath),
		zap.String("worktree", daemonWorktreePath),
		zap.Int("session_index", daemonSessionIndex),
	)

	// Worktree path is required
	if daemonWorktreePath == "" {
		return fmt.Errorf("--worktree flag is required")
	}

	// Load configuration
	var cfg *config.Config
	var cfgPath string
	var err error

	if daemonConfigPath != "" {
		cfg, err = config.Load(daemonConfigPath)
		cfgPath = daemonConfigPath
	} else {
		cfg, cfgPath, err = config.Discover()
	}
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Create or load session for the worktree
	sess, err := session.NewSessionForWorktree(daemonRepoPath, daemonWorktreePath, daemonSessionIndex)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	// Resolve the worktree to get branch info
	wt, err := worktree.Resolve(daemonRepoPath, daemonWorktreePath)
	if err != nil {
		logger.Warn("could not resolve worktree, using path directly", zap.Error(err))
		sess.UpdateWorktree(daemonWorktreePath, "")
	} else {
		sess.UpdateWorktree(wt.Path, wt.Branch)
	}

	// Create the daemon using the daemon package
	d, err := daemon.New(sess, cfg, cfgPath, daemonSocketPath, logger)
	if err != nil {
		return fmt.Errorf("failed to create daemon: %w", err)
	}

	// Handle shutdown signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info("received signal, shutting down", zap.String("signal", sig.String()))
		cancel()
	}()

	// Run the daemon (blocks until context is cancelled)
	if err := d.Run(ctx); err != nil && err != context.Canceled {
		return fmt.Errorf("daemon error: %w", err)
	}

	logger.Info("daemon stopped")
	return nil
}
