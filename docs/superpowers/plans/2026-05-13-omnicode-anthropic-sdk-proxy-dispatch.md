# OmniCode Anthropic SDK Proxy Dispatch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make OmniCode agent mode send Anthropic-shaped `/v1/messages` requests to the OmniLLM proxy through the official Anthropic Go SDK transport.

**Architecture:** Keep the OmniLLM proxy and provider routing untouched. Change only the OmniCode agent dispatch selection so Anthropic-shaped agent turns use `AnthropicSDKDispatch(apiKey, proxyBaseURL)` instead of manually marshaling JSON and calling the CLI client `Post` method. Preserve the existing OpenAI-shape dispatch path.

**Tech Stack:** Go, official `github.com/anthropics/anthropic-sdk-go`, OmniCode `internal/agent` runtime, existing Go unit tests.

---

## File Structure

- Modify `internal/agent/session_runner.go`
  - Add a proxy base URL helper for Anthropic SDK dispatch.
  - Switch default Anthropic-shape dispatch to `AnthropicSDKDispatch`.
  - Leave OpenAI-shape dispatch untouched.
- Modify `internal/agent/agent_test.go`
  - Replace the old `TestSelectDispatchAlwaysUsesMessagesProxy` assertion with an HTTP-backed test proving `selectDispatch` uses Anthropic SDK transport against the proxy base URL.
  - Add a test proving OpenAI shape still uses `/v1` OpenAI SDK base URL behavior.
- Do not modify proxy/server/provider code.

---

### Task 1: Add failing test for Anthropic SDK proxy transport

**Files:**
- Modify: `internal/agent/agent_test.go`

- [ ] **Step 1: Replace `TestSelectDispatchAlwaysUsesMessagesProxy` with an SDK-transport test**

Replace the existing test at `internal/agent/agent_test.go:452` with this test code:

```go
func TestSelectDispatchUsesAnthropicSDKAgainstProxyBaseURL(t *testing.T) {
	var capturedPath string
	var capturedAuth string
	var capturedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_1",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-opus-4-5",
			"content":     []map[string]any{{"type": "text", "text": "ok"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 3, "output_tokens": 1},
		})
	}))
	defer server.Close()

	client := &sdkProxyTestClient{baseURL: server.URL, apiKey: "proxy-key"}
	dispatch := selectDispatch(client, "claude-opus-4-5", "google-adk", DefaultAPIShape)
	respCh, err := dispatch(context.Background(), testMessagesRequest("", testUserMessage("hi")))
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	for range respCh {
	}

	if capturedPath != "/v1/messages" {
		t.Fatalf("expected SDK to post to /v1/messages, got %q", capturedPath)
	}
	if capturedAuth != "Bearer proxy-key" {
		t.Fatalf("Authorization = %q, want Bearer proxy-key", capturedAuth)
	}
	if capturedBody["model"] != "claude-opus-4-5" {
		t.Fatalf("request model = %#v", capturedBody["model"])
	}
}

type sdkProxyTestClient struct {
	baseURL string
	apiKey  string
}

func (s *sdkProxyTestClient) GetBaseURL() string { return s.baseURL }
func (s *sdkProxyTestClient) GetAPIKey() string  { return s.apiKey }

func (s *sdkProxyTestClient) Post(path string, body any) ([]byte, error) {
	return nil, fmt.Errorf("unexpected Client.Post call to %s with %#v", path, body)
}

func (s *sdkProxyTestClient) PostStream(path string, body any) (*http.Response, error) {
	return nil, fmt.Errorf("unexpected Client.PostStream call to %s with %#v", path, body)
}
```

- [ ] **Step 2: Run the targeted test and verify it fails**

Run:

```powershell
go test ./internal/agent -run TestSelectDispatchUsesAnthropicSDKAgainstProxyBaseURL -count=1
```

Expected: FAIL because `selectDispatch` still calls `NewDispatch`, which calls `Client.Post` and triggers `unexpected Client.Post call`.

---

### Task 2: Implement Anthropic SDK dispatch selection

**Files:**
- Modify: `internal/agent/session_runner.go`

- [ ] **Step 1: Add the Anthropic proxy base URL helper**

In `internal/agent/session_runner.go`, add this helper near `omniLLMOpenAIBaseURL`:

```go
func omniLLMAnthropicBaseURL(c Client) string {
	config, ok := c.(OmniLLMClientConfig)
	if !ok {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(config.GetBaseURL()), "/")
}
```

- [ ] **Step 2: Switch the default dispatch branch to `AnthropicSDKDispatch`**

Change `selectDispatch` from:

```go
func selectDispatch(c Client, model, _, apiShape string) DispatchFn {
	var base DispatchFn
	switch normalizeAPIShape(apiShape) {
	case "openai":
		base = OpenAISDKDispatch(omniLLMAPIKey(c), omniLLMOpenAIBaseURL(c), model)
	default:
		base = NewDispatch(c, model, DefaultAPIShape)
	}

	// Add transient retry behavior for interactive agent turns.
	return retryDispatch(base, 3, 500*time.Millisecond, 8*time.Second)
}
```

to:

```go
func selectDispatch(c Client, model, _, apiShape string) DispatchFn {
	var base DispatchFn
	switch normalizeAPIShape(apiShape) {
	case "openai":
		base = OpenAISDKDispatch(omniLLMAPIKey(c), omniLLMOpenAIBaseURL(c), model)
	default:
		base = AnthropicSDKDispatch(omniLLMAPIKey(c), omniLLMAnthropicBaseURL(c))
	}

	// Add transient retry behavior for interactive agent turns.
	return retryDispatch(base, 3, 500*time.Millisecond, 8*time.Second)
}
```

- [ ] **Step 3: Run the new targeted test**

Run:

```powershell
go test ./internal/agent -run TestSelectDispatchUsesAnthropicSDKAgainstProxyBaseURL -count=1
```

Expected: PASS.

---

### Task 3: Preserve OpenAI-shape dispatch behavior

**Files:**
- Modify: `internal/agent/agent_test.go`

- [ ] **Step 1: Add a regression test for OpenAI shape**

Add this test near the Anthropic dispatch selection test:

```go
func TestSelectDispatchKeepsOpenAIShapeOnOpenAIBaseURL(t *testing.T) {
	var capturedPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl_1",
			"choices": []map[string]any{{
				"index":         0,
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": "ok"},
			}},
			"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 1},
		})
	}))
	defer server.Close()

	client := &sdkProxyTestClient{baseURL: server.URL, apiKey: "proxy-key"}
	dispatch := selectDispatch(client, "gpt-5.4", "google-adk", "openai")
	respCh, err := dispatch(context.Background(), testMessagesRequest("", testUserMessage("hi")))
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	for range respCh {
	}

	if capturedPath != "/v1/chat/completions" {
		t.Fatalf("expected OpenAI dispatch to post to /v1/chat/completions, got %q", capturedPath)
	}
}
```

- [ ] **Step 2: Run the OpenAI-shape regression test**

Run:

```powershell
go test ./internal/agent -run TestSelectDispatchKeepsOpenAIShapeOnOpenAIBaseURL -count=1
```

Expected: PASS.

---

### Task 4: Run targeted package tests

**Files:**
- Test only: `internal/agent/...`

- [ ] **Step 1: Run the dispatch-related tests**

Run:

```powershell
go test ./internal/agent -run "TestSelectDispatch|TestAnthropicSDKDispatch|TestAnthropicParams" -count=1
```

Expected: PASS.

- [ ] **Step 2: Run the changed package tests**

Run:

```powershell
go test ./internal/agent/... -count=1
```

Expected: PASS.

- [ ] **Step 3: Run gofmt on changed Go files**

Run:

```powershell
gofmt -w internal/agent/session_runner.go internal/agent/agent_test.go
```

Expected: command exits successfully with no output.

- [ ] **Step 4: Re-run changed package tests after gofmt**

Run:

```powershell
go test ./internal/agent/... -count=1
```

Expected: PASS.

---

## Self-Review

- Spec coverage: The plan keeps proxy code untouched, switches only OmniCode agent Anthropic-shape dispatch to official Anthropic SDK transport, and preserves OpenAI-shape dispatch.
- Placeholder scan: No TODO/TBD placeholders remain.
- Type consistency: `omniLLMAnthropicBaseURL`, `AnthropicSDKDispatch`, `OpenAISDKDispatch`, and `sdkProxyTestClient` signatures match existing code patterns.
- Scope check: This is a single focused runtime dispatch change; no decomposition needed.

## Execution Note

Do not create a git commit during implementation unless the user explicitly asks for one.
