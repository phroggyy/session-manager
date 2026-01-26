package config

import (
	"testing"
)

func TestExpandString(t *testing.T) {
	ctx := TemplateContext{Index: 2, Name: "feature-auth"}

	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name:     "simple index",
			input:    "${index}",
			expected: "2",
		},
		{
			name:     "simple name",
			input:    "${name}",
			expected: "feature-auth",
		},
		{
			name:     "arithmetic addition",
			input:    "${3000 + index}",
			expected: "3002",
		},
		{
			name:     "arithmetic with multiplication",
			input:    "${8080 + index * 10}",
			expected: "8100",
		},
		{
			name:     "embedded in URL",
			input:    "http://localhost:${8080 + index}",
			expected: "http://localhost:8082",
		},
		{
			name:     "multiple substitutions",
			input:    "PORT=${3000 + index} NAME=${name}",
			expected: "PORT=3002 NAME=feature-auth",
		},
		{
			name:     "no substitution",
			input:    "plain string",
			expected: "plain string",
		},
		{
			name:     "parentheses in expression",
			input:    "${(2 + 3) * index}",
			expected: "10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := expandString(tt.input, ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("expandString() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if result != tt.expected {
				t.Errorf("expandString() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestEvaluateArithmetic(t *testing.T) {
	ctx := TemplateContext{Index: 5, Name: "test"}

	tests := []struct {
		name     string
		expr     string
		expected int
		wantErr  bool
	}{
		{"simple number", "42", 42, false},
		{"addition", "10 + 5", 15, false},
		{"subtraction", "20 - 8", 12, false},
		{"multiplication", "6 * 7", 42, false},
		{"division", "100 / 4", 25, false},
		{"index variable", "index", 5, false},
		{"index in expression", "3000 + index", 3005, false},
		{"precedence", "2 + 3 * 4", 14, false},
		{"parentheses", "(2 + 3) * 4", 20, false},
		{"complex expression", "8080 + index * 10", 8130, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := evaluateArithmetic(tt.expr, ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("evaluateArithmetic() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if result != tt.expected {
				t.Errorf("evaluateArithmetic() = %d, want %d", result, tt.expected)
			}
		})
	}
}

func TestConfigExpand(t *testing.T) {
	ctx := TemplateContext{Index: 1, Name: "session1"}

	cfg := &Config{
		EnvFile: ".env.${name}",
		Processes: []ProcessConfig{
			{
				Name:    "server",
				Command: "make run",
				Env: map[string]string{
					"PORT":    "${8080 + index}",
					"SESSION": "${name}",
				},
			},
		},
		Ngrok: &NgrokConfig{
			AuthToken: "token123",
			Port:      "${3000 + index}",
			Subdomain: "app-${name}",
		},
	}

	expanded, err := cfg.Expand(ctx)
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	if expanded.EnvFile != ".env.session1" {
		t.Errorf("EnvFile = %q, want %q", expanded.EnvFile, ".env.session1")
	}

	if len(expanded.Processes) != 1 {
		t.Fatalf("expected 1 process, got %d", len(expanded.Processes))
	}

	proc := expanded.Processes[0]
	if proc.Env["PORT"] != "8081" {
		t.Errorf("PORT = %q, want %q", proc.Env["PORT"], "8081")
	}
	if proc.Env["SESSION"] != "session1" {
		t.Errorf("SESSION = %q, want %q", proc.Env["SESSION"], "session1")
	}

	if expanded.Ngrok == nil {
		t.Fatal("Ngrok config should not be nil")
	}
	// Port is NOT expanded during config expansion - it's expanded at routing time
	if expanded.Ngrok.Port != "${3000 + index}" {
		t.Errorf("Ngrok.Port = %q, want %q (should not be expanded)", expanded.Ngrok.Port, "${3000 + index}")
	}
	if expanded.Ngrok.Subdomain != "app-session1" {
		t.Errorf("Ngrok.Subdomain = %q, want %q", expanded.Ngrok.Subdomain, "app-session1")
	}
}

func TestExpandPortTemplate(t *testing.T) {
	ctx := TemplateContext{Index: 1, Name: "session1"}

	// Config with a process that has PORT env var
	cfg := &Config{
		Processes: []ProcessConfig{
			{
				Name:    "dashboard",
				Command: "make run",
				Env: map[string]string{
					"PORT": "3001",
				},
			},
			{
				Name:    "api",
				Command: "make run-api",
				Env: map[string]string{
					"PORT": "4001",
				},
			},
		},
	}

	tests := []struct {
		name         string
		portTemplate string
		expected     int
		wantErr      bool
	}{
		{
			name:         "process by name",
			portTemplate: "${processes.dashboard.env.PORT}",
			expected:     3001,
		},
		{
			name:         "process by index 0",
			portTemplate: "${processes.0.env.PORT}",
			expected:     3001,
		},
		{
			name:         "process by index 1",
			portTemplate: "${processes.1.env.PORT}",
			expected:     4001,
		},
		{
			name:         "arithmetic expression",
			portTemplate: "${3000 + index}",
			expected:     3001,
		},
		{
			name:         "plain number",
			portTemplate: "8080",
			expected:     8080,
		},
		{
			name:         "invalid process name",
			portTemplate: "${processes.nonexistent.env.PORT}",
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExpandPortTemplate(cfg, tt.portTemplate, ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExpandPortTemplate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && result != tt.expected {
				t.Errorf("ExpandPortTemplate() = %d, want %d", result, tt.expected)
			}
		})
	}
}
