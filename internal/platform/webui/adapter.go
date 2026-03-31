package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	baseplatform "gmailbot/internal/platform"
	"gmailbot/internal/store"
)

type requestPayload struct {
	UserID string `json:"user_id"`
	Text   string `json:"text"`
}

type responsePayload struct {
	Response baseplatform.UnifiedResponse `json:"response"`
	Error    string                       `json:"error,omitempty"`
}

type inMessage struct {
	msg       baseplatform.UnifiedMessage
	replyChan chan responsePayload
}

type Adapter struct {
	addr      string
	store     *store.Store
	authToken string
	server    *http.Server
	handler   baseplatform.MessageHandler
	msgChan   chan inMessage
	streams   sync.Map
	stopOnce  sync.Once
	cancel    context.CancelFunc
}

func NewAdapter(addr string, st *store.Store, authToken string) *Adapter {
	return &Adapter{
		addr:      strings.TrimSpace(addr),
		store:     st,
		authToken: strings.TrimSpace(authToken),
		msgChan:   make(chan inMessage, 32),
	}
}

func (a *Adapter) Name() string { return "webui" }

func (a *Adapter) Start(ctx context.Context, handler baseplatform.MessageHandler) error {
	if strings.TrimSpace(a.addr) == "" {
		return nil
	}
	a.handler = handler
	runCtx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	go func() {
		<-ctx.Done()
		_ = a.Stop()
	}()
	go a.runMessageLoop(runCtx)
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	mux.HandleFunc("/api/chat", a.requireAuth(a.handleChat))
	mux.HandleFunc("/api/stream/", a.requireAuth(a.handleStream))
	mux.HandleFunc("/api/history/", a.requireAuth(a.handleHistory))
	mux.HandleFunc("/api/session/", a.requireAuth(a.handleSession))
	a.server = &http.Server{Addr: a.addr, Handler: mux}
	err := a.server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (a *Adapter) Stop() error {
	var stopErr error
	a.stopOnce.Do(func() {
		if a.cancel != nil {
			a.cancel()
		}
		if a.server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stopErr = a.server.Shutdown(ctx)
		}
	})
	return stopErr
}

func (a *Adapter) Send(ctx context.Context, userID string, resp baseplatform.UnifiedResponse) error {
	if strings.TrimSpace(userID) == "" {
		return errors.New("user id is required")
	}
	a.pushStream(strings.TrimSpace(userID), resp)
	return nil
}

func (a *Adapter) runMessageLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-a.msgChan:
			resp, err := a.handler(ctx, item.msg)
			payload := responsePayload{Response: resp}
			if err != nil {
				payload.Error = err.Error()
			} else {
				a.pushStream(item.msg.UserID, resp)
			}
			item.replyChan <- payload
		}
	}
}

func (a *Adapter) pushStream(userID string, resp baseplatform.UnifiedResponse) {
	value, ok := a.streams.Load(strings.TrimSpace(userID))
	if !ok {
		return
	}
	ch, ok := value.(chan baseplatform.UnifiedResponse)
	if !ok {
		return
	}
	select {
	case ch <- resp:
	default:
	}
}

func (a *Adapter) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.authToken != "" {
			token := strings.TrimSpace(r.Header.Get("Authorization"))
			if token == "" {
				token = strings.TrimSpace(r.URL.Query().Get("token"))
			}
			token = strings.TrimPrefix(token, "Bearer ")
			token = strings.TrimSpace(token)
			if token != a.authToken {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

func (a *Adapter) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(webUIPage))
}

func (a *Adapter) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload requestPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	payload.UserID = strings.TrimSpace(payload.UserID)
	payload.Text = strings.TrimSpace(payload.Text)
	if payload.UserID == "" || payload.Text == "" {
		http.Error(w, "user_id and text are required", http.StatusBadRequest)
		return
	}
	slog.Info("webui chat", "user", payload.UserID, "text_len", len(payload.Text))
	replyChan := make(chan responsePayload, 1)
	a.msgChan <- inMessage{
		msg: baseplatform.UnifiedMessage{
			Platform:  a.Name(),
			UserID:    payload.UserID,
			SessionID: "active",
			Text:      payload.Text,
		},
		replyChan: replyChan,
	}
	result := <-replyChan
	if result.Error != "" {
		writeJSON(w, http.StatusInternalServerError, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *Adapter) handleStream(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/stream/"))
	if userID == "" {
		http.Error(w, "user id is required", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}
	slog.Info("webui stream connected", "user", userID)
	ch := make(chan baseplatform.UnifiedResponse, 16)
	a.streams.Store(userID, ch)
	defer func() {
		a.streams.Delete(userID)
		slog.Info("webui stream disconnected", "user", userID)
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case resp := <-ch:
			data, _ := json.Marshal(resp)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-ticker.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func (a *Adapter) handleHistory(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/history/"))
	if userID == "" {
		http.Error(w, "user id is required", http.StatusBadRequest)
		return
	}
	session, err := a.store.GetOrCreateActiveSessionByIdentity(r.Context(), a.Name(), userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, session.Messages)
}

func (a *Adapter) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/session/"))
	if userID == "" {
		http.Error(w, "user id is required", http.StatusBadRequest)
		return
	}
	if err := a.store.ClearActiveSessionMessagesByIdentity(r.Context(), a.Name(), userID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

const webUIPage = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>GmailBot - Chat</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500&family=DM+Sans:wght@400;500;600&display=swap" rel="stylesheet">
<style>
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
:root{--bg:#0d1117;--surface:#161b22;--border:rgba(255,255,255,.08);--text:#e6edf3;--muted:#8b949e;--accent:#38bdf8;--green:#3fb950;--red:#f85149;--mono:'JetBrains Mono',monospace;--sans:'DM Sans',system-ui,sans-serif}
body{font-family:var(--sans);background:var(--bg);color:var(--text);height:100vh;display:flex;overflow:hidden}
.sidebar{width:240px;background:var(--surface);border-right:1px solid var(--border);display:flex;flex-direction:column;padding:20px 16px;flex-shrink:0}
.brand-label{font-size:10px;font-weight:600;letter-spacing:.1em;color:var(--accent);margin-bottom:6px}
.brand-name{font-size:20px;font-weight:600;margin-bottom:4px}
.brand-desc{font-size:12px;color:var(--muted);line-height:1.5;margin-bottom:20px}
.field-label{font-size:11px;color:var(--muted);margin-bottom:6px;font-weight:500}
.uid-row{display:flex;gap:6px;margin-bottom:10px}
.uid-input{flex:1;min-width:0;background:var(--bg);border:1px solid var(--border);color:var(--text);padding:8px 10px;border-radius:6px;font-size:13px;outline:none}
.uid-input:focus{border-color:var(--accent)}
.connect-btn{padding:8px 14px;border-radius:6px;font-size:13px;cursor:pointer;border:none;background:var(--accent);color:#0d1117;font-weight:600}
.connect-btn:hover{filter:brightness(1.1)}
.clear-btn{width:100%;padding:8px;background:rgba(255,255,255,.05);border:1px solid var(--border);color:var(--muted);border-radius:6px;font-size:13px;cursor:pointer;margin-top:6px}
.clear-btn:hover{color:var(--text);background:rgba(255,255,255,.08)}
.status-pill{display:inline-flex;align-items:center;gap:6px;padding:5px 10px;border-radius:4px;font-size:11px;margin-top:16px;background:rgba(255,255,255,.04);border:1px solid var(--border)}
.dot{width:6px;height:6px;border-radius:50%;background:var(--muted);flex-shrink:0}
.dot.on{background:var(--green);box-shadow:0 0 5px rgba(63,185,80,.5)}
.dot.err{background:var(--red)}
.hint{margin-top:auto;padding-top:16px;border-top:1px solid var(--border)}
.hint-item{font-size:11px;color:var(--muted);line-height:1.7}
.main{flex:1;display:flex;flex-direction:column;overflow:hidden;min-width:0}
.chat-header{height:48px;border-bottom:1px solid var(--border);display:flex;align-items:center;padding:0 20px;flex-shrink:0}
.chat-title{font-size:13px;color:var(--muted);font-weight:500}
.messages{flex:1;overflow-y:auto;padding:20px;display:flex;flex-direction:column;gap:14px}
.messages::-webkit-scrollbar{width:5px}
.messages::-webkit-scrollbar-thumb{background:rgba(255,255,255,.1);border-radius:3px}
.bubble-row{display:flex;flex-direction:column;gap:3px;max-width:72%}
.bubble-row.user{align-self:flex-end;align-items:flex-end}
.bubble-row.assistant{align-self:flex-start;align-items:flex-start}
.bubble-role{font-size:10px;font-weight:600;letter-spacing:.08em;color:var(--muted);padding:0 2px}
.bubble{padding:10px 14px;border-radius:10px;font-size:13.5px;line-height:1.65;word-break:break-word;white-space:pre-wrap}
.bubble.user{background:rgba(56,189,248,.12);border:1px solid rgba(56,189,248,.2)}
.bubble.assistant{background:var(--surface);border:1px solid var(--border)}
.bubble.empty{color:var(--muted);font-style:italic}
.thinking{display:flex;gap:5px;align-items:center;padding:10px 14px;background:var(--surface);border:1px solid var(--border);border-radius:10px}
.thinking span{width:5px;height:5px;border-radius:50%;background:var(--muted);animation:blink 1.2s infinite}
.thinking span:nth-child(2){animation-delay:.2s}
.thinking span:nth-child(3){animation-delay:.4s}
@keyframes blink{0%,100%{opacity:.25}50%{opacity:1}}
.input-area{border-top:1px solid var(--border);padding:14px 20px;display:flex;gap:10px;align-items:flex-end;flex-shrink:0}
.input-box{flex:1;background:var(--surface);border:1px solid var(--border);color:var(--text);padding:10px 14px;border-radius:8px;font-size:13.5px;resize:none;outline:none;min-height:44px;max-height:160px;line-height:1.5;transition:border-color .15s}
.input-box:focus{border-color:var(--accent)}
.send-btn{padding:10px 20px;background:var(--accent);color:#0d1117;border:none;border-radius:8px;font-size:13px;font-weight:600;cursor:pointer;flex-shrink:0}
.send-btn:hover{filter:brightness(1.1)}
.send-btn:disabled{opacity:.4;cursor:not-allowed}
</style>
</head>
<body>
<div class="sidebar">
  <div class="brand-label">WEBUI</div>
  <div class="brand-name">GmailBot</div>
  <p class="brand-desc">基于 Agent 会话的 Web 聊天入口</p>
  <div class="field-label">用户 ID</div>
  <div class="uid-row">
    <input id="uid" class="uid-input" placeholder="demo-user">
    <button id="connect" class="connect-btn">连接</button>
  </div>
  <div class="field-label">访问密钥</div>
  <input id="tkn" class="uid-input" type="password" placeholder=".env 中 DASHBOARD_AUTH 的值" style="width:100%;margin-bottom:10px;flex:none">
  <button id="clear" class="clear-btn">清空当前会话</button>
  <div class="status-pill"><span class="dot" id="dot"></span><span id="status">未连接</span></div>
  <div class="hint">
    <div class="hint-item">· 密钥 = .env 中的 DASHBOARD_AUTH</div>
    <div class="hint-item">· 刷新页面重新拉取历史</div>
    <div class="hint-item">· 回复通过 SSE 实时推送</div>
    <div class="hint-item">· Enter 发送，Shift+Enter 换行</div>
  </div>
</div>
<div class="main">
  <div class="chat-header"><span class="chat-title" id="chat-title">请输入用户 ID 并点击连接</span></div>
  <div class="messages" id="messages"></div>
  <div class="input-area">
    <textarea id="input" class="input-box" rows="1" placeholder="输入消息…"></textarea>
    <button id="send" class="send-btn" disabled>发送</button>
  </div>
</div>
<script>
var uid=document.getElementById('uid'),tkn=document.getElementById('tkn'),dot=document.getElementById('dot'),
    sts=document.getElementById('status'),msgs=document.getElementById('messages'),
    inp=document.getElementById('input'),btn=document.getElementById('send'),
    ttl=document.getElementById('chat-title'),es=null,busy=false;
uid.value=localStorage.getItem('webui-uid')||'demo-user';
tkn.value=localStorage.getItem('webui-tkn')||'';
function getToken(){var t=tkn.value.trim();localStorage.setItem('webui-tkn',t);return t;}
function authHeaders(){var h={'Content-Type':'application/json'};var t=getToken();if(t)h['Authorization']='Bearer '+t;return h;}
function authFetch(url,opts){opts=opts||{};opts.headers=Object.assign(authHeaders(),opts.headers||{});return fetch(url,opts);}

function dot2(s,m){dot.className='dot'+(s==='on'?' on':s==='err'?' err':'');sts.textContent=m;}

function toText(v){
  if(!v&&v!==0)return'';
  if(typeof v==='string')return v;
  if(Array.isArray(v))return v.map(function(p){return p&&(p.text||JSON.stringify(p))||'';}).join('');
  return JSON.stringify(v);
}

function bubble(role,raw){
  var txt=toText(raw);
  var row=document.createElement('div');row.className='bubble-row '+role;
  var lbl=document.createElement('div');lbl.className='bubble-role';
  lbl.textContent=role==='user'?'USER':'ASSISTANT';
  var b=document.createElement('div');b.className='bubble '+role+(txt?'':' empty');
  b.textContent=txt||'（空）';
  row.appendChild(lbl);row.appendChild(b);msgs.appendChild(row);
  msgs.scrollTop=msgs.scrollHeight;
}

var thk=null;
function showThink(){thk=document.createElement('div');thk.className='bubble-row assistant';
  thk.innerHTML='<div class="bubble-role">ASSISTANT</div><div class="thinking"><span></span><span></span><span></span></div>';
  msgs.appendChild(thk);msgs.scrollTop=msgs.scrollHeight;}
function hideThink(){if(thk){thk.remove();thk=null;}}

function loadHistory(){
  var u=uid.value.trim();if(!u)return;
  authFetch('/api/history/'+encodeURIComponent(u)).then(function(r){return r.json();})
  .then(function(items){
    msgs.innerHTML='';
    (Array.isArray(items)?items:[]).filter(function(m){return m.role==='user'||m.role==='assistant';})
    .forEach(function(m){bubble(m.role,m.content);});
  }).catch(function(){});
}

function conn(){
  var u=uid.value.trim();if(!u){dot2('err','请填写用户 ID');return;}
  localStorage.setItem('webui-uid',u);ttl.textContent='会话 · '+u;btn.disabled=false;
  if(es)es.close();dot2('','连接中…');
  var streamUrl='/api/stream/'+encodeURIComponent(u);
  var t=getToken();if(t)streamUrl+='?token='+encodeURIComponent(t);
  es=new EventSource(streamUrl);
  es.onopen=function(){dot2('on','已连接');};
  es.onerror=function(){dot2('err','连接中断');};
  es.onmessage=function(e){
    try{var p=JSON.parse(e.data);hideThink();bubble('assistant',p.text||p.Text||'');dot2('on','已收到回复');}catch(ex){}
    busy=false;btn.disabled=false;
  };
  loadHistory();
}

function send(){
  var u=uid.value.trim(),t=inp.value.trim();if(!u||!t||busy)return;
  busy=true;btn.disabled=true;bubble('user',t);inp.value='';inp.style.height='';showThink();dot2('on','发送中…');
  authFetch('/api/chat',{method:'POST',body:JSON.stringify({user_id:u,text:t})})
  .then(function(r){return r.json().then(function(p){if(!r.ok){hideThink();dot2('err',p.error||'失败');busy=false;btn.disabled=false;}});})
  .catch(function(e){hideThink();dot2('err',e.message);busy=false;btn.disabled=false;});
}

document.getElementById('connect').addEventListener('click',conn);
document.getElementById('send').addEventListener('click',send);
document.getElementById('clear').addEventListener('click',function(){
  var u=uid.value.trim();if(!u)return;
  authFetch('/api/session/'+encodeURIComponent(u),{method:'DELETE'}).then(function(){msgs.innerHTML='';dot2('on','已清空');});
});
inp.addEventListener('keydown',function(e){if(e.key==='Enter'&&!e.shiftKey){e.preventDefault();send();}});
inp.addEventListener('input',function(){inp.style.height='auto';inp.style.height=Math.min(inp.scrollHeight,160)+'px';});
if(tkn.value.trim())conn();
</script>
</body>
</html>`
