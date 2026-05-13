package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	toolspkg "omnicode/internal/tools"
)

type nativeLiveClient struct {
	baseURL string
	apiKey  string
}

func (c *nativeLiveClient) GetBaseURL() string { return c.baseURL }
func (c *nativeLiveClient) GetAPIKey() string  { return c.apiKey }

func (c *nativeLiveClient) Post(path string, body any) ([]byte, error) {
	jsonBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("server error (%d): %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func (c *nativeLiveClient) PostStream(path string, body any) (*http.Response, error) {
	return nil, fmt.Errorf("streaming not used")
}

func TestNativeOmniCodeAgentLiveHaikuExplainCodebase(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("OMNILLM_API_KEY"))
	if apiKey == "" {
		data, err := os.ReadFile(filepath.Join(os.Getenv("USERPROFILE"), ".config", "omnillm", "api-key"))
		if err != nil {
			t.Fatalf("read api key: %v", err)
		}
		apiKey = strings.TrimSpace(string(data))
	}

	client := &nativeLiveClient{baseURL: "http://127.0.0.1:5000", apiKey: apiKey}
	prompt := `Explain codebase.

Requirements:
- Inspect the repository directly with read-only tools before answering.
- Stay in the actual repo root C:\Users\jzhu\repos\omnillm.
- Ignore .claude/, .git/, node_modules/, dist/, build/, and any worktree/cache directories.
- Use a small inspection plan: first map top-level files, then inspect representative Go backend files under internal/ and cmd/, then inspect representative React frontend files under frontend/.
- Use at most 12 tool calls total, then stop using tools and provide the final summary.
- Final summary must cover architecture, request/response flow, provider abstractions, frontend, testing, extension points, and risks, with specific file paths inspected.`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var approvedTools []string
	checker := func(_ context.Context, req toolspkg.PermissionRequest) (bool, error) {
		switch req.ToolName {
		case "glob", "grep", "read", "ls", "todo_write", "task_create", "task_update", "task_list", "task_get":
			approvedTools = append(approvedTools, req.ToolName)
			return true, nil
		default:
			return false, nil
		}
	}

	result, err := RunTurn(ctx, client, "native-live-haiku-"+fmt.Sprint(time.Now().UnixNano()), "claude-haiku-4.5", "omnicode", DefaultAPIShape, prompt, nil, checker, nil, 25)
	if err != nil {
		t.Fatalf("RunTurn error: %v", err)
	}
	out := strings.TrimSpace(result.Output)
	if len(out) < 400 {
		t.Fatalf("output too short: %d chars\n%s", len(out), out)
	}
	if len(approvedTools) == 0 {
		t.Fatal("no native tool calls were approved")
	}
	for _, signal := range []string{"internal/", "frontend/", "provider", "test"} {
		if !strings.Contains(strings.ToLower(out), signal) {
			t.Fatalf("output missing signal %q\n%s", signal, out)
		}
	}
	t.Logf("tools=%v output_chars=%d steps=%d", approvedTools, len(out), result.Steps)
	t.Logf("output:\n%s", out)
}
