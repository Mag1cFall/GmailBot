// Dashboard HTTP API
package dashboard

import (
	"context"
	"embed"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"gmailbot/config"
	"gmailbot/internal/agent"
	"gmailbot/internal/logging"
	"gmailbot/internal/metrics"
	"gmailbot/internal/plugin"
	"gmailbot/internal/store"
)

//go:embed static/*
var staticFS embed.FS

// Server Dashboard HTTP 服务
type Server struct {
	addr            string
	auth            string
	pluginMgr       *plugin.Manager
	registry        *agent.ToolRegistry
	store           *store.Store
	providerManager *agent.ProviderManager
	metrics         *metrics.Metrics
	httpServer      *http.Server
	onConfigUpdate  func(config.Config)
}

// NewServer 创建 Dashboard 服务并注册路由
func NewServer(addr, auth string, pluginMgr *plugin.Manager, registry *agent.ToolRegistry, st *store.Store, providerManager *agent.ProviderManager, metricSet *metrics.Metrics, onConfigUpdate func(config.Config)) *Server {
	mux := http.NewServeMux()
	server := &Server{
		addr:            addr,
		auth:            auth,
		pluginMgr:       pluginMgr,
		registry:        registry,
		store:           st,
		providerManager: providerManager,
		metrics:         metricSet,
		onConfigUpdate:  onConfigUpdate,
	}
	mux.HandleFunc("/", server.handleIndex)
	mux.HandleFunc("/app.js", server.handleAppJS)
	mux.HandleFunc("/style.css", server.handleCSS)
	mux.HandleFunc("/api/status", server.wrap(server.handleStatus))
	mux.HandleFunc("/api/plugins", server.wrap(server.handlePlugins))
	mux.HandleFunc("/api/plugins/", server.wrap(server.handlePluginToggle))
	mux.HandleFunc("/api/tools", server.wrap(server.handleTools))
	mux.HandleFunc("/api/tools/", server.wrap(server.handleToolToggle))
	mux.HandleFunc("/api/sessions", server.wrap(server.handleSessionsOverview))
	mux.HandleFunc("/api/sessions/", server.wrap(server.handleSessionsDispatch))
	mux.HandleFunc("/api/context_stats/", server.wrap(server.handleContextStats))
	mux.HandleFunc("/api/config", server.wrap(server.handleConfig))
	mux.HandleFunc("/api/config/", server.wrap(server.handleConfigUpdate))
	mux.HandleFunc("/api/providers", server.wrap(server.handleProviders))
	mux.HandleFunc("/api/logs", server.wrap(server.handleLogs))
	mux.HandleFunc("/api/metrics", server.wrap(server.handleMetrics))
	server.httpServer = &http.Server{Addr: addr, Handler: mux}
	return server
}

func (s *Server) handleSessionsOverview(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.store.ListRecentSessions(r.Context(), 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type sessionView struct {
		ID            string `json:"id"`
		Platform      string `json:"platform"`
		UserID        string `json:"user_id"`
		Title         string `json:"title"`
		PersonaName   string `json:"persona_name"`
		MessageCount  int    `json:"message_count"`
		TokenEstimate int    `json:"token_estimate"`
		LastActive    string `json:"last_active"`
		IsActive      bool   `json:"is_active"`
	}
	views := make([]sessionView, 0, len(sessions))
	for _, item := range sessions {
		views = append(views, sessionView{
			ID:            item.ID,
			Platform:      item.Platform,
			UserID:        item.UserID,
			Title:         item.Title,
			PersonaName:   item.PersonaName,
			MessageCount:  item.MessageCount,
			TokenEstimate: agent.EstimateSessionMessagesTokens(item.Messages),
			LastActive:    item.LastActive.Format(time.RFC3339),
			IsActive:      item.IsActive,
		})
	}
	writeJSON(w, http.StatusOK, views)
}

// Start 启动 HTTP 服务，addr 为空时跳过
func (s *Server) Start() error {
	if strings.TrimSpace(s.addr) == "" {
		return nil
	}
	go s.httpServer.ListenAndServe()
	return nil
}

// Stop 优雅关闭 HTTP 服务
func (s *Server) Stop(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

// wrap 包装 handler，添加 Auth 校验
func (s *Server) wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		p := r.URL.Path
		if r.Method != http.MethodGet || (p != "/api/logs" && p != "/api/status" && p != "/api/metrics") {
			slog.Info("dashboard api", "method", r.Method, "path", p)
		}
		next(w, r)
	}
}

// authorized 校验请求是否携带有效 token，支持 Header 和 Bearer
func (s *Server) authorized(r *http.Request) bool {
	if strings.TrimSpace(s.auth) == "" {
		return true
	}
	if r.Header.Get("X-Dashboard-Auth") == s.auth {
		return true
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	return strings.TrimPrefix(authorization, "Bearer ") == s.auth
}

// handleIndex 返回前端 index.html
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data, _ := staticFS.ReadFile("static/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// handleAppJS 返回前端 JS
func (s *Server) handleAppJS(w http.ResponseWriter, r *http.Request) {
	data, _ := staticFS.ReadFile("static/app.js")
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write(data)
}

// handleCSS 返回前端 CSS
func (s *Server) handleCSS(w http.ResponseWriter, r *http.Request) {
	data, _ := staticFS.ReadFile("static/style.css")
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(data)
}

// handleStatus 返回运行指标快照
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.metrics.Snapshot())
}

// handlePlugins 列出所有插件信息
func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.pluginMgr.Info())
}

// handlePluginToggle 启用或禁用指定插件
func (s *Server) handlePluginToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/plugins/"), "/toggle")
	var payload struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.NewDecoder(r.Body).Decode(&payload)
	if !s.pluginMgr.SetActive(name, payload.Enabled) {
		http.Error(w, "plugin not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "enabled": payload.Enabled})
}

// handleTools 列出所有工具及启用状态
func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	type toolView struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Category    string `json:"category"`
		Active      bool   `json:"active"`
	}
	tools := s.registry.AllTools()
	views := make([]toolView, 0, len(tools))
	for _, tool := range tools {
		views = append(views, toolView{Name: tool.Name, Description: tool.Description, Category: tool.Category, Active: tool.Active})
	}
	writeJSON(w, http.StatusOK, views)
}

// handleToolToggle 启用或禁用指定工具
func (s *Server) handleToolToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/tools/"), "/toggle")
	var payload struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.NewDecoder(r.Body).Decode(&payload)
	s.registry.SetActive(name, payload.Enabled)
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "enabled": payload.Enabled})
}

// handleSessions 列出指定用户的会话列表
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	uid := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	platformName := strings.TrimSpace(r.URL.Query().Get("platform"))
	if platformName == "" {
		platformName = "telegram"
	}
	userKey, err := s.store.ResolvePlatformUserKey(r.Context(), platformName, uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sessions, err := s.store.ListSessions(r.Context(), userKey, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

// handleSessionsDispatch 根据 HTTP 方法分发到查询或删除
func (s *Server) handleSessionsDispatch(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		s.handleDeleteSession(w, r)
		return
	}
	s.handleSessions(w, r)
}

// handleDeleteSession 清空指定用户的当前活跃会话消息
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	uid := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	platformName := strings.TrimSpace(r.URL.Query().Get("platform"))
	if platformName == "" {
		platformName = "telegram"
	}
	session, err := s.store.GetOrCreateActiveSessionByIdentity(r.Context(), platformName, uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.UpdateSessionMessages(r.Context(), session.ID, nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cleared": true, "session_id": session.ID})
}

func (s *Server) handleContextStats(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/context_stats/"))
	if userID == "" {
		http.Error(w, "user id is required", http.StatusBadRequest)
		return
	}
	platformName := strings.TrimSpace(r.URL.Query().Get("platform"))
	if platformName == "" {
		platformName = "telegram"
	}
	session, err := s.store.GetOrCreateActiveSessionByIdentity(r.Context(), platformName, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ctxMgr := agent.NewContextManager(r.Context(), config.Load(), s.providerManager)
	writeJSON(w, http.StatusOK, map[string]any{
		"platform":       platformName,
		"user_id":        userID,
		"session_id":     session.ID,
		"message_count":  len(session.Messages),
		"token_estimate": agent.EstimateSessionMessagesTokens(session.Messages),
		"warn_tokens":    ctxMgr.WarnTokens(),
		"max_tokens":     ctxMgr.MaxTokens(),
	})
}

// handleConfig 返回所有可编辑配置项（敏感值打码）
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	values := map[string]string{}
	for _, key := range config.EditableKeys {
		values[key] = maskConfigValue(key, getEnv(key))
	}
	writeJSON(w, http.StatusOK, values)
}

// handleConfigUpdate 更新单个配置项并触发热重载
func (s *Server) handleConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/api/config/")
	if !allowedConfigKey(key) {
		http.Error(w, "config key not editable", http.StatusForbidden)
		return
	}
	var payload struct {
		Value string `json:"value"`
	}
	data, _ := io.ReadAll(r.Body)
	if len(data) > 0 {
		_ = json.Unmarshal(data, &payload)
	}
	if err := config.UpdateEnvFile(key, payload.Value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.onConfigUpdate != nil {
		s.onConfigUpdate(config.Load())
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": payload.Value})
}

// handleProviders 列出所有 AI 服务商信息
func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.providerManager.Providers())
}

// handleLogs 返回最近的日志缓冲
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, logging.BufferEntries())
}

// handleMetrics 返回运行指标
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.metrics.Snapshot())
}

// writeJSON 写入 JSON 响应
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// getEnv 读取环境变量并去除空白
func getEnv(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

// allowedConfigKey 检查 key 是否在允许编辑的配置列表中
func allowedConfigKey(key string) bool {
	for _, item := range config.EditableKeys {
		if item == key {
			return true
		}
	}
	return false
}

// maskConfigValue 对敏感配置值打码，只保留首尾各 4 位
func maskConfigValue(key, value string) string {
	key = strings.ToUpper(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, token := range []string{"KEY", "TOKEN", "SECRET", "AUTH", "PASSWORD"} {
		if strings.Contains(key, token) {
			if len(value) <= 8 {
				return "********"
			}
			return value[:4] + strings.Repeat("*", len(value)-8) + value[len(value)-4:]
		}
	}
	return value
}
