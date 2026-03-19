package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gmailbot/internal/agent"
)

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type Manager struct {
	registry *agent.ToolRegistry
	servers  []*serverClient
}

type serverClient struct {
	name      string
	config    ServerConfig
	transport transport
	tools     []Tool
}

type transport interface {
	Initialize(ctx context.Context) error
	ListTools(ctx context.Context) ([]Tool, error)
	CallTool(ctx context.Context, name string, args json.RawMessage) (string, error)
	Close() error
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func NewManager(rawConfig string, registry *agent.ToolRegistry) (*Manager, error) {
	servers, err := ParseServers(rawConfig)
	if err != nil {
		return nil, err
	}
	manager := &Manager{registry: registry}
	for _, cfg := range servers {
		if !cfg.Enabled() {
			continue
		}
		manager.servers = append(manager.servers, &serverClient{name: cfg.Name, config: cfg})
	}
	return manager, nil
}

func (m *Manager) Start(ctx context.Context) error {
	for _, server := range m.servers {
		if err := server.start(ctx); err != nil {
			return err
		}
		server.registerTools(m.registry)
	}
	return nil
}

func (m *Manager) Shutdown() error {
	var joined error
	for _, server := range m.servers {
		if err := server.transport.Close(); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func (s *serverClient) start(ctx context.Context) error {
	var tr transport
	switch s.config.EffectiveTransport() {
	case "stdio":
		transport, err := newStdioTransport(s.config)
		if err != nil {
			return err
		}
		tr = transport
	default:
		transport, err := newSSETransport(s.config)
		if err != nil {
			return err
		}
		tr = transport
	}
	s.transport = tr
	if err := s.transport.Initialize(ctx); err != nil {
		return err
	}
	tools, err := s.transport.ListTools(ctx)
	if err != nil {
		return err
	}
	s.tools = tools
	return nil
}

func (s *serverClient) registerTools(registry *agent.ToolRegistry) {
	for _, tool := range s.tools {
		toolCopy := tool
		name := toolCopy.Name
		if _, exists := registry.Get(name); exists {
			name = s.name + "__" + toolCopy.Name
		}
		registry.Register(&agent.ToolDef{
			Name:        name,
			Description: fmt.Sprintf("%s（MCP:%s）", toolCopy.Description, s.name),
			Parameters:  toolCopy.InputSchema,
			Handler: func(tc *agent.ToolContext, args json.RawMessage) (string, error) {
				callCtx := tc.Context
				if callCtx == nil {
					callCtx = context.Background()
				}
				timeoutCtx, cancel := context.WithTimeout(callCtx, time.Duration(s.config.EffectiveTimeout())*time.Second)
				defer cancel()
				return s.transport.CallTool(timeoutCtx, toolCopy.Name, args)
			},
			Active:   true,
			Category: "mcp",
		})
	}
}

type stdioTransport struct {
	config ServerConfig
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	mu     sync.Mutex
	nextID int64
}

func newStdioTransport(cfg ServerConfig) (*stdioTransport, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, fmt.Errorf("stdio transport requires command")
	}
	return &stdioTransport{config: cfg}, nil
}

func (t *stdioTransport) Initialize(ctx context.Context) error {
	commandCtx, cancel := context.WithCancel(ctx)
	_ = cancel
	cmd := exec.CommandContext(commandCtx, t.config.Command, t.config.Args...)
	cmd.Env = os.Environ()
	for key, value := range t.config.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}
	t.cmd = cmd
	t.stdin = stdin
	t.stdout = bufio.NewReader(stdout)
	_, err = t.request(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "gmailbot", "version": "1.0.0"},
	})
	if err != nil {
		return err
	}
	return t.notify("notifications/initialized", map[string]any{})
}

func (t *stdioTransport) ListTools(ctx context.Context) ([]Tool, error) {
	raw, err := t.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

func (t *stdioTransport) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	var payload any = map[string]any{}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &payload)
	}
	raw, err := t.request(ctx, "tools/call", map[string]any{"name": name, "arguments": payload})
	if err != nil {
		return "", err
	}
	return normalizeCallResult(raw), nil
}

func (t *stdioTransport) Close() error {
	if t.stdin != nil {
		_ = t.stdin.Close()
	}
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
		_, _ = t.cmd.Process.Wait()
	}
	return nil
}

func (t *stdioTransport) notify(method string, params any) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return writeFramedMessage(t.stdin, rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (t *stdioTransport) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	id := atomic.AddInt64(&t.nextID, 1)
	if err := writeFramedMessage(t.stdin, rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}
	for {
		messageCh := make(chan []byte, 1)
		errCh := make(chan error, 1)
		go func() {
			message, err := readFramedMessage(t.stdout)
			if err != nil {
				errCh <- err
				return
			}
			messageCh <- message
		}()
		var message []byte
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err := <-errCh:
			return nil, err
		case message = <-messageCh:
		}
		var response rpcResponse
		if err := json.Unmarshal(message, &response); err != nil {
			continue
		}
		if response.ID != id {
			continue
		}
		if response.Error != nil {
			return nil, errors.New(response.Error.Message)
		}
		return response.Result, nil
	}
}

type sseTransport struct {
	config   ServerConfig
	client   *http.Client
	endpoint string
	nextID   int64
	mu       sync.Mutex
	pending  map[int64]chan rpcResponse
	cancel   context.CancelFunc
}

func newSSETransport(cfg ServerConfig) (*sseTransport, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, fmt.Errorf("sse transport requires url")
	}
	return &sseTransport{
		config:  cfg,
		client:  &http.Client{Timeout: time.Duration(cfg.EffectiveTimeout()) * time.Second},
		pending: map[int64]chan rpcResponse{},
	}, nil
}

func (t *sseTransport) Initialize(ctx context.Context) error {
	connCtx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	req, err := http.NewRequestWithContext(connCtx, http.MethodGet, t.config.URL, nil)
	if err != nil {
		return err
	}
	for key, value := range t.config.Headers {
		req.Header.Set(key, value)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	go t.readLoop(connCtx, resp.Body)
	deadline := time.After(time.Duration(t.config.EffectiveTimeout()) * time.Second)
	for strings.TrimSpace(t.endpoint) == "" {
		select {
		case <-deadline:
			t.endpoint = t.config.URL
		case <-time.After(50 * time.Millisecond):
		}
		if strings.TrimSpace(t.endpoint) != "" {
			break
		}
	}
	_, err = t.request(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "gmailbot", "version": "1.0.0"},
	})
	if err != nil {
		return err
	}
	_, err = t.post(ctx, 0, "notifications/initialized", map[string]any{})
	return err
}

func (t *sseTransport) ListTools(ctx context.Context) ([]Tool, error) {
	raw, err := t.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

func (t *sseTransport) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	var payload any = map[string]any{}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &payload)
	}
	raw, err := t.request(ctx, "tools/call", map[string]any{"name": name, "arguments": payload})
	if err != nil {
		return "", err
	}
	return normalizeCallResult(raw), nil
}

func (t *sseTransport) Close() error {
	if t.cancel != nil {
		t.cancel()
	}
	return nil
}

func (t *sseTransport) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&t.nextID, 1)
	responseCh := make(chan rpcResponse, 1)
	t.mu.Lock()
	t.pending[id] = responseCh
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
	}()
	result, err := t.post(ctx, id, method, params)
	if err == nil && len(result) > 0 {
		return result, nil
	}
	select {
	case response := <-responseCh:
		if response.Error != nil {
			return nil, errors.New(response.Error.Message)
		}
		return response.Result, nil
	case <-time.After(time.Duration(t.config.EffectiveTimeout()) * time.Second):
		return nil, fmt.Errorf("mcp sse request timeout")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *sseTransport) post(ctx context.Context, id int64, method string, params any) (json.RawMessage, error) {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	endpoint := t.endpoint
	if endpoint == "" {
		endpoint = t.config.URL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, value := range t.config.Headers {
		req.Header.Set(key, value)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("mcp sse request failed: %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, nil
	}
	var response rpcResponse
	if err := json.Unmarshal(trimmed, &response); err == nil {
		if response.Error != nil {
			return nil, errors.New(response.Error.Message)
		}
		return response.Result, nil
	}
	return nil, nil
}

func (t *sseTransport) readLoop(ctx context.Context, body io.ReadCloser) {
	defer body.Close()
	reader := bufio.NewReader(body)
	var eventType string
	var dataLines []string
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			payload := strings.Join(dataLines, "\n")
			t.processSSEEvent(eventType, payload)
			eventType = ""
			dataLines = nil
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
}

func (t *sseTransport) processSSEEvent(eventType, payload string) {
	if strings.TrimSpace(payload) == "" {
		return
	}
	if eventType == "endpoint" {
		base, err := url.Parse(t.config.URL)
		if err != nil {
			t.endpoint = payload
			return
		}
		ref, err := url.Parse(payload)
		if err != nil {
			t.endpoint = payload
			return
		}
		t.endpoint = base.ResolveReference(ref).String()
		return
	}
	var response rpcResponse
	if err := json.Unmarshal([]byte(payload), &response); err != nil {
		return
	}
	t.mu.Lock()
	ch := t.pending[response.ID]
	t.mu.Unlock()
	if ch != nil {
		ch <- response
	}
}

func writeFramedMessage(writer io.Writer, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = writer.Write(body)
	return err
}

func readFramedMessage(reader *bufio.Reader) ([]byte, error) {
	contentLength := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			value := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(line), "content-length:"))
			length, err := strconv.Atoi(value)
			if err != nil {
				return nil, err
			}
			contentLength = length
		}
	}
	if contentLength <= 0 {
		return nil, fmt.Errorf("invalid content length")
	}
	message := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, message); err != nil {
		return nil, err
	}
	return message, nil
}

func normalizeCallResult(raw json.RawMessage) string {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return string(raw)
	}
	data, err := json.Marshal(decoded)
	if err != nil {
		return string(raw)
	}
	return string(data)
}
