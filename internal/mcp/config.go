// MCP server 配置解析
package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ServerConfig MCP server 配置
type ServerConfig struct {
	Name      string            `json:"name"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	Transport string            `json:"transport"`
	Type      string            `json:"type"`
	Timeout   int               `json:"timeout"`
	Active    *bool             `json:"active"`
}

// Enabled 返回该 server 是否启用
func (c ServerConfig) Enabled() bool {
	return c.Active == nil || *c.Active
}

// EffectiveTransport 推断实际传输类型，优先 Transport 字段，其次 Type，最后按 url 存否判断
func (c ServerConfig) EffectiveTransport() string {
	transport := strings.TrimSpace(strings.ToLower(c.Transport))
	if transport == "" {
		transport = strings.TrimSpace(strings.ToLower(c.Type))
	}
	if transport != "" {
		return transport
	}
	if strings.TrimSpace(c.URL) != "" {
		return "sse"
	}
	return "stdio"
}

// EffectiveTimeout 返回超时秒数，默认 15 秒
func (c ServerConfig) EffectiveTimeout() int {
	if c.Timeout <= 0 {
		return 15
	}
	return c.Timeout
}

// ParseServers 解析 MCP_SERVERS JSON 配置
func ParseServers(raw string) ([]ServerConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var list []ServerConfig
	if err := json.Unmarshal([]byte(raw), &list); err == nil {
		return normalizeServers(list)
	}
	var wrapper struct {
		Servers []ServerConfig `json:"servers"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err == nil && len(wrapper.Servers) > 0 {
		return normalizeServers(wrapper.Servers)
	}
	var mapped map[string]ServerConfig
	if err := json.Unmarshal([]byte(raw), &mapped); err == nil {
		for name, cfg := range mapped {
			cfg.Name = name
			list = append(list, cfg)
		}
		return normalizeServers(list)
	}
	return nil, fmt.Errorf("invalid MCP_SERVERS JSON")
}

// normalizeServers 校验并补全缺省名称
func normalizeServers(servers []ServerConfig) ([]ServerConfig, error) {
	result := make([]ServerConfig, 0, len(servers))
	for i, server := range servers {
		server.Name = strings.TrimSpace(server.Name)
		if server.Name == "" {
			server.Name = fmt.Sprintf("server_%d", i+1)
		}
		if strings.TrimSpace(server.Command) == "" && strings.TrimSpace(server.URL) == "" {
			return nil, fmt.Errorf("mcp server %s requires command or url", server.Name)
		}
		result = append(result, server)
	}
	return result, nil
}
