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
	Processes []ProcessConfig `yaml:"processes" json:"processes"`
}

// ProcessConfig defines a process to manage
type ProcessConfig struct {
	Name    string            `yaml:"name" json:"name"`
	Command string            `yaml:"command" json:"command"`
	Cwd     string            `yaml:"cwd" json:"cwd"`
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
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get current working directory: %w", err)
	}

	gitRoot, err := findGitRoot(cwd)
	if err != nil {
		logger.Debug("no git root found, will search up to filesystem root", zap.Error(err))
		gitRoot = "/" // Fall back to filesystem root
	}

	configPath, err := findConfigFile(cwd, gitRoot)
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
