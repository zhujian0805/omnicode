package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
)

// send_message — send a message to another running agent or sub-process.

type sendMessageTool struct{}

func SendMessage() Tool { return &sendMessageTool{} }

func (t *sendMessageTool) Name() string { return "send_message" }
func (t *sendMessageTool) Description() string {
	return "Send a message to another agent, sub-agent, or named process and return its response. " +
		"Used for multi-agent coordination: orchestrate parallel agents, relay instructions, " +
		"or pass results between agents."
}
func (t *sendMessageTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"to":      map[string]any{"type": "string", "description": "Target agent or process name/ID."},
			"message": map[string]any{"type": "string", "description": "The message or instruction to send."},
		},
		"required": []string{"to", "message"},
	}
}
func (t *sendMessageTool) Execute(ctx context.Context, call Context, input json.RawMessage) Result {
	var p struct {
		To      string `json:"to"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(input, &p); err != nil {
		return Result{Output: "error: " + err.Error(), IsError: true}
	}
	if p.To == "" || p.Message == "" {
		return Result{Output: "error: to and message are required", IsError: true}
	}
	if call.SendMessageFn == nil {
		return Result{Output: "error: send_message not configured in this environment", IsError: true}
	}
	resp, err := call.SendMessageFn(ctx, p.To, p.Message)
	if err != nil {
		return Result{Output: "error: " + err.Error(), IsError: true}
	}
	return Result{
		Title:  fmt.Sprintf("Message sent to %s", p.To),
		Output: resp,
	}
}

// ─── agent_tool — spawn a named sub-agent to handle a sub-task ───────────────

type agentTool struct{}

func AgentTool() Tool { return &agentTool{} }

func (t *agentTool) Name() string { return "agent" }
func (t *agentTool) Description() string {
	return "Spawn a sub-agent to handle a complex, multi-step sub-task in isolation. " +
		"The sub-agent gets its own tool context, runs to completion, and returns its output. " +
		"Use this to parallelize work or delegate specialised tasks."
}
func (t *agentTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"description": map[string]any{"type": "string", "description": "Short description of the sub-agent's task (used as its name/label)."},
			"prompt":      map[string]any{"type": "string", "description": "The full instruction/task for the sub-agent."},
		},
		"required": []string{"prompt"},
	}
}
func (t *agentTool) Execute(ctx context.Context, call Context, input json.RawMessage) Result {
	var p struct {
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}
	if err := json.Unmarshal(input, &p); err != nil {
		return Result{Output: "error: " + err.Error(), IsError: true}
	}
	if p.Prompt == "" {
		return Result{Output: "error: prompt is required", IsError: true}
	}
	// Delegate through SendMessageFn if wired (orchestrator handles the routing).
	if call.SendMessageFn != nil {
		target := p.Description
		if target == "" {
			target = "sub-agent"
		}
		resp, err := call.SendMessageFn(ctx, target, p.Prompt)
		if err != nil {
			return Result{Output: "error: " + err.Error(), IsError: true}
		}
		return Result{Title: fmt.Sprintf("Sub-agent (%s) completed", target), Output: resp}
	}
	// Fallback: run as a background shell task if no messaging backend is configured.
	store := call.TaskStore
	if store == nil {
		return Result{
			Output:  "error: agent tool requires either a send_message backend or a task store",
			IsError: true,
		}
	}
	id := store.nextID()
	desc := p.Description
	if desc == "" {
		desc = fmt.Sprintf("sub-agent: %s", truncateTo(p.Prompt, 60))
	}
	run := &TaskRun{
		ID:          id,
		Description: desc,
		Status:      TaskRunPending,
		Output:      fmt.Sprintf("Sub-agent queued. Prompt: %s", p.Prompt),
	}
	store.add(run)
	return Result{
		Title:  fmt.Sprintf("Sub-agent queued: %s", id),
		Output: fmt.Sprintf("Sub-agent task %s created. Prompt:\n%s\n\nUse task_output %s to retrieve results.", id, p.Prompt, id),
	}
}

// ─── orchestrate_agents — leader-worker orchestration patterns ──────────────

type orchestrateAgentsTool struct{}

func OrchestrateAgents() Tool { return &orchestrateAgentsTool{} }

func (t *orchestrateAgentsTool) Name() string { return "orchestrate_agents" }
func (t *orchestrateAgentsTool) Description() string {
	return "Run multi-agent orchestration with built-in patterns: fan_out, pipeline, supervisor, generator_evaluator, or planner_generator_evaluator. " +
		"Each task is sent to a named worker via send_message, and results are aggregated."
}
func (t *orchestrateAgentsTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"enum":        []string{"fan_out", "pipeline", "supervisor", "generator_evaluator", "planner_generator_evaluator", "initializer_coder"},
				"description": "Orchestration pattern.",
			},
			"tasks": map[string]any{
				"type":        "array",
				"description": "List of worker tasks to execute.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"worker": map[string]any{"type": "string", "description": "Target worker name."},
						"prompt": map[string]any{"type": "string", "description": "Prompt sent to the worker."},
					},
					"required": []string{"worker", "prompt"},
				},
			},
			"supervisor_worker": map[string]any{"type": "string", "description": "Supervisor worker name (supervisor pattern only)."},
			"supervisor_prompt": map[string]any{"type": "string", "description": "Additional instructions for the supervisor synthesis step."},
			"max_rounds":        map[string]any{"type": "integer", "description": "Maximum refinement rounds for generator_evaluator (default 3, max 10)."},
			"acceptance_criteria": map[string]any{
				"type":        "string",
				"description": "Success criteria used by evaluator in generator_evaluator pattern.",
			},
			"max_replans": map[string]any{
				"type":        "integer",
				"description": "Maximum replan cycles for planner_generator_evaluator (default 2, max 5).",
			},
		},
		"required": []string{"pattern", "tasks"},
	}
}

func (t *orchestrateAgentsTool) Execute(ctx context.Context, call Context, input json.RawMessage) Result {
	if call.SendMessageFn == nil {
		return Result{Output: "error: send_message backend is not configured", IsError: true}
	}

	var p struct {
		Pattern string `json:"pattern"`
		Tasks   []struct {
			Worker string `json:"worker"`
			Prompt string `json:"prompt"`
		} `json:"tasks"`
		SupervisorWorker string `json:"supervisor_worker"`
		SupervisorPrompt string `json:"supervisor_prompt"`
		MaxRounds        int    `json:"max_rounds"`
		MaxReplans       int    `json:"max_replans"`
		Acceptance       string `json:"acceptance_criteria"`
	}
	if err := json.Unmarshal(input, &p); err != nil {
		return Result{Output: "error: " + err.Error(), IsError: true}
	}

	pattern := strings.TrimSpace(p.Pattern)
	if pattern == "" {
		return Result{Output: "error: pattern is required", IsError: true}
	}
	if len(p.Tasks) == 0 {
		return Result{Output: "error: tasks must contain at least one item", IsError: true}
	}
	for i := range p.Tasks {
		p.Tasks[i].Worker = strings.TrimSpace(p.Tasks[i].Worker)
		p.Tasks[i].Prompt = strings.TrimSpace(p.Tasks[i].Prompt)
		if p.Tasks[i].Worker == "" || p.Tasks[i].Prompt == "" {
			return Result{Output: fmt.Sprintf("error: tasks[%d] requires non-empty worker and prompt", i), IsError: true}
		}
	}

	log.Debug().
		Str("pattern", pattern).
		Int("tasks", len(p.Tasks)).
		Int("max_rounds", p.MaxRounds).
		Int("max_replans", p.MaxReplans).
		Msg("orchestrate_agents: starting pattern")

	switch pattern {
	case "fan_out":
		return runFanOut(ctx, call.SendMessageFn, p.Tasks)
	case "pipeline":
		return runPipeline(ctx, call.SendMessageFn, p.Tasks)
	case "supervisor":
		return runSupervisor(ctx, call.SendMessageFn, p.Tasks, p.SupervisorWorker, p.SupervisorPrompt)
	case "generator_evaluator":
		return runGeneratorEvaluator(ctx, call.SendMessageFn, p.Tasks, p.MaxRounds, p.Acceptance)
	case "planner_generator_evaluator":
		return runPlannerGeneratorEvaluator(ctx, call.SendMessageFn, p.Tasks, p.MaxRounds, p.MaxReplans, p.Acceptance)
	case "initializer_coder":
		return runInitializerCoder(ctx, call.SendMessageFn, p.Tasks)
	default:
		return Result{Output: "error: pattern must be one of fan_out, pipeline, supervisor, generator_evaluator, planner_generator_evaluator, initializer_coder", IsError: true}
	}
}

func runFanOut(
	ctx context.Context,
	sendMessageFn func(context.Context, string, string) (string, error),
	tasks []struct {
		Worker string `json:"worker"`
		Prompt string `json:"prompt"`
	},
) Result {
	type fanOutResult struct {
		idx    int
		worker string
		output string
		err    error
	}

	out := make([]fanOutResult, len(tasks))
	var wg sync.WaitGroup
	for i, task := range tasks {
		wg.Add(1)
		go func(idx int, worker, prompt string) {
			defer wg.Done()
			resp, err := sendMessageFn(ctx, worker, prompt)
			out[idx] = fanOutResult{idx: idx, worker: worker, output: resp, err: err}
		}(i, task.Worker, task.Prompt)
	}
	wg.Wait()

	var sb strings.Builder
	hasErr := false
	sb.WriteString("Pattern: fan_out\n")
	for i, r := range out {
		label := fmt.Sprintf("task %d (%s)", i+1, r.worker)
		if r.err != nil {
			hasErr = true
			fmt.Fprintf(&sb, "\n%s\nerror: %v\n", label, r.err)
			continue
		}
		fmt.Fprintf(&sb, "\n%s\n%s\n", label, strings.TrimSpace(r.output))
	}
	return Result{Title: "Fan-out orchestration", Output: strings.TrimSpace(sb.String()), IsError: hasErr}
}

func runPipeline(
	ctx context.Context,
	sendMessageFn func(context.Context, string, string) (string, error),
	tasks []struct {
		Worker string `json:"worker"`
		Prompt string `json:"prompt"`
	},
) Result {
	var sb strings.Builder
	var previous string
	sb.WriteString("Pattern: pipeline\n")
	for i, task := range tasks {
		if err := ctx.Err(); err != nil {
			return Result{Title: "Pipeline orchestration", Output: fmt.Sprintf("pipeline cancelled at stage %d: %v", i+1, err), IsError: true}
		}
		prompt := task.Prompt
		if strings.TrimSpace(previous) != "" {
			prompt += "\n\nPrevious stage output:\n" + previous
		}
		resp, err := sendMessageFn(ctx, task.Worker, prompt)
		if err != nil {
			return Result{
				Title:   "Pipeline orchestration",
				Output:  fmt.Sprintf("pipeline stopped at stage %d (%s): %v", i+1, task.Worker, err),
				IsError: true,
			}
		}
		previous = strings.TrimSpace(resp)
		fmt.Fprintf(&sb, "\nstage %d (%s)\n%s\n", i+1, task.Worker, previous)
	}
	return Result{Title: "Pipeline orchestration", Output: strings.TrimSpace(sb.String())}
}

func runSupervisor(
	ctx context.Context,
	sendMessageFn func(context.Context, string, string) (string, error),
	tasks []struct {
		Worker string `json:"worker"`
		Prompt string `json:"prompt"`
	},
	supervisorWorker,
	supervisorPrompt string,
) Result {
	fanOut := runFanOut(ctx, sendMessageFn, tasks)
	if fanOut.IsError {
		return Result{Title: "Supervisor orchestration", Output: "fan_out step failed:\n" + fanOut.Output, IsError: true}
	}

	supervisorWorker = strings.TrimSpace(supervisorWorker)
	if supervisorWorker == "" {
		supervisorWorker = "supervisor"
	}
	supervisorPrompt = strings.TrimSpace(supervisorPrompt)
	if supervisorPrompt == "" {
		supervisorPrompt = "Synthesize the worker outputs into one final answer with key decisions and next steps."
	}

	finalPrompt := supervisorPrompt + "\n\nWorker outputs:\n" + fanOut.Output
	summary, err := sendMessageFn(ctx, supervisorWorker, finalPrompt)
	if err != nil {
		return Result{Title: "Supervisor orchestration", Output: "supervisor step failed: " + err.Error(), IsError: true}
	}

	return Result{
		Title: "Supervisor orchestration",
		Output: strings.TrimSpace(
			"Pattern: supervisor\n\nWorker outputs\n" + fanOut.Output + "\n\nSupervisor summary\n" + strings.TrimSpace(summary),
		),
	}
}

func runGeneratorEvaluator(
	ctx context.Context,
	sendMessageFn func(context.Context, string, string) (string, error),
	tasks []struct {
		Worker string `json:"worker"`
		Prompt string `json:"prompt"`
	},
	maxRounds int,
	acceptanceCriteria string,
) Result {
	if len(tasks) < 2 {
		return Result{Output: "error: generator_evaluator requires at least two tasks: generator and evaluator", IsError: true}
	}

	generator := tasks[0]
	evaluator := tasks[1]
	if strings.TrimSpace(generator.Worker) == "" || strings.TrimSpace(evaluator.Worker) == "" {
		return Result{Output: "error: generator and evaluator workers are required", IsError: true}
	}

	if maxRounds <= 0 {
		maxRounds = 3
	}
	if maxRounds > 10 {
		maxRounds = 10
	}
	acceptanceCriteria = strings.TrimSpace(acceptanceCriteria)
	if acceptanceCriteria == "" {
		acceptanceCriteria = "Output is correct, complete, and production-ready."
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Pattern: generator_evaluator\nGenerator: %s\nEvaluator: %s\nMax rounds: %d\n", generator.Worker, evaluator.Worker, maxRounds)

	currentSpec := strings.TrimSpace(generator.Prompt)
	if currentSpec == "" {
		return Result{Output: "error: generator prompt cannot be empty", IsError: true}
	}

	bestCandidate := ""
	for round := 1; round <= maxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return Result{Title: "Generator-evaluator orchestration", Output: fmt.Sprintf("cancelled at round %d: %v", round, err), IsError: true}
		}
		log.Debug().
			Int("round", round).
			Int("max_rounds", maxRounds).
			Str("generator", generator.Worker).
			Str("evaluator", evaluator.Worker).
			Msg("orchestrate_agents: generator_evaluator round started")

		genPrompt := currentSpec
		if bestCandidate != "" {
			genPrompt += "\n\nPrevious draft:\n" + bestCandidate
		}
		candidate, err := sendMessageFn(ctx, generator.Worker, genPrompt)
		if err != nil {
			log.Debug().
				Int("round", round).
				Str("worker", generator.Worker).
				Err(err).
				Msg("orchestrate_agents: generator_evaluator generator failed")
			return Result{Title: "Generator-evaluator orchestration", Output: fmt.Sprintf("generator failed at round %d: %v", round, err), IsError: true}
		}
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			log.Debug().
				Int("round", round).
				Str("worker", generator.Worker).
				Msg("orchestrate_agents: generator_evaluator generator returned empty output")
			return Result{Title: "Generator-evaluator orchestration", Output: fmt.Sprintf("generator returned empty output at round %d", round), IsError: true}
		}
		log.Debug().
			Int("round", round).
			Int("candidate_chars", len(candidate)).
			Msg("orchestrate_agents: generator_evaluator candidate produced")

		evalPrompt := "Evaluate the candidate against the acceptance criteria. " +
			"Start your answer with either PASS or FAIL on the first token. " +
			"If FAIL, provide concise actionable fixes.\n\nAcceptance criteria:\n" + acceptanceCriteria +
			"\n\nCandidate:\n" + candidate
		assessment, err := sendMessageFn(ctx, evaluator.Worker, evalPrompt)
		if err != nil {
			log.Debug().
				Int("round", round).
				Str("worker", evaluator.Worker).
				Err(err).
				Msg("orchestrate_agents: generator_evaluator evaluator failed")
			return Result{Title: "Generator-evaluator orchestration", Output: fmt.Sprintf("evaluator failed at round %d: %v", round, err), IsError: true}
		}
		assessment = strings.TrimSpace(assessment)

		fmt.Fprintf(&sb, "\nRound %d candidate\n%s\n\nRound %d evaluation\n%s\n", round, candidate, round, assessment)
		bestCandidate = candidate

		normalizedAssessment := strings.ToUpper(strings.TrimSpace(assessment))
		if strings.HasPrefix(normalizedAssessment, "PASS") {
			log.Debug().
				Int("round", round).
				Msg("orchestrate_agents: generator_evaluator converged")
			fmt.Fprintf(&sb, "\nConverged: evaluator accepted output in round %d.\n", round)
			return Result{Title: "Generator-evaluator orchestration", Output: strings.TrimSpace(sb.String())}
		}
		log.Debug().
			Int("round", round).
			Int("assessment_chars", len(assessment)).
			Msg("orchestrate_agents: generator_evaluator feedback captured")

		currentSpec = generator.Prompt + "\n\nEvaluator feedback to address:\n" + assessment
	}

	log.Debug().
		Int("max_rounds", maxRounds).
		Msg("orchestrate_agents: generator_evaluator reached max rounds")
	fmt.Fprintf(&sb, "\nReached max rounds (%d) without PASS. Returning best candidate from final round.\n", maxRounds)
	return Result{Title: "Generator-evaluator orchestration", Output: strings.TrimSpace(sb.String())}
}

func runPlannerGeneratorEvaluator(
	ctx context.Context,
	sendMessageFn func(context.Context, string, string) (string, error),
	tasks []struct {
		Worker string `json:"worker"`
		Prompt string `json:"prompt"`
	},
	maxRounds int,
	maxReplans int,
	acceptanceCriteria string,
) Result {
	if len(tasks) < 3 {
		return Result{Output: "error: planner_generator_evaluator requires at least three tasks: planner, generator, and evaluator", IsError: true}
	}

	planner := tasks[0]
	generator := tasks[1]
	evaluator := tasks[2]
	if strings.TrimSpace(planner.Worker) == "" || strings.TrimSpace(generator.Worker) == "" || strings.TrimSpace(evaluator.Worker) == "" {
		return Result{Output: "error: planner, generator, and evaluator workers are required", IsError: true}
	}

	if maxRounds <= 0 {
		maxRounds = 3
	}
	if maxRounds > 10 {
		maxRounds = 10
	}
	if maxReplans <= 0 {
		maxReplans = 2
	}
	if maxReplans > 5 {
		maxReplans = 5
	}

	goal := strings.TrimSpace(generator.Prompt)
	if goal == "" {
		return Result{Output: "error: generator prompt cannot be empty", IsError: true}
	}

	acceptanceCriteria = strings.TrimSpace(acceptanceCriteria)
	if acceptanceCriteria == "" {
		acceptanceCriteria = "Output is correct, complete, and production-ready."
	}

	plannerInstructions := strings.TrimSpace(planner.Prompt)
	if plannerInstructions == "" {
		plannerInstructions = "Decompose the goal into concrete implementation steps and explicit success criteria checkpoints."
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Pattern: planner_generator_evaluator\nPlanner: %s\nGenerator: %s\nEvaluator: %s\nMax rounds: %d\nMax replans: %d\n", planner.Worker, generator.Worker, evaluator.Worker, maxRounds, maxReplans)

	lastFeedback := ""
	bestCandidate := ""
	for replan := 1; replan <= maxReplans+1; replan++ {
		if err := ctx.Err(); err != nil {
			return Result{Title: "Planner-generator-evaluator orchestration", Output: fmt.Sprintf("cancelled at cycle %d: %v", replan, err), IsError: true}
		}
		log.Debug().
			Int("replan_cycle", replan).
			Int("max_replan_cycles", maxReplans+1).
			Str("planner", planner.Worker).
			Str("generator", generator.Worker).
			Str("evaluator", evaluator.Worker).
			Msg("orchestrate_agents: planner_generator_evaluator cycle started")

		planPrompt := plannerInstructions + "\n\nGoal:\n" + goal + "\n\nAcceptance criteria:\n" + acceptanceCriteria
		if strings.TrimSpace(lastFeedback) != "" {
			planPrompt += "\n\nPrior evaluator failures to address:\n" + lastFeedback
		}

		plan, err := sendMessageFn(ctx, planner.Worker, planPrompt)
		if err != nil {
			log.Debug().
				Int("replan_cycle", replan).
				Str("worker", planner.Worker).
				Err(err).
				Msg("orchestrate_agents: planner failed")
			return Result{Title: "Planner-generator-evaluator orchestration", Output: fmt.Sprintf("planner failed at cycle %d: %v", replan, err), IsError: true}
		}
		plan = strings.TrimSpace(plan)
		if plan == "" {
			log.Debug().
				Int("replan_cycle", replan).
				Str("worker", planner.Worker).
				Msg("orchestrate_agents: planner returned empty plan")
			return Result{Title: "Planner-generator-evaluator orchestration", Output: fmt.Sprintf("planner returned empty plan at cycle %d", replan), IsError: true}
		}
		log.Debug().
			Int("replan_cycle", replan).
			Int("plan_chars", len(plan)).
			Msg("orchestrate_agents: plan generated")

		fmt.Fprintf(&sb, "\nReplan cycle %d plan\n%s\n", replan, plan)

		currentSpec := goal + "\n\nExecution plan:\n" + plan
		for round := 1; round <= maxRounds; round++ {
			if err := ctx.Err(); err != nil {
				return Result{Title: "Planner-generator-evaluator orchestration", Output: fmt.Sprintf("cancelled at cycle %d round %d: %v", replan, round, err), IsError: true}
			}
			log.Debug().
				Int("replan_cycle", replan).
				Int("round", round).
				Int("max_rounds", maxRounds).
				Msg("orchestrate_agents: planner_generator_evaluator round started")

			genPrompt := currentSpec
			if bestCandidate != "" {
				genPrompt += "\n\nPrevious draft:\n" + bestCandidate
			}
			candidate, err := sendMessageFn(ctx, generator.Worker, genPrompt)
			if err != nil {
				log.Debug().
					Int("replan_cycle", replan).
					Int("round", round).
					Str("worker", generator.Worker).
					Err(err).
					Msg("orchestrate_agents: planner_generator_evaluator generator failed")
				return Result{Title: "Planner-generator-evaluator orchestration", Output: fmt.Sprintf("generator failed at cycle %d round %d: %v", replan, round, err), IsError: true}
			}
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				log.Debug().
					Int("replan_cycle", replan).
					Int("round", round).
					Str("worker", generator.Worker).
					Msg("orchestrate_agents: planner_generator_evaluator generator returned empty output")
				return Result{Title: "Planner-generator-evaluator orchestration", Output: fmt.Sprintf("generator returned empty output at cycle %d round %d", replan, round), IsError: true}
			}
			log.Debug().
				Int("replan_cycle", replan).
				Int("round", round).
				Int("candidate_chars", len(candidate)).
				Msg("orchestrate_agents: planner_generator_evaluator candidate produced")

			evalPrompt := "Evaluate the candidate against the acceptance criteria. " +
				"Start your answer with either PASS or FAIL on the first token. " +
				"If FAIL, provide concise actionable fixes tied to the criteria.\n\nAcceptance criteria:\n" + acceptanceCriteria +
				"\n\nCandidate:\n" + candidate
			assessment, err := sendMessageFn(ctx, evaluator.Worker, evalPrompt)
			if err != nil {
				log.Debug().
					Int("replan_cycle", replan).
					Int("round", round).
					Str("worker", evaluator.Worker).
					Err(err).
					Msg("orchestrate_agents: planner_generator_evaluator evaluator failed")
				return Result{Title: "Planner-generator-evaluator orchestration", Output: fmt.Sprintf("evaluator failed at cycle %d round %d: %v", replan, round, err), IsError: true}
			}
			assessment = strings.TrimSpace(assessment)

			fmt.Fprintf(&sb, "\nCycle %d round %d candidate\n%s\n\nCycle %d round %d evaluation\n%s\n", replan, round, candidate, replan, round, assessment)
			bestCandidate = candidate

			normalizedAssessment := strings.ToUpper(strings.TrimSpace(assessment))
			if strings.HasPrefix(normalizedAssessment, "PASS") {
				log.Debug().
					Int("replan_cycle", replan).
					Int("round", round).
					Msg("orchestrate_agents: planner_generator_evaluator converged")
				fmt.Fprintf(&sb, "\nConverged: evaluator accepted output in cycle %d round %d.\n", replan, round)
				return Result{Title: "Planner-generator-evaluator orchestration", Output: strings.TrimSpace(sb.String())}
			}
			log.Debug().
				Int("replan_cycle", replan).
				Int("round", round).
				Int("assessment_chars", len(assessment)).
				Msg("orchestrate_agents: planner_generator_evaluator feedback captured")

			lastFeedback = assessment
			currentSpec = goal + "\n\nExecution plan:\n" + plan + "\n\nEvaluator feedback to address:\n" + assessment
		}
	}

	log.Debug().
		Int("max_replans", maxReplans).
		Msg("orchestrate_agents: planner_generator_evaluator reached max replans")
	fmt.Fprintf(&sb, "\nReached max replans (%d) without PASS. Returning best candidate from final cycle.\n", maxReplans)
	return Result{Title: "Planner-generator-evaluator orchestration", Output: strings.TrimSpace(sb.String())}
}

func runInitializerCoder(
	ctx context.Context,
	sendMessageFn func(context.Context, string, string) (string, error),
	tasks []struct {
		Worker string `json:"worker"`
		Prompt string `json:"prompt"`
	},
) Result {
	if len(tasks) < 2 {
		return Result{Output: "error: initializer_coder requires at least two tasks: initializer and one or more coders", IsError: true}
	}

	initializer := tasks[0]
	coders := tasks[1:]

	var sb strings.Builder
	sb.WriteString("Pattern: initializer_coder\n")

	if err := ctx.Err(); err != nil {
		return Result{Title: "Initializer-coder orchestration", Output: "cancelled before start: " + err.Error(), IsError: true}
	}

	log.Debug().
		Str("initializer", initializer.Worker).
		Int("coders", len(coders)).
		Msg("orchestrate_agents: initializer_coder starting")

	plan, err := sendMessageFn(ctx, initializer.Worker, initializer.Prompt)
	if err != nil {
		return Result{Title: "Initializer-coder orchestration", Output: fmt.Sprintf("initializer (%s) failed: %v", initializer.Worker, err), IsError: true}
	}
	plan = strings.TrimSpace(plan)
	if plan == "" {
		return Result{Title: "Initializer-coder orchestration", Output: "initializer returned empty plan", IsError: true}
	}

	log.Debug().
		Int("plan_chars", len(plan)).
		Msg("orchestrate_agents: initializer_coder plan produced")

	fmt.Fprintf(&sb, "\nInitializer (%s)\n%s\n", initializer.Worker, plan)

	var progress strings.Builder
	hasErr := false
	for i, coder := range coders {
		if err := ctx.Err(); err != nil {
			return Result{Title: "Initializer-coder orchestration", Output: sb.String() + "\ncancelled at coder " + fmt.Sprint(i+1) + ": " + err.Error(), IsError: true}
		}

		log.Debug().
			Int("coder", i+1).
			Str("worker", coder.Worker).
			Msg("orchestrate_agents: initializer_coder running coder")

		coderPrompt := coder.Prompt + "\n\nInitializer plan:\n" + plan
		if progress.Len() > 0 {
			coderPrompt += "\n\nCompleted work so far:\n" + progress.String()
		}

		resp, err := sendMessageFn(ctx, coder.Worker, coderPrompt)
		if err != nil {
			hasErr = true
			fmt.Fprintf(&sb, "\nCoder %d (%s)\nerror: %v\n", i+1, coder.Worker, err)
			log.Debug().
				Int("coder", i+1).
				Str("worker", coder.Worker).
				Err(err).
				Msg("orchestrate_agents: initializer_coder coder failed")
			continue
		}
		resp = strings.TrimSpace(resp)
		fmt.Fprintf(&sb, "\nCoder %d (%s)\n%s\n", i+1, coder.Worker, resp)
		fmt.Fprintf(&progress, "- %s: %s\n", coder.Worker, truncateTo(resp, 200))

		log.Debug().
			Int("coder", i+1).
			Str("worker", coder.Worker).
			Int("resp_chars", len(resp)).
			Msg("orchestrate_agents: initializer_coder coder completed")
	}

	return Result{Title: "Initializer-coder orchestration", Output: strings.TrimSpace(sb.String()), IsError: hasErr}
}
