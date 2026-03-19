package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolRegistryLifecycleAndSchema(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(&ToolDef{
		Name:        "demo",
		Description: "demo tool",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`),
		Active:      true,
		Category:    "demo",
		Handler: func(ctx *ToolContext, args json.RawMessage) (string, error) {
			var req struct {
				Value string `json:"value"`
			}
			if err := json.Unmarshal(args, &req); err != nil {
				return "", err
			}
			return `{"echo":"` + req.Value + `"}`, nil
		},
	})

	if registry.Count() != 1 {
		t.Fatalf("expected one tool, got %d", registry.Count())
	}
	tool, ok := registry.Get("demo")
	if !ok || tool.Description != "demo tool" {
		t.Fatalf("unexpected tool lookup result: %#v %v", tool, ok)
	}
	result, err := registry.Execute(&ToolContext{}, "demo", `{"value":"hello"}`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result != `{"echo":"hello"}` {
		t.Fatalf("unexpected execution result: %s", result)
	}

	schemas := registry.OpenAITools()
	if len(schemas) != 1 || schemas[0].Function == nil || schemas[0].Function.Name != "demo" {
		t.Fatalf("unexpected exported schemas: %#v", schemas)
	}
	paramsJSON, err := json.Marshal(schemas[0].Function.Parameters)
	if err != nil {
		t.Fatalf("marshal schema parameters failed: %v", err)
	}
	if !strings.Contains(string(paramsJSON), `"required":["value"]`) {
		t.Fatalf("unexpected schema parameters: %s", string(paramsJSON))
	}

	registry.SetActive("demo", false)
	if _, err := registry.Execute(&ToolContext{}, "demo", `{"value":"hello"}`); err == nil {
		t.Fatal("expected disabled tool execution to fail")
	}
	registry.SetActive("demo", true)
	registry.Unregister("demo")
	if _, ok := registry.Get("demo"); ok {
		t.Fatal("expected tool to be removed")
	}
}
