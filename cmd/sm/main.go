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
	verbose    bool
	configPath string
	follow     bool
	logger     *zap.Logger
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

// daemonCmd is a hidden command used internally to run the daemon process
var daemonCmd = &cobra.Command{
	Use:    "daemon",
	Short:  "Run the daemon (internal use)",
	Hidden: true,
	RunE:   runDaemon,
}

// Daemon flags
var (
	daemonRepoPath   string
	daemonSocketPath string
	daemonConfigPath string
)

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose/debug logging")

	startCmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to config file")
	rootCmd.AddCommand(startCmd)

	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(switchCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(attachCmd)
	rootCmd.AddCommand(listCmd)

	logsCmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output")
	rootCmd.AddCommand(logsCmd)

	// Hidden daemon command
	daemonCmd.Flags().StringVar(&daemonRepoPath, "repo", "", "Repository path")
	daemonCmd.Flags().StringVar(&daemonSocketPath, "socket", "", "Socket path")
	daemonCmd.Flags().StringVar(&daemonConfigPath, "config", "", "Config file path")
	daemonCmd.MarkFlagRequired("repo")
	daemonCmd.MarkFlagRequired("socket")
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

func runStart(cmd *cobra.Command, args []string) error {
	logger.Debug("starting session manager")

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

	// Find the repository root
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	repoRoot, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("failed to find git repository: %w", err)
	}

	// Get or create session
	sess, err := session.NewSession(repoRoot)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	socketPath := sess.GetSocketPath()

	// Check if daemon is already running
	if isDaemonRunning(socketPath) {
		logger.Info("daemon already running, attaching to existing session")
		return attachTUI(socketPath)
	}

	// Start daemon in background
	logger.Info("starting daemon")
	if err := startDaemon(repoRoot, cfgPath, socketPath); err != nil {
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

	repoRoot, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("failed to find git repository: %w", err)
	}

	sess, err := session.LoadSession(repoRoot)
	if err != nil {
		return fmt.Errorf("no active session found: %w", err)
	}

	socketPath := sess.GetSocketPath()

	if !isDaemonRunning(socketPath) {
		fmt.Println("No daemon running")
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

	fmt.Println("Session manager stopped")
	return nil
}

func runSwitch(cmd *cobra.Command, args []string) error {
	target := args[0]
	logger.Debug("switching worktree", zap.String("target", target))

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	repoRoot, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("failed to find git repository: %w", err)
	}

	sess, err := session.LoadSession(repoRoot)
	if err != nil {
		return fmt.Errorf("no active session found: %w", err)
	}

	socketPath := sess.GetSocketPath()

	if !isDaemonRunning(socketPath) {
		return fmt.Errorf("no daemon running, start session first with 'sm start'")
	}

	// Connect to daemon using client
	client, err := daemon.Connect(socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer client.Close()

	// Send switch command
	fmt.Printf("Switching to %s...\n", target)
	if err := client.Switch(target); err != nil {
		return fmt.Errorf("switch failed: %w", err)
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

	repoRoot, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("failed to find git repository: %w", err)
	}

	sess, err := session.LoadSession(repoRoot)
	if err != nil {
		return fmt.Errorf("no active session found: %w", err)
	}

	socketPath := sess.GetSocketPath()
	state := sess.GetState()

	// Print status header
	fmt.Printf("Session ID: %s\n", state.ID)
	fmt.Printf("Repository: %s\n", state.RepoPath)
	fmt.Printf("Started:    %s\n", state.StartedAt)
	fmt.Println()

	if state.CurrentWorktree != "" {
		fmt.Printf("Worktree:   %s\n", state.CurrentWorktree)
	}
	if state.CurrentBranch != "" {
		fmt.Printf("Branch:     %s\n", state.CurrentBranch)
	}
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

	repoRoot, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("failed to find git repository: %w", err)
	}

	sess, err := session.LoadSession(repoRoot)
	if err != nil {
		return fmt.Errorf("no active session found: %w", err)
	}

	socketPath := sess.GetSocketPath()

	if !isDaemonRunning(socketPath) {
		return fmt.Errorf("no daemon running, start session first with 'sm start'")
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

	repoRoot, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("failed to find git repository: %w", err)
	}

	sess, err := session.LoadSession(repoRoot)
	if err != nil {
		return fmt.Errorf("no active session found: %w", err)
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

// Helper functions

func isDaemonRunning(socketPath string) bool {
	conn, err := net.DialTimeout("unix", socketPath, 1*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func startDaemon(repoRoot, configPath, socketPath string) error {
	// Get the path to the current executable
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Determine log file path (same directory as socket)
	sessionDir := filepath.Dir(socketPath)
	daemonLogPath := filepath.Join(sessionDir, "daemon.log")

	// Open log file for daemon output
	logFile, err := os.OpenFile(daemonLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to create daemon log file: %w", err)
	}

	// Start daemon process
	cmdArgs := []string{"daemon", "--repo", repoRoot, "--socket", socketPath, "-v"}
	if configPath != "" {
		cmdArgs = append(cmdArgs, "--config", configPath)
	}

	daemonCmd := exec.Command(executable, cmdArgs...)
	daemonCmd.Dir = repoRoot

	// Redirect output to log file
	daemonCmd.Stdin = nil
	daemonCmd.Stdout = logFile
	daemonCmd.Stderr = logFile

	// Start in new process group
	if err := daemonCmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("failed to start daemon process: %w", err)
	}

	logger.Info("daemon started", zap.String("log", daemonLogPath))

	// Release the process so it runs independently
	// Note: we don't close logFile here - the daemon process owns it now
	if err := daemonCmd.Process.Release(); err != nil {
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
	)

	// Load configuration
	var cfg *config.Config
	var err error

	if daemonConfigPath != "" {
		cfg, err = config.Load(daemonConfigPath)
	} else {
		cfg, _, err = config.Discover()
	}
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Create or load session
	sess, err := session.NewSession(daemonRepoPath)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	// Get current worktree and update session
	currentWorktree, err := worktree.Current()
	if err != nil {
		logger.Warn("could not determine current worktree, using repo path", zap.Error(err))
		// Fallback to repo path - still need a worktree to start processes
		sess.UpdateWorktree(daemonRepoPath, "")
	} else {
		sess.UpdateWorktree(currentWorktree.Path, currentWorktree.Branch)
	}

	// Create the daemon using the daemon package
	d, err := daemon.New(sess, cfg, logger)
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
