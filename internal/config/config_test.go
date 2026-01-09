package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadYAML(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a valid YAML config file
	yamlContent := `processes:
  - name: web
    command: npm run dev
    cwd: /app/web
    env:
      PORT: "3000"
      NODE_ENV: development
  - name: api
    command: go run main.go
    cwd: /app/api
`
	configPath := filepath.Join(tmpDir, "sm.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// Load the config
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Validate the loaded config
	if len(cfg.Processes) != 2 {
		t.Errorf("expected 2 processes, got %d", len(cfg.Processes))
	}

	// Check first process
	if cfg.Processes[0].Name != "web" {
		t.Errorf("expected first process name to be 'web', got '%s'", cfg.Processes[0].Name)
	}
	if cfg.Processes[0].Command != "npm run dev" {
		t.Errorf("expected first process command to be 'npm run dev', got '%s'", cfg.Processes[0].Command)
	}
	if cfg.Processes[0].Cwd != "/app/web" {
		t.Errorf("expected first process cwd to be '/app/web', got '%s'", cfg.Processes[0].Cwd)
	}
	if cfg.Processes[0].Env["PORT"] != "3000" {
		t.Errorf("expected PORT env to be '3000', got '%s'", cfg.Processes[0].Env["PORT"])
	}
	if cfg.Processes[0].Env["NODE_ENV"] != "development" {
		t.Errorf("expected NODE_ENV env to be 'development', got '%s'", cfg.Processes[0].Env["NODE_ENV"])
	}

	// Check second process
	if cfg.Processes[1].Name != "api" {
		t.Errorf("expected second process name to be 'api', got '%s'", cfg.Processes[1].Name)
	}
	if cfg.Processes[1].Command != "go run main.go" {
		t.Errorf("expected second process command to be 'go run main.go', got '%s'", cfg.Processes[1].Command)
	}
}

func TestLoadJSON(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a valid JSON config file
	jsonContent := `{
  "processes": [
    {
      "name": "frontend",
      "command": "yarn start",
      "cwd": "/app/frontend",
      "env": {
        "REACT_APP_API_URL": "http://localhost:8080"
      }
    },
    {
      "name": "backend",
      "command": "python manage.py runserver",
      "cwd": "/app/backend"
    }
  ]
}`
	configPath := filepath.Join(tmpDir, "sm.json")
	if err := os.WriteFile(configPath, []byte(jsonContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// Load the config
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Validate the loaded config
	if len(cfg.Processes) != 2 {
		t.Errorf("expected 2 processes, got %d", len(cfg.Processes))
	}

	// Check first process
	if cfg.Processes[0].Name != "frontend" {
		t.Errorf("expected first process name to be 'frontend', got '%s'", cfg.Processes[0].Name)
	}
	if cfg.Processes[0].Command != "yarn start" {
		t.Errorf("expected first process command to be 'yarn start', got '%s'", cfg.Processes[0].Command)
	}
	if cfg.Processes[0].Env["REACT_APP_API_URL"] != "http://localhost:8080" {
		t.Errorf("expected REACT_APP_API_URL env to be 'http://localhost:8080', got '%s'", cfg.Processes[0].Env["REACT_APP_API_URL"])
	}

	// Check second process
	if cfg.Processes[1].Name != "backend" {
		t.Errorf("expected second process name to be 'backend', got '%s'", cfg.Processes[1].Name)
	}
	if cfg.Processes[1].Cwd != "/app/backend" {
		t.Errorf("expected second process cwd to be '/app/backend', got '%s'", cfg.Processes[1].Cwd)
	}
}

func TestDiscoverWalksUpDirectories(t *testing.T) {
	// Create a temporary directory structure
	tmpDir, err := os.MkdirTemp("", "config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a nested directory structure
	nestedDir := filepath.Join(tmpDir, "a", "b", "c")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("failed to create nested dirs: %v", err)
	}

	// Create config file at root level
	yamlContent := `processes:
  - name: test
    command: echo hello
`
	configPath := filepath.Join(tmpDir, "sm.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// Change to nested directory
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current dir: %v", err)
	}
	defer os.Chdir(originalDir)

	if err := os.Chdir(nestedDir); err != nil {
		t.Fatalf("failed to change to nested dir: %v", err)
	}

	// Test findConfigFile directly since Discover uses git root
	foundPath, err := findConfigFile(nestedDir, tmpDir)
	if err != nil {
		t.Fatalf("failed to find config file: %v", err)
	}

	if foundPath != configPath {
		t.Errorf("expected config path '%s', got '%s'", configPath, foundPath)
	}

	// Load the config from the found path
	cfg, err := Load(foundPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Processes[0].Name != "test" {
		t.Errorf("expected process name 'test', got '%s'", cfg.Processes[0].Name)
	}
}

func TestDiscoverPrefersCloserConfig(t *testing.T) {
	// Create a temporary directory structure
	tmpDir, err := os.MkdirTemp("", "config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a nested directory structure
	nestedDir := filepath.Join(tmpDir, "a", "b")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("failed to create nested dirs: %v", err)
	}

	// Create config file at root level
	rootConfig := `processes:
  - name: root
    command: echo root
`
	if err := os.WriteFile(filepath.Join(tmpDir, "sm.yaml"), []byte(rootConfig), 0644); err != nil {
		t.Fatalf("failed to write root config file: %v", err)
	}

	// Create config file at nested level
	nestedConfig := `processes:
  - name: nested
    command: echo nested
`
	nestedConfigPath := filepath.Join(nestedDir, "sm.yaml")
	if err := os.WriteFile(nestedConfigPath, []byte(nestedConfig), 0644); err != nil {
		t.Fatalf("failed to write nested config file: %v", err)
	}

	// Test findConfigFile - should find the closer (nested) config
	foundPath, err := findConfigFile(nestedDir, tmpDir)
	if err != nil {
		t.Fatalf("failed to find config file: %v", err)
	}

	if foundPath != nestedConfigPath {
		t.Errorf("expected nested config path '%s', got '%s'", nestedConfigPath, foundPath)
	}
}

func TestConfigFileNamePriority(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	yamlContent := `processes:
  - name: yaml
    command: echo yaml
`
	ymlContent := `processes:
  - name: yml
    command: echo yml
`
	jsonContent := `{"processes": [{"name": "json", "command": "echo json"}]}`

	// Create all three config files
	if err := os.WriteFile(filepath.Join(tmpDir, "sm.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write sm.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "sm.yml"), []byte(ymlContent), 0644); err != nil {
		t.Fatalf("failed to write sm.yml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "sm.json"), []byte(jsonContent), 0644); err != nil {
		t.Fatalf("failed to write sm.json: %v", err)
	}

	// sm.yaml should be preferred
	foundPath, err := findConfigFile(tmpDir, tmpDir)
	if err != nil {
		t.Fatalf("failed to find config file: %v", err)
	}

	expectedPath := filepath.Join(tmpDir, "sm.yaml")
	if foundPath != expectedPath {
		t.Errorf("expected sm.yaml to be preferred, got '%s'", foundPath)
	}
}

func TestValidateMissingName(t *testing.T) {
	cfg := &Config{
		Processes: []ProcessConfig{
			{
				Name:    "",
				Command: "echo hello",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for missing name")
	}
	if err != nil && !contains(err.Error(), "name is required") {
		t.Errorf("expected error message to contain 'name is required', got '%s'", err.Error())
	}
}

func TestValidateMissingCommand(t *testing.T) {
	cfg := &Config{
		Processes: []ProcessConfig{
			{
				Name:    "test",
				Command: "",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for missing command")
	}
	if err != nil && !contains(err.Error(), "command is required") {
		t.Errorf("expected error message to contain 'command is required', got '%s'", err.Error())
	}
}

func TestValidateMissingBothNameAndCommand(t *testing.T) {
	cfg := &Config{
		Processes: []ProcessConfig{
			{
				Name:    "",
				Command: "",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for missing name and command")
	}
	if err != nil {
		if !contains(err.Error(), "name is required") {
			t.Errorf("expected error message to contain 'name is required', got '%s'", err.Error())
		}
		if !contains(err.Error(), "command is required") {
			t.Errorf("expected error message to contain 'command is required', got '%s'", err.Error())
		}
	}
}

func TestValidateNoProcesses(t *testing.T) {
	cfg := &Config{
		Processes: []ProcessConfig{},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for no processes")
	}
	if err != nil && !contains(err.Error(), "no processes defined") {
		t.Errorf("expected error message to contain 'no processes defined', got '%s'", err.Error())
	}
}

func TestValidateValidConfig(t *testing.T) {
	cfg := &Config{
		Processes: []ProcessConfig{
			{
				Name:    "web",
				Command: "npm start",
				Cwd:     "/app",
			},
			{
				Name:    "api",
				Command: "go run main.go",
			},
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf("expected no validation error, got '%s'", err.Error())
	}
}

func TestLoadNonExistentFile(t *testing.T) {
	_, err := Load("/nonexistent/path/sm.yaml")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
	if err != nil && !contains(err.Error(), "not found") {
		t.Errorf("expected error message to contain 'not found', got '%s'", err.Error())
	}
}

func TestLoadFromJSON(t *testing.T) {
	jsonData := `{
		"processes": [
			{
				"name": "test",
				"command": "echo test",
				"cwd": "/tmp",
				"env": {"KEY": "value"}
			}
		]
	}`

	cfg, err := LoadFromJSON(jsonData)
	if err != nil {
		t.Fatalf("failed to load from JSON: %v", err)
	}

	if len(cfg.Processes) != 1 {
		t.Errorf("expected 1 process, got %d", len(cfg.Processes))
	}

	if cfg.Processes[0].Name != "test" {
		t.Errorf("expected name 'test', got '%s'", cfg.Processes[0].Name)
	}

	if cfg.Processes[0].Env["KEY"] != "value" {
		t.Errorf("expected env KEY='value', got '%s'", cfg.Processes[0].Env["KEY"])
	}
}

func TestLoadFromJSONInvalid(t *testing.T) {
	invalidJSON := `{invalid json}`

	_, err := LoadFromJSON(invalidJSON)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
