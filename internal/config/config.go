package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// Config represents the session manager configuration
type Config struct {
	EnvFile   string          `yaml:"env_file,omitempty" json:"env_file,omitempty"`
	Processes []ProcessConfig `yaml:"processes" json:"processes"`
	Ngrok     *NgrokConfig    `yaml:"ngrok,omitempty" json:"ngrok,omitempty"`
}

// NgrokConfig represents ngrok tunnel configuration.
// Now supports a single tunnel that can be dynamically routed to different sessions.
type NgrokConfig struct {
	AuthToken string `yaml:"auth_token,omitempty" json:"auth_token,omitempty"`
	Region    string `yaml:"region,omitempty" json:"region,omitempty"`
	Subdomain string `yaml:"subdomain,omitempty" json:"subdomain,omitempty"` // Static subdomain (URL stays same)
	Port      string `yaml:"port" json:"port"`                               // Template like "${processes.dashboard.env.PORT}"
}

// ProcessConfig defines a process to manage
type ProcessConfig struct {
	Name    string            `yaml:"name" json:"name"`
	Command string            `yaml:"command" json:"command"`
	Cwd     string            `yaml:"cwd" json:"cwd"`
	EnvFile string            `yaml:"env_file,omitempty" json:"env_file,omitempty"`
	Env     map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

// configFileNames are the supported config file names in order of preference
var configFileNames = []string{"sm.yaml", "sm.yml", "sm.json"}

// Load loads configuration from a specific path
func Load(path string) (*Config, error) {
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("config file not found: %s", path)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse YAML config: %w", err)
		}
	case ".json":
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse JSON config: %w", err)
		}
	default:
		// Try viper for other formats
		v := viper.New()
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		if err := v.Unmarshal(&cfg); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config: %w", err)
		}
	}

	logger.Debug("loaded config", zap.String("path", path), zap.Int("processes", len(cfg.Processes)))

	return &cfg, nil
}

// Discover finds and loads config from cwd walking up to git root
// Returns the config and the path where it was found
func Discover() (*Config, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get current working directory: %w", err)
	}
	return DiscoverFrom(cwd)
}

// DiscoverFrom finds and loads config from a given directory walking up to git root
// Returns the config and the path where it was found
func DiscoverFrom(startDir string) (*Config, string, error) {
	gitRoot, err := findGitRoot(startDir)
	if err != nil {
		gitRoot = "/" // Fall back to filesystem root
	}

	configPath, err := findConfigFile(startDir, gitRoot)
	if err != nil {
		return nil, "", err
	}

	cfg, err := Load(configPath)
	if err != nil {
		return nil, "", err
	}

	return cfg, configPath, nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if len(c.Processes) == 0 {
		return errors.New("no processes defined in config")
	}

	var errs []string
	for i, proc := range c.Processes {
		if proc.Name == "" {
			errs = append(errs, fmt.Sprintf("process[%d]: name is required", i))
		}
		if proc.Command == "" {
			errs = append(errs, fmt.Sprintf("process[%d]: command is required", i))
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}

	return nil
}

// findGitRoot finds the root of the git repository starting from the given path
func findGitRoot(startPath string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = startPath
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// findConfigFile searches for a config file starting from startPath up to stopPath
func findConfigFile(startPath, stopPath string) (string, error) {
	currentPath := startPath

	for {
		// Check for each config file name in the current directory
		for _, name := range configFileNames {
			configPath := filepath.Join(currentPath, name)
			if _, err := os.Stat(configPath); err == nil {
				return configPath, nil
			}
		}

		// Check if we've reached the stop path
		if currentPath == stopPath {
			break
		}

		// Move up one directory
		parentPath := filepath.Dir(currentPath)
		if parentPath == currentPath {
			// Reached filesystem root
			break
		}
		currentPath = parentPath
	}

	return "", errors.New("no config file found (searched for sm.yaml, sm.yml, sm.json)")
}

// LoadFromJSON loads configuration from a JSON string (useful for testing)
func LoadFromJSON(data string) (*Config, error) {
	var cfg Config
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse JSON config: %w", err)
	}
	return &cfg, nil
}

// ParseEnvFile parses an envrc-style file and returns a map of environment variables.
// Supports:
//   - KEY=value
//   - KEY="value with spaces"
//   - KEY='value with spaces'
//   - export KEY=value
//   - Comments starting with #
//   - Empty lines
func ParseEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read env file: %w", err)
	}

	env := make(map[string]string)
	lines := strings.Split(string(data), "\n")

	for lineNum, line := range lines {
		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Remove "export " prefix if present
		line = strings.TrimPrefix(line, "export ")
		line = strings.TrimSpace(line)

		// Skip lines that don't look like assignments
		if !strings.Contains(line, "=") {
			continue
		}

		// Split on first =
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Validate key (must be valid env var name)
		if key == "" || !isValidEnvKey(key) {
			continue
		}

		// Remove surrounding quotes from value
		value = unquote(value)

		env[key] = value
		_ = lineNum // Available for debug logging if needed
	}

	return env, nil
}

// isValidEnvKey checks if a string is a valid environment variable name
func isValidEnvKey(key string) bool {
	for i, r := range key {
		if i == 0 {
			if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_') {
				return false
			}
		} else {
			if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
				return false
			}
		}
	}
	return len(key) > 0
}

// unquote removes surrounding quotes from a string
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// GetEnv returns the merged environment variables for a process.
// Priority (highest to lowest):
//  1. Process-level env (ProcessConfig.Env)
//  2. Process-level env_file (ProcessConfig.EnvFile)
//  3. Global env_file (Config.EnvFile)
//
// The basePath is the directory containing the config file, used to resolve relative env_file paths.
func (c *Config) GetEnv(proc ProcessConfig, basePath string) (map[string]string, error) {
	env := make(map[string]string)

	// Load global env_file first (lowest priority)
	if c.EnvFile != "" {
		envFilePath := c.EnvFile
		if !filepath.IsAbs(envFilePath) {
			envFilePath = filepath.Join(basePath, envFilePath)
		}
		fileEnv, err := ParseEnvFile(envFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load global env_file %q: %w", c.EnvFile, err)
		}
		for k, v := range fileEnv {
			env[k] = v
		}
	}

	// Load process-level env_file (medium priority)
	if proc.EnvFile != "" {
		envFilePath := proc.EnvFile
		if !filepath.IsAbs(envFilePath) {
			envFilePath = filepath.Join(basePath, envFilePath)
		}
		fileEnv, err := ParseEnvFile(envFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load env_file %q for process %q: %w", proc.EnvFile, proc.Name, err)
		}
		for k, v := range fileEnv {
			env[k] = v
		}
	}

	// Apply process-level env (highest priority)
	for k, v := range proc.Env {
		env[k] = v
	}

	return env, nil
}
