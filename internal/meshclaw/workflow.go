package meshclaw

import (
	"fmt"
	"strings"
)

// WorkflowStep represents a single step in a workflow
type WorkflowStep struct {
	Agent    string            // agent name to run
	Args     []string          // additional arguments
	Input    string            // input from previous step (if any)
	UseInput bool              // whether to pass previous output as input
	Options  map[string]string // step options
}

// Workflow represents a multi-step workflow
type Workflow struct {
	Name  string
	Steps []WorkflowStep
}

// WorkflowResult contains the result of a workflow execution
type WorkflowResult struct {
	Success     bool
	StepResults []StepResult
	FinalOutput string
}

// StepResult contains the result of a single step
type StepResult struct {
	Step    int
	Agent   string
	Node    string
	Success bool
	Output  string
	Error   string
}

// ParseWorkflow parses a workflow string into steps
// Examples:
//   "news then summarize" → [news, summarize]
//   "system on g1 and notify" → [system --on=g1, notify]
func ParseWorkflow(input string) (*Workflow, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, fmt.Errorf("empty workflow")
	}

	// Split by "then", "and", "→", "|"
	separators := []string{" then ", " and then ", " -> ", " → ", " | ", " 그리고 ", " 다음 "}
	parts := []string{input}

	for _, sep := range separators {
		var newParts []string
		for _, p := range parts {
			split := strings.Split(p, sep)
			newParts = append(newParts, split...)
		}
		parts = newParts
	}

	if len(parts) < 2 {
		return nil, fmt.Errorf("workflow needs at least 2 steps (use 'then' or 'and')")
	}

	workflow := &Workflow{
		Name:  fmt.Sprintf("workflow-%d", len(parts)),
		Steps: make([]WorkflowStep, 0, len(parts)),
	}

	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		step := parseWorkflowStep(part)
		step.UseInput = i > 0 // All steps after first use previous output
		workflow.Steps = append(workflow.Steps, step)
	}

	if len(workflow.Steps) < 2 {
		return nil, fmt.Errorf("workflow needs at least 2 steps")
	}

	return workflow, nil
}

// parseWorkflowStep parses a single step like "news on g1"
func parseWorkflowStep(input string) WorkflowStep {
	words := strings.Fields(strings.ToLower(input))
	step := WorkflowStep{
		Args:    []string{},
		Options: make(map[string]string),
	}

	if len(words) == 0 {
		return step
	}

	// First word is agent name
	step.Agent = words[0]

	// Parse remaining words for options
	for i := 1; i < len(words); i++ {
		w := words[i]

		// --on=node or on node
		if w == "on" || w == "at" {
			if i+1 < len(words) {
				step.Options["on"] = words[i+1]
				step.Args = append(step.Args, "--on="+words[i+1])
				i++
			}
		} else if w == "--gpu" || w == "gpu" {
			step.Args = append(step.Args, "--gpu")
		} else if strings.HasPrefix(w, "--") {
			step.Args = append(step.Args, w)
		}
	}

	return step
}

// RunWorkflow executes a workflow and returns results
func RunWorkflow(workflow *Workflow, onStep func(step int, agent, status string)) (*WorkflowResult, error) {
	result := &WorkflowResult{
		Success:     true,
		StepResults: make([]StepResult, 0, len(workflow.Steps)),
	}

	var previousOutput string

	for i, step := range workflow.Steps {
		if onStep != nil {
			onStep(i+1, step.Agent, "starting")
		}

		stepResult := StepResult{
			Step:  i + 1,
			Agent: step.Agent,
		}

		// Build requirement from step options
		req := NodeRequirement{}
		if node, ok := step.Options["on"]; ok {
			req.Prefer = node
		}
		for _, arg := range step.Args {
			if arg == "--gpu" {
				req.GPU = true
			}
		}

		// Create agent input if using previous output
		agentInput := ""
		if step.UseInput && previousOutput != "" {
			agentInput = previousOutput
		}

		// Run the agent (for now, just run without input - will enhance later)
		_ = agentInput // TODO: pass input to agent

		runResult, err := RunAgentStream(step.Agent, req, func(node, line string) {
			if onStep != nil {
				onStep(i+1, step.Agent, line)
			}
		})

		if err != nil {
			stepResult.Success = false
			stepResult.Error = err.Error()
			result.StepResults = append(result.StepResults, stepResult)
			result.Success = false
			return result, fmt.Errorf("step %d (%s) failed: %w", i+1, step.Agent, err)
		}

		stepResult.Success = runResult.Success
		stepResult.Node = runResult.Node
		stepResult.Output = runResult.Output
		if !runResult.Success {
			stepResult.Error = runResult.Error
			result.Success = false
			result.StepResults = append(result.StepResults, stepResult)
			return result, fmt.Errorf("step %d (%s) failed: %s", i+1, step.Agent, runResult.Error)
		}

		previousOutput = runResult.Output
		result.StepResults = append(result.StepResults, stepResult)

		if onStep != nil {
			onStep(i+1, step.Agent, "completed")
		}
	}

	result.FinalOutput = previousOutput
	return result, nil
}

// BuiltinWorkflows contains predefined workflows
var BuiltinWorkflows = map[string]*Workflow{
	"morning": {
		Name: "morning",
		Steps: []WorkflowStep{
			{Agent: "news", UseInput: false},
			{Agent: "system", UseInput: false},
		},
	},
	"full-check": {
		Name: "full-check",
		Steps: []WorkflowStep{
			{Agent: "system", UseInput: false},
			{Agent: "hello", UseInput: false},
		},
	},
}

// GetWorkflow returns a builtin workflow by name
func GetWorkflow(name string) *Workflow {
	return BuiltinWorkflows[name]
}

// ListWorkflows returns available workflow names
func ListWorkflows() []string {
	names := make([]string, 0, len(BuiltinWorkflows))
	for name := range BuiltinWorkflows {
		names = append(names, name)
	}
	return names
}
