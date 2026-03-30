// Webhook HTTP 服务
package webhook

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
)

// EventHandler webhook 事件处理函数
type EventHandler func(eventType string, payload map[string]any)

// Server Webhook HTTP 服务
type Server struct {
	addr     string
	secret   string
	mu       sync.RWMutex
	handlers []EventHandler
	listener net.Listener
	server   *http.Server
}

// NewServer 创建 Webhook 服务
func NewServer(addr, secret string) *Server {
	return &Server{
		addr:   addr,
		secret: secret,
	}
}

// OnEvent 注册事件处理器
func (s *Server) OnEvent(handler EventHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers = append(s.handlers, handler)
}

// Start 启动 Webhook 服务，addr 为空則跳过
func (s *Server) Start() error {
	if strings.TrimSpace(s.addr) == "" {
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", s.handleWebhook)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	s.server = &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = ln
	slog.Info("webhook server listening", "addr", s.addr)

	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("webhook server error", "error", err)
		}
	}()
	return nil
}

// Stop 关闭 Webhook 服务
func (s *Server) Stop() {
	if s.server != nil {
		_ = s.server.Close()
	}
}

// handleWebhook 接收 POST 请求，验证 secret 并异步分发事件
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.secret != "" {
		token := r.Header.Get("X-Webhook-Secret")
		if token == "" {
			token = r.URL.Query().Get("secret")
		}
		if token != s.secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	eventType, _ := payload["event"].(string)
	if eventType == "" {
		eventType = "generic"
	}

	s.mu.RLock()
	handlers := make([]EventHandler, len(s.handlers))
	copy(handlers, s.handlers)
	s.mu.RUnlock()

	for _, h := range handlers {
		go h(eventType, payload)
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "received"})
}
