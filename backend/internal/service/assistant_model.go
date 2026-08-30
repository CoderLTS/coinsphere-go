package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
)

const assistantToolRoundLimit = 6

type assistantChatMessage struct {
	Role       string              `json:"role"`
	Content    string              `json:"content,omitempty"`
	ToolCalls  []assistantToolCall `json:"tool_calls,omitempty"`
	ToolCallID string              `json:"tool_call_id,omitempty"`
}

type assistantToolCall struct {
	ID       string                    `json:"id"`
	Type     string                    `json:"type"`
	Function assistantToolCallFunction `json:"function"`
}

type assistantToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type assistantToolDefinition struct {
	Type     string                          `json:"type"`
	Function assistantToolDefinitionFunction `json:"function"`
}

type assistantToolDefinitionFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type assistantToolExecution func(context.Context, json.RawMessage) (json.RawMessage, *assistantWorkflowProposal, error)

type assistantRunResult struct {
	Content  string
	Proposal *assistantWorkflowProposal
}

type assistantCompletion struct {
	Content   string
	ToolCalls []assistantToolCall
}

func (a *App) runAssistantModel(ctx context.Context, runtime assistantModelRuntime, history []assistantChatMessage, principal *Principal, emit func(AssistantStreamEvent) error) (assistantRunResult, error) {
	definitions, executions := a.assistantToolCatalog(principal)
	messages := make([]assistantChatMessage, 0, len(history)+16)
	messages = append(messages, assistantChatMessage{Role: "system", Content: a.assistantSystemPrompt(definitions)})
	messages = append(messages, history...)
	contentParts := make([]string, 0, 8)
	contentBytes := 0
	var proposal *assistantWorkflowProposal

	for round := 0; round < assistantToolRoundLimit; round++ {
		completion, err := streamAssistantCompletion(ctx, runtime, messages, definitions, func(content string) error {
			contentBytes += len(content)
			if contentBytes > aiProviderResponseLimit {
				return errors.New("AI model response exceeds size limit")
			}
			contentParts = append(contentParts, content)
			return emit(AssistantStreamEvent{Name: "content", Data: map[string]string{"content": content}})
		})
		if err != nil {
			return assistantRunResult{}, err
		}
		if len(completion.ToolCalls) == 0 {
			return assistantRunResult{Content: strings.Join(contentParts, ""), Proposal: proposal}, nil
		}
		messages = append(messages, assistantChatMessage{Role: "assistant", Content: completion.Content, ToolCalls: completion.ToolCalls})
		for _, call := range completion.ToolCalls {
			execute, exists := executions[call.Function.Name]
			if err := emit(AssistantStreamEvent{Name: "tool", Data: map[string]string{"name": call.Function.Name, "status": "running"}}); err != nil {
				return assistantRunResult{}, err
			}
			var result json.RawMessage
			var candidate *assistantWorkflowProposal
			if !exists {
				result = json.RawMessage(`{"ok":false,"error":"unknown tool"}`)
			} else {
				result, candidate, err = execute(ctx, json.RawMessage(call.Function.Arguments))
				if err != nil {
					result, _ = json.Marshal(map[string]any{"ok": false, "error": err.Error()})
				}
			}
			status := "completed"
			if err != nil || !exists {
				status = "failed"
			}
			if err := emit(AssistantStreamEvent{Name: "tool", Data: map[string]string{"name": call.Function.Name, "status": status}}); err != nil {
				return assistantRunResult{}, err
			}
			if candidate != nil {
				proposal = candidate
			}
			if len(result) > 64<<10 {
				result = json.RawMessage(`{"ok":false,"error":"tool result exceeds 64 KiB"}`)
			}
			messages = append(messages, assistantChatMessage{
				Role: "tool", ToolCallID: call.ID, Content: string(result),
			})
		}
	}
	return assistantRunResult{}, errors.New("assistant tool call round limit exceeded")
}

func streamAssistantCompletion(ctx context.Context, runtime assistantModelRuntime, messages []assistantChatMessage, tools []assistantToolDefinition, onContent func(string) error) (assistantCompletion, error) {
	body, err := json.Marshal(map[string]any{
		"model": runtime.ModelName, "messages": messages, "tools": tools,
		"tool_choice": "auto", "stream": true,
	})
	if err != nil {
		return assistantCompletion{}, errors.New("encode AI model request failed")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, runtime.endpoint("chat/completions"), bytes.NewReader(body))
	if err != nil {
		return assistantCompletion{}, errors.New("build AI model request failed")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	runtime.authorize(request)
	response, err := runtime.client().Do(request)
	if err != nil {
		if response != nil {
			response.Body.Close()
		}
		if ctx.Err() != nil {
			return assistantCompletion{}, ctx.Err()
		}
		return assistantCompletion{}, errors.New("AI model request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = readBounded(response.Body, 64<<10)
		return assistantCompletion{}, errors.New("AI model rejected the request")
	}

	content := strings.Builder{}
	callParts := map[int]*assistantToolCall{}
	readBytes := 0
	streamDone := false
	err = scanAssistantSSE(response.Body, func(payload []byte) error {
		readBytes += len(payload)
		if readBytes > aiProviderResponseLimit {
			return errors.New("AI model response exceeds size limit")
		}
		if bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
			streamDone = true
			return nil
		}
		var chunk struct {
			Error   any `json:"error"`
			Choices []struct {
				Delta struct {
					Content   any `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal(payload, &chunk) != nil || chunk.Error != nil {
			return errors.New("AI model returned an invalid stream event")
		}
		if len(chunk.Choices) == 0 {
			return nil
		}
		delta := chunk.Choices[0].Delta
		if value, ok := delta.Content.(string); ok && value != "" {
			content.WriteString(value)
			if err := onContent(value); err != nil {
				return err
			}
		}
		for _, part := range delta.ToolCalls {
			call := callParts[part.Index]
			if call == nil {
				call = &assistantToolCall{Type: "function"}
				callParts[part.Index] = call
			}
			if part.ID != "" {
				call.ID = part.ID
			}
			if part.Type != "" {
				call.Type = part.Type
			}
			call.Function.Name += part.Function.Name
			call.Function.Arguments += part.Function.Arguments
		}
		return nil
	})
	if err != nil {
		return assistantCompletion{}, err
	}
	if !streamDone {
		return assistantCompletion{}, errors.New("AI model stream ended before completion")
	}
	indexes := make([]int, 0, len(callParts))
	for index := range callParts {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	calls := make([]assistantToolCall, 0, len(indexes))
	for _, index := range indexes {
		call := *callParts[index]
		if call.ID == "" || call.Function.Name == "" || !json.Valid([]byte(call.Function.Arguments)) {
			return assistantCompletion{}, errors.New("AI model returned an invalid tool call")
		}
		calls = append(calls, call)
	}
	return assistantCompletion{Content: content.String(), ToolCalls: calls}, nil
}

func scanAssistantSSE(reader io.Reader, consume func([]byte) error) error {
	scanner := bufio.NewScanner(io.LimitReader(reader, aiProviderResponseLimit+1))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	data := make([]string, 0, 1)
	total := 0
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		payload := []byte(strings.Join(data, "\n"))
		data = data[:0]
		return consume(payload)
	}
	for scanner.Scan() {
		line := scanner.Text()
		total += len(line) + 1
		if total > aiProviderResponseLimit {
			return errors.New("AI model response exceeds size limit")
		}
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return errors.New("read AI model stream failed")
	}
	if err := flush(); err != nil {
		return err
	}
	return nil
}
