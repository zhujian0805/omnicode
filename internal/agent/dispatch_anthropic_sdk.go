package agent

// dispatch_anthropic_sdk.go — DispatchFn backed by the official Anthropic Go SDK.
// Unlike NewChatCompletionsDispatch (which routes through the local OmniLLM proxy),
// AnthropicSDKDispatch connects directly to api.anthropic.com. The base URL is
// overridable via ANTHROPIC_BASE_URL so the call can still be proxied when needed.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
)

// AnthropicSDKDispatch returns a DispatchFn that calls the Anthropic Messages
// API directly using the official anthropic-sdk-go client.
//
// apiKey must be a valid Anthropic API key (or "" to let the SDK fall back to
// the ANTHROPIC_API_KEY environment variable).  Pass a non-empty baseURL to
// override the default https://api.anthropic.com endpoint — useful for
// pointing at OmniLLM's /v1/messages proxy or a local test server.
func AnthropicSDKDispatch(apiKey, baseURL string) DispatchFn {
	opts := []option.RequestOption{}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	opts = append(opts, anthropicSDKClientOptionsFromEnv()...)
	client := anthropic.NewClient(opts...)

	return func(ctx context.Context, req *MessagesRequest) (<-chan *MessagesResponse, error) {
		params, err := anthropicParamsFromRequest(req)
		if err != nil {
			return nil, fmt.Errorf("anthropic-sdk: build params: %w", err)
		}

		requestOpts := anthropicSDKRequestOptionsFromEnv()
		if anthropicSDKStreamingEnabled(req) {
			stream := client.Messages.NewStreaming(ctx, params, requestOpts...)
			resp, err := anthropicStreamToResponse(stream)
			if err != nil {
				return nil, wrapAnthropicSDKError("messages.new_streaming", err)
			}

			ch := make(chan *MessagesResponse, 1)
			ch <- resp
			close(ch)
			return ch, nil
		}

		msg, err := client.Messages.New(ctx, params, requestOpts...)
		if err != nil {
			return nil, wrapAnthropicSDKError("messages.new", err)
		}

		resp := anthropicMsgToResponse(msg)

		ch := make(chan *MessagesResponse, 1)
		ch <- resp
		close(ch)
		return ch, nil
	}
}

func anthropicSDKClientOptionsFromEnv() []option.RequestOption {
	maxRetries, ok := lookupIntEnv("ANTHROPIC_SDK_MAX_RETRIES")
	if !ok {
		return nil
	}
	return []option.RequestOption{option.WithMaxRetries(maxRetries)}
}

func anthropicSDKRequestOptionsFromEnv() []option.RequestOption {
	requestTimeout, ok := lookupDurationMSEnv("ANTHROPIC_SDK_REQUEST_TIMEOUT_MS")
	if !ok {
		return nil
	}
	return []option.RequestOption{option.WithRequestTimeout(requestTimeout)}
}

func anthropicSDKStreamingEnabled(req *MessagesRequest) bool {
	if req != nil && req.Stream {
		return true
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv("ANTHROPIC_SDK_STREAMING")))
	switch v {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func lookupIntEnv(name string) (int, bool) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	if v < 0 {
		return 0, false
	}
	return v, true
}

func lookupDurationMSEnv(name string) (time.Duration, bool) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return 0, false
	}
	return time.Duration(v) * time.Millisecond, true
}

func wrapAnthropicSDKError(action string, err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		if apiErr.RequestID != "" {
			return fmt.Errorf("anthropic-sdk: %s (request_id=%s): %w", action, apiErr.RequestID, err)
		}
		return fmt.Errorf("anthropic-sdk: %s (status=%d): %w", action, apiErr.StatusCode, err)
	}
	return fmt.Errorf("anthropic-sdk: %s: %w", action, err)
}

func anthropicStreamToResponse(stream *ssestream.Stream[anthropic.MessageStreamEventUnion]) (*MessagesResponse, error) {
	if stream == nil {
		return nil, fmt.Errorf("anthropic stream is nil")
	}
	defer stream.Close()

	resp := &MessagesResponse{StopReason: StopReasonEndTurn}
	type streamBlockState struct {
		block       ContentBlock
		partialJSON strings.Builder
	}
	blocks := map[int]*streamBlockState{}
	orderedIndexes := map[int]struct{}{}

	for stream.Next() {
		event := stream.Current()
		switch ev := event.AsAny().(type) {
		case anthropic.MessageStartEvent:
			resp.ID = ev.Message.ID
			resp.Model = string(ev.Message.Model)
			resp.Usage = &Usage{
				InputTokens:  int(ev.Message.Usage.InputTokens),
				OutputTokens: int(ev.Message.Usage.OutputTokens),
			}
		case anthropic.ContentBlockStartEvent:
			idx := int(ev.Index)
			blocks[idx] = &streamBlockState{block: anthropicStartBlockToResponseBlock(ev.ContentBlock)}
			orderedIndexes[idx] = struct{}{}
		case anthropic.ContentBlockDeltaEvent:
			idx := int(ev.Index)
			state, ok := blocks[idx]
			if !ok {
				state = &streamBlockState{}
				blocks[idx] = state
				orderedIndexes[idx] = struct{}{}
			}
			switch delta := ev.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				state.block.Text += delta.Text
			case anthropic.ThinkingDelta:
				state.block.Thinking += delta.Thinking
			case anthropic.SignatureDelta:
				sig := delta.Signature
				state.block.Signature = &sig
			case anthropic.InputJSONDelta:
				state.partialJSON.WriteString(delta.PartialJSON)
			}
		case anthropic.MessageDeltaEvent:
			resp.StopReason = mapAnthropicStopReason(ev.Delta.StopReason)
			if ev.Delta.StopSequence != "" {
				stopSequence := ev.Delta.StopSequence
				resp.StopSequence = &stopSequence
			}
			if resp.Usage == nil {
				resp.Usage = &Usage{}
			}
			resp.Usage.OutputTokens = int(ev.Usage.OutputTokens)
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	indexes := make([]int, 0, len(orderedIndexes))
	for idx := range orderedIndexes {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	for _, idx := range indexes {
		state := blocks[idx]
		if state == nil {
			continue
		}
		if state.block.Type == "tool_use" && state.partialJSON.Len() > 0 {
			state.block.Input = decodeToolUseInputJSON(state.partialJSON.String())
		}
		if state.block.Type == "" {
			continue
		}
		resp.Content = append(resp.Content, state.block)
	}

	return resp, nil
}

func anthropicStartBlockToResponseBlock(block anthropic.ContentBlockStartEventContentBlockUnion) ContentBlock {
	switch block.Type {
	case "text":
		return ContentBlock{Type: "text", Text: block.Text}
	case "thinking":
		sig := block.Signature
		return ContentBlock{Type: "thinking", Thinking: block.Thinking, Signature: &sig}
	case "tool_use":
		input, _ := block.Input.(map[string]any)
		if input == nil {
			input = map[string]any{}
		}
		return ContentBlock{Type: "tool_use", ID: block.ID, Name: block.Name, Input: input}
	default:
		return ContentBlock{Type: block.Type}
	}
}

func decodeToolUseInputJSON(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return map[string]any{}
	}
	return m
}

// ─── conversion helpers ───────────────────────────────────────────────────────

const defaultAnthropicMaxTokens = 4096

func buildAnthropicMessagesJSON(model string, req *MessagesRequest, forceStream bool) (json.RawMessage, error) {
	if req == nil {
		req = &MessagesRequest{}
	}

	reqCopy := *req
	if trimmedModel := strings.TrimSpace(model); trimmedModel != "" {
		reqCopy.Model = trimmedModel
	}
	if forceStream {
		reqCopy.Stream = true
	}

	params, err := anthropicParamsFromRequest(&reqCopy)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic messages request: %w", err)
	}

	if !reqCopy.Stream {
		return json.RawMessage(payload), nil
	}

	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, fmt.Errorf("decode anthropic messages request: %w", err)
	}
	body["stream"] = true

	payload, err = json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic streaming request: %w", err)
	}

	return json.RawMessage(payload), nil
}

func anthropicParamsFromRequest(req *MessagesRequest) (anthropic.MessageNewParams, error) {
	if req == nil {
		req = &MessagesRequest{}
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "claude-opus-4-5"
	}

	maxTokens := int64(defaultAnthropicMaxTokens)
	if req.MaxTokens > 0 {
		maxTokens = int64(req.MaxTokens)
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: maxTokens,
	}

	if len(req.System) > 0 {
		params.System = make([]anthropic.TextBlockParam, 0, len(req.System))
		for _, block := range req.System {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				params.System = append(params.System, anthropic.TextBlockParam{Text: strings.TrimSpace(block.Text)})
			}
		}
	}

	messages, err := messagesToAnthropicParams(req.Messages)
	if err != nil {
		return params, err
	}
	params.Messages = messages

	if len(req.Tools) > 0 {
		tools := make([]anthropic.ToolUnionParam, 0, len(req.Tools))
		for _, t := range req.Tools {
			desc := ""
			if t.Description != nil {
				desc = *t.Description
			}
			properties, required := anthropicToolSchemaParts(t.InputSchema)
			tools = append(tools, anthropic.ToolUnionParam{
				OfTool: &anthropic.ToolParam{
					Name:        t.Name,
					Description: anthropic.String(desc),
					InputSchema: anthropic.ToolInputSchemaParam{
						Properties: properties,
						Required:   required,
					},
				},
			})
		}
		params.Tools = tools
		if req.ToolChoice != nil {
			params.ToolChoice = canonicalToolChoiceToAnthropicSDK(req.ToolChoice)
		} else {
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{},
			}
		}
	}

	return params, nil
}

func anthropicToolSchemaParts(schema map[string]any) (any, []string) {
	if schema == nil {
		return map[string]any{}, nil
	}
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		properties = map[string]any{}
	}
	requiredAny, _ := schema["required"].([]string)
	if requiredAny != nil {
		return properties, requiredAny
	}
	requiredRaw, _ := schema["required"].([]any)
	if len(requiredRaw) == 0 {
		return properties, nil
	}
	required := make([]string, 0, len(requiredRaw))
	for _, item := range requiredRaw {
		if text, ok := item.(string); ok && text != "" {
			required = append(required, text)
		}
	}
	return properties, required
}

func messagesToAnthropicParams(msgs []Message) ([]anthropic.MessageParam, error) {
	var out []anthropic.MessageParam
	for _, msg := range msgs {
		switch msg.Role {
		case "system":
			continue
		case "user":
			blocks := userBlocksToAnthropicBlocks(msg.Content)
			if len(blocks) > 0 {
				out = append(out, anthropic.NewUserMessage(blocks...))
			}
		case "assistant":
			blocks := assistantBlocksToAnthropicBlocks(msg.Content)
			if len(blocks) > 0 {
				out = append(out, anthropic.NewAssistantMessage(blocks...))
			}
		}
	}
	return out, nil
}

func userBlocksToAnthropicBlocks(parts []ContentBlock) []anthropic.ContentBlockParamUnion {
	var blocks []anthropic.ContentBlockParamUnion
	for _, part := range parts {
		switch part.Type {
		case "text":
			blocks = append(blocks, anthropic.NewTextBlock(part.Text))
		case "tool_result":
			isError := false
			if part.IsError != nil {
				isError = *part.IsError
			}
			toolUseID := part.ToolUseID
			if toolUseID == "" {
				toolUseID = part.ID
			}
			blocks = append(blocks, anthropic.NewToolResultBlock(toolUseID, part.Content, isError))
		}
	}
	return blocks
}

func assistantBlocksToAnthropicBlocks(parts []ContentBlock) []anthropic.ContentBlockParamUnion {
	var blocks []anthropic.ContentBlockParamUnion
	for _, part := range parts {
		switch part.Type {
		case "text":
			blocks = append(blocks, anthropic.NewTextBlock(part.Text))
		case "tool_use":
			blocks = append(blocks, anthropic.NewToolUseBlock(part.ID, part.Input, part.Name))
		}
	}
	return blocks
}

func canonicalToolChoiceToAnthropicSDK(choice any) anthropic.ToolChoiceUnionParam {
	switch v := choice.(type) {
	case string:
		switch v {
		case "required":
			return anthropic.ToolChoiceUnionParam{OfAny: &anthropic.ToolChoiceAnyParam{}}
		case "none":
			return anthropic.ToolChoiceUnionParam{OfNone: &anthropic.ToolChoiceNoneParam{}}
		case "auto":
			fallthrough
		default:
			return anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{}}
		}
	case map[string]any:
		if typ, _ := v["type"].(string); typ == "function" {
			if name, _ := v["functionName"].(string); name != "" {
				return anthropic.ToolChoiceUnionParam{OfTool: &anthropic.ToolChoiceToolParam{Name: name}}
			}
		}
	}
	return anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{}}
}

func anthropicMsgToResponse(msg *anthropic.Message) *MessagesResponse {
	resp := &MessagesResponse{
		ID:    msg.ID,
		Model: string(msg.Model),
	}

	resp.StopReason = mapAnthropicStopReason(msg.StopReason)

	if msg.StopSequence != "" {
		stopSequence := msg.StopSequence
		resp.StopSequence = &stopSequence
	}

	resp.Usage = &Usage{
		InputTokens:  int(msg.Usage.InputTokens),
		OutputTokens: int(msg.Usage.OutputTokens),
	}

	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			resp.Content = append(resp.Content, ContentBlock{Type: "text", Text: block.Text})
		case "tool_use":
			args := rawMessageToMap(block.Input)
			resp.Content = append(resp.Content, ContentBlock{Type: "tool_use", ID: block.ID, Name: block.Name, Input: args})
		}
	}

	return resp
}

func mapAnthropicStopReason(reason anthropic.StopReason) StopReason {
	switch reason {
	case anthropic.StopReasonToolUse:
		return StopReasonToolUse
	case anthropic.StopReasonMaxTokens:
		return StopReasonMaxTokens
	case anthropic.StopReasonStopSequence:
		return StopReasonStopSequence
	default:
		return StopReasonEndTurn
	}
}

func rawMessageToMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{}
	}
	return m
}
