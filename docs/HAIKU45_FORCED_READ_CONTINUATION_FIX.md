# Claude Haiku 4.5 Forced-Read Continuation Fix

## Context

OmniCode agent sessions with `claude-haiku-4.5` could stop too early after an initial discovery tool call (for example `glob`).

Observed behavior:

- Step 0 executes a tool call (for example `glob`).
- Step 1 returns assistant text with no tool calls.
- Agent loop exits immediately with `agent: no tool calls, finishing`.

This prevented the intended one-time forced `read` follow-up turn from being recovered when the model skipped it.

## What Changed

Before:

- `Run` / `Stream` appended the assistant response, then immediately exited when no tool calls were returned.
- Recovery checks looked at message state where the most recent message was already assistant text, so the previous `tool_result` context was not recognized.

After:

- Added a continuation fallback in both `Run` and `Stream`:
  - If a forced single-read follow-up was expected but the model returned no tool calls, the runtime appends one continuation user prompt and proceeds to the next step instead of exiting immediately.
- Added `shouldRecoverFromMissedForcedRead(...)` helper that detects the forced-read condition even when the latest message is assistant text.
- Added regression tests for both `Run` and `Stream` paths.

## Why It Is Critical

This fixes a multi-step agent reliability issue for model/provider combinations that occasionally skip an expected tool call after receiving tool results.

Without this fallback, OmniCode can terminate exploration prematurely and produce lower-quality or under-grounded answers.

## Affected Files

- `internal/agent/agent.go`
- `internal/agent/agent_test.go`

## Validation

- `go test -count=1 ./internal/agent -run "TestRunInjectsContinuationWhenForcedReadFollowupIsMissed|TestStreamInjectsContinuationWhenForcedReadFollowupIsMissed|TestBuildRequestForcesSingleReadFollowupAfterInitialToolResult|TestShouldForceSingleReadFollowup"`

## Commit Range

Pending commit.
