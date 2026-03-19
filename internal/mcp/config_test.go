package mcp

import "testing"

func TestParseServersSupportsArrayAndMap(t *testing.T) {
	servers, err := ParseServers(`[{"name":"stdio","command":"node","args":["server.js"]}]`)
	if err != nil {
		t.Fatalf("parse array failed: %v", err)
	}
	if len(servers) != 1 || servers[0].EffectiveTransport() != "stdio" {
		t.Fatalf("unexpected array parse result: %#v", servers)
	}
	servers, err = ParseServers(`{"demo":{"url":"https://example.com/sse","transport":"sse"}}`)
	if err != nil {
		t.Fatalf("parse map failed: %v", err)
	}
	if len(servers) != 1 || servers[0].Name != "demo" || servers[0].EffectiveTransport() != "sse" {
		t.Fatalf("unexpected map parse result: %#v", servers)
	}
}
