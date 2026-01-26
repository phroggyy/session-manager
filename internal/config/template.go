package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// TemplateContext holds the variables available for template expansion.
type TemplateContext struct {
	Index int
	Name  string
}

// templateVarRegex matches ${...} patterns
var templateVarRegex = regexp.MustCompile(`\$\{([^}]+)\}`)

// Expand expands all template variables in the config and returns a new config.
func (c *Config) Expand(ctx TemplateContext) (*Config, error) {
	expanded := &Config{
		EnvFile:   c.EnvFile,
		Processes: make([]ProcessConfig, len(c.Processes)),
	}

	// Expand env_file path
	if c.EnvFile != "" {
		expandedEnvFile, err := expandString(c.EnvFile, ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to expand env_file: %w", err)
		}
		expanded.EnvFile = expandedEnvFile
	}

	// Expand each process config
	for i, proc := range c.Processes {
		expandedProc, err := expandProcessConfig(proc, ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to expand process %q: %w", proc.Name, err)
		}
		expanded.Processes[i] = expandedProc
	}

	// Expand ngrok config if present
	if c.Ngrok != nil {
		expandedNgrok, err := expandNgrokConfig(*c.Ngrok, ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to expand ngrok config: %w", err)
		}
		expanded.Ngrok = &expandedNgrok
	}

	return expanded, nil
}

// expandProcessConfig expands template variables in a process config.
func expandProcessConfig(proc ProcessConfig, ctx TemplateContext) (ProcessConfig, error) {
	expanded := ProcessConfig{
		Name: proc.Name,
	}

	var err error

	// Expand command
	expanded.Command, err = expandString(proc.Command, ctx)
	if err != nil {
		return expanded, fmt.Errorf("command: %w", err)
	}

	// Expand cwd
	if proc.Cwd != "" {
		expanded.Cwd, err = expandString(proc.Cwd, ctx)
		if err != nil {
			return expanded, fmt.Errorf("cwd: %w", err)
		}
	}

	// Expand env_file
	if proc.EnvFile != "" {
		expanded.EnvFile, err = expandString(proc.EnvFile, ctx)
		if err != nil {
			return expanded, fmt.Errorf("env_file: %w", err)
		}
	}

	// Expand env values
	if len(proc.Env) > 0 {
		expanded.Env = make(map[string]string, len(proc.Env))
		for k, v := range proc.Env {
			expandedValue, err := expandString(v, ctx)
			if err != nil {
				return expanded, fmt.Errorf("env[%s]: %w", k, err)
			}
			expanded.Env[k] = expandedValue
		}
	}

	return expanded, nil
}

// expandNgrokConfig expands template variables in ngrok config.
func expandNgrokConfig(ngrok NgrokConfig, ctx TemplateContext) (NgrokConfig, error) {
	expanded := NgrokConfig{
		Region: ngrok.Region,
	}

	var err error

	// Expand auth token (may reference env vars)
	if ngrok.AuthToken != "" {
		expanded.AuthToken, err = expandString(ngrok.AuthToken, ctx)
		if err != nil {
			return expanded, fmt.Errorf("auth_token: %w", err)
		}
	}

	// Expand subdomain
	if ngrok.Subdomain != "" {
		expanded.Subdomain, err = expandString(ngrok.Subdomain, ctx)
		if err != nil {
			return expanded, fmt.Errorf("subdomain: %w", err)
		}
	}

	// Port is kept as-is - it's expanded at routing time via ExpandPortTemplate
	// This allows process references like ${processes.dashboard.env.PORT}
	expanded.Port = ngrok.Port

	return expanded, nil
}

// processRefRegex matches ${processes.<name>.env.<VAR>} or ${processes.<index>.env.<VAR>}
var processRefRegex = regexp.MustCompile(`\$\{processes\.([^.]+)\.env\.([^}]+)\}`)

// ExpandPortTemplate expands a port template against an expanded config.
// Supports:
//   - ${processes.<name>.env.<VAR>} - Reference a process by name
//   - ${processes.<index>.env.<VAR>} - Reference a process by index (0, 1, 2...)
//   - Regular templates like ${3000 + index}
//
// The cfg should already be expanded (templates resolved to actual values).
func ExpandPortTemplate(cfg *Config, portTemplate string, ctx TemplateContext) (int, error) {
	// First, try to match process references
	matches := processRefRegex.FindStringSubmatch(portTemplate)
	if len(matches) == 3 {
		procRef := matches[1] // Process name or index
		envVar := matches[2]  // Environment variable name

		// Find the process
		var proc *ProcessConfig
		if idx, err := strconv.Atoi(procRef); err == nil {
			// Referenced by index
			if idx >= 0 && idx < len(cfg.Processes) {
				proc = &cfg.Processes[idx]
			}
		} else {
			// Referenced by name
			for i := range cfg.Processes {
				if cfg.Processes[i].Name == procRef {
					proc = &cfg.Processes[i]
					break
				}
			}
		}

		if proc == nil {
			return 0, fmt.Errorf("process %q not found", procRef)
		}

		// Get the env var value
		value, exists := proc.Env[envVar]
		if !exists {
			return 0, fmt.Errorf("env var %q not found in process %q", envVar, procRef)
		}

		// Parse as int
		port, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("env var %q value %q is not a valid port number", envVar, value)
		}

		return port, nil
	}

	// Otherwise, try regular template expansion
	expanded, err := expandString(portTemplate, ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to expand port template: %w", err)
	}

	port, err := strconv.Atoi(expanded)
	if err != nil {
		return 0, fmt.Errorf("expanded port %q is not a valid number", expanded)
	}

	return port, nil
}

// expandString expands all ${...} patterns in a string.
func expandString(s string, ctx TemplateContext) (string, error) {
	var lastErr error

	result := templateVarRegex.ReplaceAllStringFunc(s, func(match string) string {
		// Extract the expression inside ${}
		expr := match[2 : len(match)-1]

		// Try to evaluate as expression
		value, err := evaluateExpression(expr, ctx)
		if err != nil {
			lastErr = err
			return match
		}

		return value
	})

	if lastErr != nil {
		return "", lastErr
	}

	return result, nil
}

// evaluateExpression evaluates an expression like "3000 + index" or just "name".
func evaluateExpression(expr string, ctx TemplateContext) (string, error) {
	expr = strings.TrimSpace(expr)

	// Check for simple variable references
	switch expr {
	case "index":
		return strconv.Itoa(ctx.Index), nil
	case "name":
		return ctx.Name, nil
	}

	// Try to evaluate as arithmetic expression
	result, err := evaluateArithmetic(expr, ctx)
	if err != nil {
		return "", err
	}

	return strconv.Itoa(result), nil
}

// evaluateArithmetic evaluates an arithmetic expression with variables.
// Supports: +, -, *, / and the variables "index" and "name" (name as 0 for arithmetic).
func evaluateArithmetic(expr string, ctx TemplateContext) (int, error) {
	// Tokenize the expression
	tokens, err := tokenize(expr)
	if err != nil {
		return 0, err
	}

	// Substitute variables
	for i, token := range tokens {
		if token.typ == tokenVar {
			switch token.value {
			case "index":
				tokens[i] = token
				tokens[i].typ = tokenNum
				tokens[i].numValue = ctx.Index
			default:
				return 0, fmt.Errorf("unknown variable: %s", token.value)
			}
		}
	}

	// Evaluate with proper precedence
	return evaluateTokens(tokens)
}

// Token types
type tokenType int

const (
	tokenNum tokenType = iota
	tokenVar
	tokenOp
	tokenLParen
	tokenRParen
)

type token struct {
	typ      tokenType
	value    string
	numValue int
}

// tokenize breaks an expression into tokens.
func tokenize(expr string) ([]token, error) {
	var tokens []token
	i := 0

	for i < len(expr) {
		ch := expr[i]

		// Skip whitespace
		if ch == ' ' || ch == '\t' {
			i++
			continue
		}

		// Number
		if ch >= '0' && ch <= '9' {
			start := i
			for i < len(expr) && expr[i] >= '0' && expr[i] <= '9' {
				i++
			}
			numStr := expr[start:i]
			num, _ := strconv.Atoi(numStr)
			tokens = append(tokens, token{typ: tokenNum, value: numStr, numValue: num})
			continue
		}

		// Variable (identifier)
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' {
			start := i
			for i < len(expr) && ((expr[i] >= 'a' && expr[i] <= 'z') || (expr[i] >= 'A' && expr[i] <= 'Z') || (expr[i] >= '0' && expr[i] <= '9') || expr[i] == '_') {
				i++
			}
			tokens = append(tokens, token{typ: tokenVar, value: expr[start:i]})
			continue
		}

		// Operators
		if ch == '+' || ch == '-' || ch == '*' || ch == '/' {
			tokens = append(tokens, token{typ: tokenOp, value: string(ch)})
			i++
			continue
		}

		// Parentheses
		if ch == '(' {
			tokens = append(tokens, token{typ: tokenLParen, value: "("})
			i++
			continue
		}
		if ch == ')' {
			tokens = append(tokens, token{typ: tokenRParen, value: ")"})
			i++
			continue
		}

		return nil, fmt.Errorf("unexpected character: %c", ch)
	}

	return tokens, nil
}

// evaluateTokens evaluates a list of tokens using a simple recursive descent parser.
func evaluateTokens(tokens []token) (int, error) {
	if len(tokens) == 0 {
		return 0, fmt.Errorf("empty expression")
	}

	pos := 0
	result, err := parseAddSub(tokens, &pos)
	if err != nil {
		return 0, err
	}

	if pos < len(tokens) {
		return 0, fmt.Errorf("unexpected token: %s", tokens[pos].value)
	}

	return result, nil
}

// parseAddSub handles + and - (lowest precedence)
func parseAddSub(tokens []token, pos *int) (int, error) {
	left, err := parseMulDiv(tokens, pos)
	if err != nil {
		return 0, err
	}

	for *pos < len(tokens) && tokens[*pos].typ == tokenOp && (tokens[*pos].value == "+" || tokens[*pos].value == "-") {
		op := tokens[*pos].value
		*pos++

		right, err := parseMulDiv(tokens, pos)
		if err != nil {
			return 0, err
		}

		if op == "+" {
			left = left + right
		} else {
			left = left - right
		}
	}

	return left, nil
}

// parseMulDiv handles * and / (higher precedence)
func parseMulDiv(tokens []token, pos *int) (int, error) {
	left, err := parsePrimary(tokens, pos)
	if err != nil {
		return 0, err
	}

	for *pos < len(tokens) && tokens[*pos].typ == tokenOp && (tokens[*pos].value == "*" || tokens[*pos].value == "/") {
		op := tokens[*pos].value
		*pos++

		right, err := parsePrimary(tokens, pos)
		if err != nil {
			return 0, err
		}

		if op == "*" {
			left = left * right
		} else {
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left = left / right
		}
	}

	return left, nil
}

// parsePrimary handles numbers, variables, and parenthesized expressions
func parsePrimary(tokens []token, pos *int) (int, error) {
	if *pos >= len(tokens) {
		return 0, fmt.Errorf("unexpected end of expression")
	}

	tok := tokens[*pos]

	switch tok.typ {
	case tokenNum:
		*pos++
		return tok.numValue, nil

	case tokenLParen:
		*pos++ // consume '('
		result, err := parseAddSub(tokens, pos)
		if err != nil {
			return 0, err
		}
		if *pos >= len(tokens) || tokens[*pos].typ != tokenRParen {
			return 0, fmt.Errorf("missing closing parenthesis")
		}
		*pos++ // consume ')'
		return result, nil

	default:
		return 0, fmt.Errorf("unexpected token: %s", tok.value)
	}
}
