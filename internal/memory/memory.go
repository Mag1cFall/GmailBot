package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gmailbot/internal/agent"
	"gmailbot/internal/plugin"
)

type Store struct {
	mu   sync.RWMutex
	root string
}

func NewStore(root string) *Store {
	return &Store{root: root}
}

func (s *Store) UserDir(tgUserID int64) string {
	return filepath.Join(s.root, fmt.Sprintf("%d", tgUserID))
}

func (s *Store) ensureDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}

func (s *Store) ReadFile(tgUserID int64, name string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	path := filepath.Join(s.UserDir(tgUserID), name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func (s *Store) WriteFile(tgUserID int64, name, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.UserDir(tgUserID)
	if err := s.ensureDir(dir); err != nil {
		return err
	}
	subDir := filepath.Dir(filepath.Join(dir, name))
	if err := s.ensureDir(subDir); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0644)
}

func (s *Store) AppendFile(tgUserID int64, name, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.UserDir(tgUserID)
	if err := s.ensureDir(dir); err != nil {
		return err
	}
	path := filepath.Join(dir, name)
	if err := s.ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err
}

func (s *Store) ListFiles(tgUserID int64) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dir := s.UserDir(tgUserID)
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && (strings.HasSuffix(info.Name(), ".md") || strings.HasSuffix(info.Name(), ".jsonl")) {
			rel, _ := filepath.Rel(dir, path)
			files = append(files, rel)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return files, nil
}

type FileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func (s *Store) ListFilesWithSize(tgUserID int64) ([]FileInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dir := s.UserDir(tgUserID)
	var files []FileInfo
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			files = append(files, FileInfo{Name: rel, Size: info.Size()})
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return files, nil
}

func (s *Store) DeleteFile(tgUserID int64, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "..") {
		return fmt.Errorf("invalid file name")
	}
	path := filepath.Join(s.UserDir(tgUserID), name)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Store) ClearSessionTranscripts(tgUserID int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.UserDir(tgUserID), "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
				count++
			}
		}
	}
	return count, nil
}

func (s *Store) Search(tgUserID int64, query string) ([]SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dir := s.UserDir(tgUserID)
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, nil
	}

	var results []SearchResult
	keywords := strings.Fields(query)

	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		content := string(data)
		lower := strings.ToLower(content)
		score := 0
		for _, kw := range keywords {
			count := strings.Count(lower, kw)
			score += count
		}
		if score > 0 {
			rel, _ := filepath.Rel(dir, path)
			snippet := extractSnippet(content, keywords[0], 200)
			results = append(results, SearchResult{
				File:    rel,
				Score:   score,
				Snippet: snippet,
			})
		}
		return nil
	})

	sortResults(results)
	if len(results) > 10 {
		results = results[:10]
	}
	return results, nil
}

type SearchResult struct {
	File    string `json:"file"`
	Score   int    `json:"score"`
	Snippet string `json:"snippet"`
}

func extractSnippet(content, keyword string, maxLen int) string {
	lower := strings.ToLower(content)
	kwLower := strings.ToLower(keyword)
	idx := strings.Index(lower, kwLower)
	if idx < 0 {
		if len(content) > maxLen {
			return content[:maxLen] + "..."
		}
		return content
	}
	start := idx - 50
	if start < 0 {
		start = 0
	}
	end := idx + len(keyword) + maxLen
	if end > len(content) {
		end = len(content)
	}
	snippet := content[start:end]
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(content) {
		snippet = snippet + "..."
	}
	return snippet
}

func sortResults(results []SearchResult) {
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Score > results[j-1].Score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}

func (s *Store) SaveSessionTranscript(tgUserID int64, sessionID string, role, content string) error {
	entry := map[string]string{
		"time":    time.Now().UTC().Format(time.RFC3339),
		"role":    role,
		"content": content,
	}
	data, _ := json.Marshal(entry)
	return s.AppendFile(tgUserID, filepath.Join("sessions", sessionID+".jsonl"), string(data)+"\n")
}

type MemoryPlugin struct {
	store *Store
}

func NewPlugin(store *Store) *MemoryPlugin {
	return &MemoryPlugin{store: store}
}

func (p *MemoryPlugin) Name() string                     { return "memory" }
func (p *MemoryPlugin) Description() string              { return "用户记忆存储" }
func (p *MemoryPlugin) Shutdown() error                  { return nil }
func (p *MemoryPlugin) Commands() []plugin.Command       { return nil }
func (p *MemoryPlugin) EventHandlers() []plugin.EventSub { return nil }

func (p *MemoryPlugin) Init(ctx *plugin.Context) error {
	p.registerReadMemory(ctx.Registry)
	p.registerWriteMemory(ctx.Registry)
	p.registerSearchMemory(ctx.Registry)
	p.registerListMemory(ctx.Registry)
	p.registerDeleteMemory(ctx.Registry)
	return nil
}

func (p *MemoryPlugin) registerReadMemory(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "memory_read",
		Description: "读取用户的记忆文件（偏好、联系人、规则等）",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"file":{"type":"string","description":"记忆文件名，如 preferences.md, contacts.md, rules.md"}
			},
			"required":["file"]
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				File string `json:"file"`
			}
			json.Unmarshal(raw, &req)
			content, err := p.store.ReadFile(tc.TgUserID, req.File)
			if err != nil {
				return "", err
			}
			if content == "" {
				return agent.ToJSON(map[string]any{"content": "", "exists": false})
			}
			return agent.ToJSON(map[string]any{"content": content, "exists": true})
		},
		Active:   true,
		Category: "memory",
	})
}

func (p *MemoryPlugin) registerWriteMemory(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "memory_write",
		Description: "写入或更新用户的记忆文件",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"file":{"type":"string","description":"记忆文件名"},
				"content":{"type":"string","description":"要写入的完整内容（Markdown格式）"}
			},
			"required":["file","content"]
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				File    string `json:"file"`
				Content string `json:"content"`
			}
			json.Unmarshal(raw, &req)
			err := p.store.WriteFile(tc.TgUserID, req.File, req.Content)
			if err != nil {
				return "", err
			}
			return agent.ToJSON(map[string]any{"status": "saved"})
		},
		Active:   true,
		Category: "memory",
	})
}

func (p *MemoryPlugin) registerSearchMemory(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "memory_search",
		Description: "搜索用户记忆中的关键信息",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"query":{"type":"string","description":"搜索关键词"}
			},
			"required":["query"]
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				Query string `json:"query"`
			}
			json.Unmarshal(raw, &req)
			results, err := p.store.Search(tc.TgUserID, req.Query)
			if err != nil {
				return "", err
			}
			return agent.ToJSON(map[string]any{"results": results})
		},
		Active:   true,
		Category: "memory",
	})
}

func (p *MemoryPlugin) registerListMemory(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "memory_list",
		Description: "列出用户所有记忆文件",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{}
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			files, err := p.store.ListFiles(tc.TgUserID)
			if err != nil {
				return "", err
			}
			return agent.ToJSON(map[string]any{"files": files})
		},
		Active:   true,
		Category: "memory",
	})
}

func (p *MemoryPlugin) registerDeleteMemory(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "memory_delete",
		Description: "删除用户的指定记忆文件",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"file":{"type":"string","description":"要删除的记忆文件名"}
			},
			"required":["file"]
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				File string `json:"file"`
			}
			json.Unmarshal(raw, &req)
			err := p.store.DeleteFile(tc.TgUserID, req.File)
			if err != nil {
				return "", err
			}
			return agent.ToJSON(map[string]any{"status": "deleted", "file": req.File})
		},
		Active:   true,
		Category: "memory",
	})
}
