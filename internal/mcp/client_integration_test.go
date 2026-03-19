package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"gmailbot/internal/agent"
)

func TestManagerRegistersAndCallsStdioTool(t *testing.T) {
	registry := agent.NewToolRegistry()
	config := []ServerConfig{{
		Name:    "helper",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessMCPServer", "--"},
	}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal config failed: %v", err)
	}
	manager, err := NewManager(string(raw), registry)
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	t.Setenv("GO_WANT_MCP_HELPER", "1")
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("start manager failed: %v", err)
	}
	defer manager.Shutdown()
	tool, ok := registry.Get("echo")
	if !ok {
		t.Fatalf("expected echo tool to be registered")
	}
	result, err := tool.Handler(&agent.ToolContext{}, json.RawMessage(`{"text":"hello"}`))
	if err != nil {
		t.Fatalf("call tool failed: %v", err)
	}
	t.Logf("mcp tool result: %s", result)
	if !strings.Contains(result, "hello") {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestHelperProcessMCPServer(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		message, err := readFramedMessage(reader)
		if err != nil {
			os.Exit(0)
		}
		var request rpcRequest
		if err := json.Unmarshal(message, &request); err != nil {
			continue
		}
		if request.ID == 0 {
			continue
		}
		switch request.Method {
		case "initialize":
			_ = writeFramedMessage(os.Stdout, rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: json.RawMessage(`{"protocolVersion":"2024-11-05"}`)})
		case "tools/list":
			_ = writeFramedMessage(os.Stdout, rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: json.RawMessage(`{"tools":[{"name":"echo","description":"echo text","inputSchema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}}]}`)})
		case "tools/call":
			_ = writeFramedMessage(os.Stdout, rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`)})
		}
	}
}
