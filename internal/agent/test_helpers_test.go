package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"gmailbot/internal/store"
	"gmailbot/internal/testutil"

	openai "github.com/sashabaranov/go-openai"
)

type scriptedResult struct {
	response ChatResponse
	err      error
}

type scriptedProvider struct {
	name     string
	mu       sync.Mutex
	results  []scriptedResult
	requests []ChatRequest
	index    int
	window   int
}

func newScriptedProvider(name string, results ...scriptedResult) *scriptedProvider {
	return &scriptedProvider{name: name, results: results}
}

func (p *scriptedProvider) Name() string {
	if p.name == "" {
		return "scripted"
	}
	return p.name
}

func (p *scriptedProvider) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, cloneChatRequest(req))
	if p.index >= len(p.results) {
		return ChatResponse{}, errors.New("no scripted response available")
	}
	result := p.results[p.index]
	p.index++
	return result.response, result.err
}

func (p *scriptedProvider) FetchContextWindow(ctx context.Context, model string) (int, error) {
	if p.window > 0 {
		return p.window, nil
	}
	return 0, errors.New("context window unavailable")
}

func (p *scriptedProvider) Requests() []ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ChatRequest, len(p.requests))
	for i, req := range p.requests {
		out[i] = cloneChatRequest(req)
	}
	return out
}

func cloneChatRequest(req ChatRequest) ChatRequest {
	cloned := ChatRequest{
		Model: req.Model,
		Tools: append([]openai.Tool(nil), req.Tools...),
	}
	if len(req.Messages) > 0 {
		cloned.Messages = make([]ChatMessage, len(req.Messages))
		for i, msg := range req.Messages {
			cloned.Messages[i] = ChatMessage{
				Role:       msg.Role,
				Content:    msg.Content,
				ToolCallID: msg.ToolCallID,
				Name:       msg.Name,
				ToolCalls:  append([]openai.ToolCall(nil), msg.ToolCalls...),
			}
		}
	}
	return cloned
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	return testutil.NewTestStore(t)
}
