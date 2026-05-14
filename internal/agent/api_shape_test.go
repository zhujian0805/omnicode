package agent

import "testing"

func TestIsClaudeModel(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"claude-opus-4-5", true},
		{"claude-sonnet-4-5-20250514", true},
		{"Claude-3-Haiku", true},
		{"  claude-3-opus  ", true},
		{"gpt-4o", false},
		{"gpt-4o-mini", false},
		{"gemini-pro", false},
		{"", false},
		{"my-custom-model", false},
		{"CLAUDE-OPUS-4", true},
	}
	for _, tc := range cases {
		if got := isClaudeModel(tc.model); got != tc.want {
			t.Errorf("isClaudeModel(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestNormalizeAPIShape(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "anthropic"},
		{"anthropic", "anthropic"},
		{"messages", "anthropic"},
		{"/v1/messages", "anthropic"},
		{"openai", "openai"},
		{"chat", "openai"},
		{"/v1/chat/completions", "openai"},
		{"responses", "responses"},
		{"/v1/responses", "responses"},
		{"unknown", "anthropic"},
	}

	for _, tc := range cases {
		if got := normalizeAPIShape(tc.in); got != tc.want {
			t.Fatalf("normalizeAPIShape(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
