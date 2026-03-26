package meshclaw

import (
	"fmt"
	"os"
	"path/filepath"
)

// BuiltinTemplates defines built-in assistant templates
var BuiltinTemplates = map[string]*Config{
	"assistant": {
		Name:        "assistant",
		Description: "General-purpose assistant with bash access",
		Model:       "claude-sonnet-4-20250514",
		Provider:    "anthropic",
		SystemPrompt: `You are a helpful assistant running on a server.
You can execute bash commands to help the user.
Always explain what you're doing before running commands.
Be concise and efficient.`,
		Tools: []string{"bash"},
	},

	"system-monitor": {
		Name:        "system-monitor",
		Description: "Server health monitor with alerts",
		Model:       "claude-sonnet-4-20250514",
		Provider:    "anthropic",
		SystemPrompt: `You are a system monitoring assistant.
Check system health (CPU, memory, disk, processes) and report issues.
Run diagnostic commands when asked.
Alert on critical conditions.`,
		Tools: []string{"bash"},
		Schedule: []ScheduleConfig{
			{Interval: "5m", Task: "Check system health: CPU, memory, disk usage"},
		},
	},

	"code-reviewer": {
		Name:        "code-reviewer",
		Description: "Git diff reviewer",
		Model:       "claude-sonnet-4-20250514",
		Provider:    "anthropic",
		SystemPrompt: `You are a code review assistant.
Review git diffs and provide constructive feedback.
Focus on: bugs, security issues, code style, performance.
Be specific and actionable.`,
		Tools: []string{"bash"},
	},

	"research": {
		Name:        "research",
		Description: "Web research and summarization",
		Model:       "claude-sonnet-4-20250514",
		Provider:    "anthropic",
		SystemPrompt: `You are a research assistant.
Search the web, gather information, and provide summaries.
Use curl to fetch web pages when needed.
Cite sources and organize findings clearly.`,
		Tools: []string{"bash"},
	},

	"devops": {
		Name:        "devops",
		Description: "DevOps automation assistant",
		Model:       "claude-sonnet-4-20250514",
		Provider:    "anthropic",
		SystemPrompt: `You are a DevOps automation assistant.
Help with deployments, infrastructure management, and automation.
You can run bash commands, manage services, and check logs.
Always confirm destructive operations before executing.`,
		Tools: []string{"bash"},
	},
}

// ListTemplates returns available templates
func ListTemplates() []string {
	names := make([]string, 0, len(BuiltinTemplates))
	for name := range BuiltinTemplates {
		names = append(names, name)
	}
	return names
}

// GetTemplate returns a template by name
func GetTemplate(name string) *Config {
	return BuiltinTemplates[name]
}

// InitFromTemplate creates a new worker from a template
func InitFromTemplate(templateName, workerName, outputDir string) (string, error) {
	tmpl := GetTemplate(templateName)
	if tmpl == nil {
		return "", fmt.Errorf("template not found: %s", templateName)
	}

	if workerName == "" {
		workerName = templateName
	}

	if outputDir == "" {
		outputDir = filepath.Join(WorkersDir(), workerName)
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", err
	}

	// Create config
	cfg := &Config{
		Name:         workerName,
		Description:  tmpl.Description,
		Model:        tmpl.Model,
		Provider:     tmpl.Provider,
		SystemPrompt: tmpl.SystemPrompt,
		Tools:        tmpl.Tools,
		Schedule:     tmpl.Schedule,
	}

	configPath := filepath.Join(outputDir, "config.json")
	if err := SaveConfig(cfg, configPath); err != nil {
		return "", err
	}

	return configPath, nil
}
