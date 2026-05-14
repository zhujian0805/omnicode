package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestOrchestrateAgentsFanOut(t *testing.T) {
	tool := OrchestrateAgents()
	var mu sync.Mutex
	var calls []string
	ctx := Context{
		SendMessageFn: func(_ context.Context, to, message string) (string, error) {
			mu.Lock()
			calls = append(calls, to+"::"+message)
			mu.Unlock()
			return fmt.Sprintf("%s done", to), nil
		},
	}

	input, _ := json.Marshal(map[string]any{
		"pattern": "fan_out",
		"tasks": []map[string]any{
			{"worker": "research", "prompt": "collect facts"},
			{"worker": "coder", "prompt": "draft patch"},
		},
	})
	res := tool.Execute(context.Background(), ctx, input)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	mu.Lock()
	callCount := len(calls)
	mu.Unlock()
	if callCount != 2 {
		t.Fatalf("expected 2 calls, got %d", callCount)
	}
	if !strings.Contains(res.Output, "Pattern: fan_out") {
		t.Fatalf("missing fan_out header: %q", res.Output)
	}
}

func TestOrchestrateAgentsPipelineCarriesPreviousOutput(t *testing.T) {
	tool := OrchestrateAgents()
	received := make([]string, 0, 2)
	ctx := Context{
		SendMessageFn: func(_ context.Context, to, message string) (string, error) {
			received = append(received, to+"::"+message)
			if to == "stage1" {
				return "stage1 result", nil
			}
			return "stage2 result", nil
		},
	}

	input, _ := json.Marshal(map[string]any{
		"pattern": "pipeline",
		"tasks": []map[string]any{
			{"worker": "stage1", "prompt": "analyze"},
			{"worker": "stage2", "prompt": "implement"},
		},
	})
	res := tool.Execute(context.Background(), ctx, input)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if len(received) != 2 {
		t.Fatalf("expected 2 pipeline calls, got %d", len(received))
	}
	if !strings.Contains(received[1], "Previous stage output:\nstage1 result") {
		t.Fatalf("second stage did not receive prior output: %q", received[1])
	}
}

func TestOrchestrateAgentsSupervisorAddsSynthesisStep(t *testing.T) {
	tool := OrchestrateAgents()
	var supervisorInput string
	ctx := Context{
		SendMessageFn: func(_ context.Context, to, message string) (string, error) {
			if to == "supervisor" {
				supervisorInput = message
				return "final synthesis", nil
			}
			return to + " output", nil
		},
	}

	input, _ := json.Marshal(map[string]any{
		"pattern": "supervisor",
		"tasks": []map[string]any{
			{"worker": "research", "prompt": "collect facts"},
			{"worker": "coder", "prompt": "write code"},
		},
		"supervisor_worker": "supervisor",
		"supervisor_prompt": "merge worker results",
	})
	res := tool.Execute(context.Background(), ctx, input)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(supervisorInput, "merge worker results") {
		t.Fatalf("supervisor prompt missing custom instruction: %q", supervisorInput)
	}
	if !strings.Contains(supervisorInput, "research output") || !strings.Contains(supervisorInput, "coder output") {
		t.Fatalf("supervisor input missing worker outputs: %q", supervisorInput)
	}
	if !strings.Contains(res.Output, "final synthesis") {
		t.Fatalf("final output missing supervisor summary: %q", res.Output)
	}
}

func TestOrchestrateAgentsGeneratorEvaluatorConverges(t *testing.T) {
	tool := OrchestrateAgents()
	genCalls := 0
	evalCalls := 0
	ctx := Context{
		SendMessageFn: func(_ context.Context, to, message string) (string, error) {
			switch to {
			case "generator":
				genCalls++
				if genCalls == 1 {
					return "draft v1", nil
				}
				return "draft v2", nil
			case "evaluator":
				evalCalls++
				if evalCalls == 1 {
					return "FAIL: missing edge-case handling", nil
				}
				return "PASS: meets acceptance criteria", nil
			default:
				return "", fmt.Errorf("unexpected worker %s", to)
			}
		},
	}

	input, _ := json.Marshal(map[string]any{
		"pattern": "generator_evaluator",
		"tasks": []map[string]any{
			{"worker": "generator", "prompt": "produce implementation"},
			{"worker": "evaluator", "prompt": "evaluate result"},
		},
		"max_rounds":          3,
		"acceptance_criteria": "Code compiles and handles edge cases",
	})
	res := tool.Execute(context.Background(), ctx, input)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if genCalls != 2 || evalCalls != 2 {
		t.Fatalf("expected 2 gen and 2 eval calls, got gen=%d eval=%d", genCalls, evalCalls)
	}
	if !strings.Contains(res.Output, "Converged") {
		t.Fatalf("expected convergence marker, got: %q", res.Output)
	}
}

func TestOrchestrateAgentsGeneratorEvaluatorMaxRounds(t *testing.T) {
	tool := OrchestrateAgents()
	evalCalls := 0
	ctx := Context{
		SendMessageFn: func(_ context.Context, to, message string) (string, error) {
			if to == "generator" {
				return "candidate", nil
			}
			evalCalls++
			return "FAIL: needs more work", nil
		},
	}

	input, _ := json.Marshal(map[string]any{
		"pattern": "generator_evaluator",
		"tasks": []map[string]any{
			{"worker": "generator", "prompt": "produce implementation"},
			{"worker": "evaluator", "prompt": "evaluate result"},
		},
		"max_rounds": 2,
	})
	res := tool.Execute(context.Background(), ctx, input)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if evalCalls != 2 {
		t.Fatalf("expected 2 evaluator calls, got %d", evalCalls)
	}
	if !strings.Contains(res.Output, "Reached max rounds (2)") {
		t.Fatalf("expected max-round summary, got: %q", res.Output)
	}
}

func TestOrchestrateAgentsPlannerGeneratorEvaluatorConverges(t *testing.T) {
	tool := OrchestrateAgents()
	plannerCalls := 0
	genCalls := 0
	evalCalls := 0
	ctx := Context{
		SendMessageFn: func(_ context.Context, to, message string) (string, error) {
			switch to {
			case "planner":
				plannerCalls++
				if plannerCalls == 1 {
					return "Plan v1: implement baseline path and add tests.", nil
				}
				return "Plan v2: keep baseline, add edge-case handling and validation.", nil
			case "generator":
				genCalls++
				if genCalls == 1 {
					return "candidate v1", nil
				}
				return "candidate v2", nil
			case "evaluator":
				evalCalls++
				if evalCalls == 1 {
					return "FAIL: missing edge-case handling", nil
				}
				return "PASS: satisfies all criteria", nil
			default:
				return "", fmt.Errorf("unexpected worker %s", to)
			}
		},
	}

	input, _ := json.Marshal(map[string]any{
		"pattern": "planner_generator_evaluator",
		"tasks": []map[string]any{
			{"worker": "planner", "prompt": "decompose and sequence"},
			{"worker": "generator", "prompt": "implement feature"},
			{"worker": "evaluator", "prompt": "evaluate result"},
		},
		"max_rounds":          2,
		"max_replans":         2,
		"acceptance_criteria": "Code compiles and handles edge cases",
	})
	res := tool.Execute(context.Background(), ctx, input)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if plannerCalls != 1 {
		t.Fatalf("expected planner to run once before convergence, got %d", plannerCalls)
	}
	if genCalls != 2 || evalCalls != 2 {
		t.Fatalf("expected 2 generator and 2 evaluator calls, got gen=%d eval=%d", genCalls, evalCalls)
	}
	if !strings.Contains(res.Output, "Pattern: planner_generator_evaluator") {
		t.Fatalf("missing pattern header: %q", res.Output)
	}
	if !strings.Contains(res.Output, "Converged") {
		t.Fatalf("expected convergence marker, got: %q", res.Output)
	}
}

func TestOrchestrateAgentsPlannerGeneratorEvaluatorRespectsReplanCap(t *testing.T) {
	tool := OrchestrateAgents()
	plannerCalls := 0
	evalCalls := 0
	ctx := Context{
		SendMessageFn: func(_ context.Context, to, message string) (string, error) {
			switch to {
			case "planner":
				plannerCalls++
				return fmt.Sprintf("Plan cycle %d", plannerCalls), nil
			case "generator":
				return "candidate", nil
			case "evaluator":
				evalCalls++
				return "FAIL: still not acceptable", nil
			default:
				return "", fmt.Errorf("unexpected worker %s", to)
			}
		},
	}

	input, _ := json.Marshal(map[string]any{
		"pattern": "planner_generator_evaluator",
		"tasks": []map[string]any{
			{"worker": "planner", "prompt": "plan"},
			{"worker": "generator", "prompt": "implement"},
			{"worker": "evaluator", "prompt": "evaluate"},
		},
		"max_rounds":  1,
		"max_replans": 2,
	})
	res := tool.Execute(context.Background(), ctx, input)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if plannerCalls != 3 {
		t.Fatalf("expected planner to run max_replans+1 times (3), got %d", plannerCalls)
	}
	if evalCalls != 3 {
		t.Fatalf("expected evaluator to run 3 times with max_rounds=1 and 3 cycles, got %d", evalCalls)
	}
	if !strings.Contains(res.Output, "Reached max replans (2)") {
		t.Fatalf("expected max-replans summary, got: %q", res.Output)
	}
}

func TestOrchestrateAgentsInitializerCoderBasic(t *testing.T) {
	tool := OrchestrateAgents()
	var initCalls, coderCalls int
	ctx := Context{
		SendMessageFn: func(_ context.Context, to, message string) (string, error) {
			switch to {
			case "initializer":
				initCalls++
				return "Plan: 1. build auth 2. build chat", nil
			case "coder-auth":
				coderCalls++
				if !strings.Contains(message, "Plan: 1. build auth") {
					return "", fmt.Errorf("coder-auth missing initializer plan")
				}
				return "auth implemented", nil
			case "coder-chat":
				coderCalls++
				if !strings.Contains(message, "Plan: 1. build auth") {
					return "", fmt.Errorf("coder-chat missing initializer plan")
				}
				if !strings.Contains(message, "Completed work so far") {
					return "", fmt.Errorf("coder-chat missing progress from previous coder")
				}
				return "chat implemented", nil
			default:
				return "", fmt.Errorf("unexpected worker %s", to)
			}
		},
	}

	input, _ := json.Marshal(map[string]any{
		"pattern": "initializer_coder",
		"tasks": []map[string]any{
			{"worker": "initializer", "prompt": "decompose project"},
			{"worker": "coder-auth", "prompt": "implement auth"},
			{"worker": "coder-chat", "prompt": "implement chat"},
		},
	})
	res := tool.Execute(context.Background(), ctx, input)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if initCalls != 1 {
		t.Fatalf("expected initializer to run exactly once, got %d", initCalls)
	}
	if coderCalls != 2 {
		t.Fatalf("expected 2 coder calls, got %d", coderCalls)
	}
	if !strings.Contains(res.Output, "Pattern: initializer_coder") {
		t.Fatalf("missing pattern header: %q", res.Output)
	}
	if !strings.Contains(res.Output, "auth implemented") || !strings.Contains(res.Output, "chat implemented") {
		t.Fatalf("missing coder outputs: %q", res.Output)
	}
}

func TestOrchestrateAgentsInitializerCoderContinuesOnError(t *testing.T) {
	tool := OrchestrateAgents()
	ctx := Context{
		SendMessageFn: func(_ context.Context, to, message string) (string, error) {
			switch to {
			case "init":
				return "feature list: auth, chat, settings", nil
			case "coder-fail":
				return "", fmt.Errorf("simulated failure")
			case "coder-ok":
				return "feature done", nil
			default:
				return "", fmt.Errorf("unexpected worker %s", to)
			}
		},
	}

	input, _ := json.Marshal(map[string]any{
		"pattern": "initializer_coder",
		"tasks": []map[string]any{
			{"worker": "init", "prompt": "plan"},
			{"worker": "coder-fail", "prompt": "implement auth"},
			{"worker": "coder-ok", "prompt": "implement chat"},
		},
	})
	res := tool.Execute(context.Background(), ctx, input)
	if !res.IsError {
		t.Fatalf("expected error flag due to failed coder, got success")
	}
	if !strings.Contains(res.Output, "error: simulated failure") {
		t.Fatalf("expected failure message in output: %q", res.Output)
	}
	if !strings.Contains(res.Output, "feature done") {
		t.Fatalf("expected second coder to still run after first failure: %q", res.Output)
	}
}

func TestOrchestrateAgentsInitializerCoderMinTasks(t *testing.T) {
	tool := OrchestrateAgents()
	ctx := Context{
		SendMessageFn: func(_ context.Context, _, _ string) (string, error) {
			return "ok", nil
		},
	}

	input, _ := json.Marshal(map[string]any{
		"pattern": "initializer_coder",
		"tasks": []map[string]any{
			{"worker": "init", "prompt": "plan"},
		},
	})
	res := tool.Execute(context.Background(), ctx, input)
	if !res.IsError {
		t.Fatalf("expected error for single task, got success")
	}
	if !strings.Contains(res.Output, "at least two tasks") {
		t.Fatalf("expected min-tasks error: %q", res.Output)
	}
}
