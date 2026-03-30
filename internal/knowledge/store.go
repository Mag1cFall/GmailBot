// 知识库存储和检索
package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gmailbot/internal/agent"
	"gmailbot/internal/plugin"
)

// Document 知识库文档
type Document struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// Store 基于文件系统的知识库
type Store struct {
	mu   sync.RWMutex
	root string
	docs map[string]*Document
}

// NewStore 创建知识库，启动时加载已有文档
func NewStore(root string) *Store {
	s := &Store{
		root: root,
		docs: make(map[string]*Document),
	}
	s.loadExisting()
	return s
}

// loadExisting 扫描 root 目录，加载所有 .md 文档到内存
func (s *Store) loadExisting() {
	_ = os.MkdirAll(s.root, 0755)
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.root, e.Name()))
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".md")
		s.docs[id] = &Document{
			ID:      id,
			Title:   id,
			Content: string(data),
		}
	}
}

// Add 添加文档并持久化到磁盘
func (s *Store) Add(_ context.Context, id, title, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.MkdirAll(s.root, 0755)
	doc := &Document{ID: id, Title: title, Content: content}
	s.docs[id] = doc
	return os.WriteFile(filepath.Join(s.root, id+".md"), []byte(content), 0644)
}

// Delete 删除文档
func (s *Store) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.docs, id)
	path := filepath.Join(s.root, id+".md")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Search 按关键词搜索文档，按嵌入频次降序返回 topK 结果
func (s *Store) Search(_ context.Context, query string, topK int) []SearchResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	keywords := strings.Fields(query)
	if topK <= 0 {
		topK = 5
	}

	var results []SearchResult
	for _, doc := range s.docs {
		lower := strings.ToLower(doc.Content)
		score := 0
		for _, kw := range keywords {
			score += strings.Count(lower, kw)
		}
		if score > 0 {
			snippet := extractSnippet(doc.Content, keywords[0], 300)
			results = append(results, SearchResult{
				DocID:   doc.ID,
				Title:   doc.Title,
				Score:   score,
				Snippet: snippet,
			})
		}
	}

	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Score > results[j-1].Score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}

	if len(results) > topK {
		results = results[:topK]
	}
	return results
}

// List 返回所有文档的 ID 和标题
func (s *Store) List() []Document {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var docs []Document
	for _, doc := range s.docs {
		docs = append(docs, Document{ID: doc.ID, Title: doc.Title})
	}
	return docs
}

// SearchResult 检索结果
type SearchResult struct {
	DocID   string `json:"doc_id"`
	Title   string `json:"title"`
	Score   int    `json:"score"`
	Snippet string `json:"snippet"`
}

// extractSnippet 提取关键词付近的文本片段
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
	start := idx - 80
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

// KnowledgePlugin 知识库插件
type KnowledgePlugin struct {
	store *Store
}

// NewPlugin 创建知识库插件
func NewPlugin(store *Store) *KnowledgePlugin {
	return &KnowledgePlugin{store: store}
}

func (p *KnowledgePlugin) Name() string                     { return "knowledge" }
func (p *KnowledgePlugin) Description() string              { return "知识库检索与管理" }
func (p *KnowledgePlugin) Shutdown() error                  { return nil }
func (p *KnowledgePlugin) Commands() []plugin.Command       { return nil }
func (p *KnowledgePlugin) EventHandlers() []plugin.EventSub { return nil }

// Init 注册全部知识库工具
func (p *KnowledgePlugin) Init(ctx *plugin.Context) error {
	p.registerAdd(ctx.Registry)
	p.registerSearch(ctx.Registry)
	p.registerList(ctx.Registry)
	p.registerDelete(ctx.Registry)
	return nil
}

// registerAdd 注册 knowledge_add 工具
func (p *KnowledgePlugin) registerAdd(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "knowledge_add",
		Description: "添加文档到知识库",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"id":{"type":"string","description":"文档唯一标识"},
				"title":{"type":"string","description":"文档标题"},
				"content":{"type":"string","description":"文档内容"}
			},
			"required":["id","title","content"]
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				ID      string `json:"id"`
				Title   string `json:"title"`
				Content string `json:"content"`
			}
			json.Unmarshal(raw, &req)
			err := p.store.Add(context.Background(), req.ID, req.Title, req.Content)
			if err != nil {
				return "", err
			}
			return agent.ToJSON(map[string]any{"status": "added", "id": req.ID})
		},
		Active:   true,
		Category: "knowledge",
	})
}

// registerSearch 注册 knowledge_search 工具
func (p *KnowledgePlugin) registerSearch(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "knowledge_search",
		Description: "从知识库中检索相关文档",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"query":{"type":"string","description":"搜索关键词"},
				"top_k":{"type":"integer","description":"返回结果数量，默认5"}
			},
			"required":["query"]
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				Query string `json:"query"`
				TopK  int    `json:"top_k"`
			}
			json.Unmarshal(raw, &req)
			results := p.store.Search(context.Background(), req.Query, req.TopK)
			return agent.ToJSON(map[string]any{"results": results})
		},
		Active:   true,
		Category: "knowledge",
	})
}

// registerList 注册 knowledge_list 工具
func (p *KnowledgePlugin) registerList(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "knowledge_list",
		Description: "列出知识库中的所有文档",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{}
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			docs := p.store.List()
			return agent.ToJSON(map[string]any{"documents": docs})
		},
		Active:   true,
		Category: "knowledge",
	})
}

// registerDelete 注册 knowledge_delete 工具
func (p *KnowledgePlugin) registerDelete(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "knowledge_delete",
		Description: "从知识库中删除文档",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"id":{"type":"string","description":"文档 ID"}
			},
			"required":["id"]
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				ID string `json:"id"`
			}
			json.Unmarshal(raw, &req)
			err := p.store.Delete(context.Background(), req.ID)
			if err != nil {
				return "", err
			}
			return agent.ToJSON(map[string]any{"status": "deleted"})
		},
		Active:   true,
		Category: "knowledge",
	})
}

// GetRetriever 返回检索函数
func (p *KnowledgePlugin) GetRetriever() func(query string) string {
	return func(query string) string {
		results := p.store.Search(context.Background(), query, 3)
		if len(results) == 0 {
			return ""
		}
		var sb strings.Builder
		for _, r := range results {
			sb.WriteString(fmt.Sprintf("--- %s ---\n%s\n\n", r.Title, r.Snippet))
		}
		return sb.String()
	}
}
