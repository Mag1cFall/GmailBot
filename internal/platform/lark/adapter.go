package lark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	baseplatform "gmailbot/internal/platform"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// replyTarget 飞书消息回复目标，区分群聊（chat_id）和私聊（open_id）
type replyTarget struct {
	receiveID     string
	receiveIDType string
}

// Adapter 飞书平台适配器，通过长连接（WebSocket）接收和发送消息
type Adapter struct {
	appID     string
	appSecret string
	botName   string
	wsClient  *larkws.Client
	apiClient *lark.Client
	handler   baseplatform.MessageHandler
	seen      sync.Map
	targets   sync.Map
	stopOnce  sync.Once
	cancel    context.CancelFunc
}

// NewAdapter 创建飞书适配器
func NewAdapter(appID, appSecret, botName string) *Adapter {
	return &Adapter{
		appID:     strings.TrimSpace(appID),
		appSecret: strings.TrimSpace(appSecret),
		botName:   strings.TrimSpace(botName),
	}
}

// Name 返回适配器名称
func (a *Adapter) Name() string {
	return "lark"
}

// Start 建立飞书长连接并开始接收消息
func (a *Adapter) Start(ctx context.Context, handler baseplatform.MessageHandler) error {
	if a.appID == "" || a.appSecret == "" {
		return nil
	}
	a.handler = handler
	runCtx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	dispatcher := dispatcher.NewEventDispatcher("", "").OnP2MessageReceiveV1(a.handleEvent)
	a.wsClient = larkws.NewClient(
		a.appID,
		a.appSecret,
		larkws.WithEventHandler(dispatcher),
		larkws.WithLogLevel(larkcore.LogLevelError),
	)
	a.apiClient = lark.NewClient(
		a.appID,
		a.appSecret,
		lark.WithLogLevel(larkcore.LogLevelError),
	)
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.wsClient.Start(runCtx)
	}()
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

// Stop 关闭长连接
func (a *Adapter) Stop() error {
	a.stopOnce.Do(func() {
		if a.cancel != nil {
			a.cancel()
		}
	})
	return nil
}

// Send 向指定用户发送文本消息
func (a *Adapter) Send(ctx context.Context, userID string, resp baseplatform.UnifiedResponse) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return errors.New("user id is required")
	}
	value, ok := a.targets.Load(userID)
	if !ok {
		return fmt.Errorf("no reply target for lark user %s", userID)
	}
	target := value.(replyTarget)
	content := fmt.Sprintf(`{"text":%q}`, strings.TrimSpace(resp.Text))
	receiveID := target.receiveID
	receiveIDType := target.receiveIDType
	msgType := larkim.MsgTypeText
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(receiveIDType).
		Body(&larkim.CreateMessageReqBody{
			ReceiveId: &receiveID,
			MsgType:   &msgType,
			Content:   &content,
		}).
		Build()
	result, err := a.apiClient.Im.V1.Message.Create(ctx, req)
	if err != nil {
		return err
	}
	if !result.Success() {
		return result.CodeError
	}
	return nil
}

func (a *Adapter) handleEvent(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	if event == nil || event.EventV2Base == nil || event.EventV2Base.Header == nil || event.Event == nil || event.Event.Message == nil || event.Event.Sender == nil || event.Event.Sender.SenderId == nil {
		return nil
	}
	if a.isDuplicateEvent(event.EventV2Base.Header.EventID) {
		return nil
	}
	message := event.Event.Message
	userID := firstNonEmptyPtr(event.Event.Sender.SenderId.OpenId)
	if userID == "" {
		return nil
	}
	chatType := firstNonEmptyPtr(message.ChatType)
	if chatType == "group" && a.botName != "" && !mentionsBot(message.Mentions, a.botName) {
		return nil
	}
	receiveIDType := larkim.ReceiveIdTypeOpenId
	receiveID := userID
	if chatType == "group" {
		receiveIDType = "chat_id"
		receiveID = firstNonEmptyPtr(message.ChatId)
	}
	a.targets.Store(userID, replyTarget{receiveID: receiveID, receiveIDType: receiveIDType})
	text := extractLarkText(message.Content, message.Mentions, a.botName)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	resp, err := a.handler(ctx, baseplatform.UnifiedMessage{
		Platform:  a.Name(),
		UserID:    userID,
		SessionID: "active",
		Text:      text,
	})
	if err != nil {
		return err
	}
	if strings.TrimSpace(resp.Text) == "" && len(resp.Attachments) == 0 {
		return nil
	}
	return a.Send(ctx, userID, resp)
}

func (a *Adapter) isDuplicateEvent(eventID string) bool {
	a.cleanupSeenEvents()
	if _, ok := a.seen.Load(eventID); ok {
		return true
	}
	a.seen.Store(eventID, time.Now())
	return false
}

func (a *Adapter) cleanupSeenEvents() {
	cutoff := time.Now().Add(-30 * time.Minute)
	a.seen.Range(func(key, value any) bool {
		timestamp, ok := value.(time.Time)
		if ok && timestamp.Before(cutoff) {
			a.seen.Delete(key)
		}
		return true
	})
}

func extractLarkText(raw *string, mentions []*larkim.MentionEvent, botName string) string {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(*raw), &payload); err != nil {
		return strings.TrimSpace(*raw)
	}
	text, _ := payload["text"].(string)
	text = strings.TrimSpace(text)
	for _, mention := range mentions {
		key := firstNonEmptyPtr(mention.Key)
		name := firstNonEmptyPtr(mention.Name)
		if key == "" {
			continue
		}
		replacement := ""
		if name != "" {
			replacement = "@" + name
		}
		text = strings.ReplaceAll(text, key, replacement)
		if botName != "" && name == botName {
			text = strings.ReplaceAll(text, replacement, "")
		}
	}
	text = strings.TrimSpace(text)
	return strings.TrimSpace(text)
}

func mentionsBot(mentions []*larkim.MentionEvent, botName string) bool {
	for _, mention := range mentions {
		if firstNonEmptyPtr(mention.Name) == botName {
			return true
		}
	}
	return false
}

func firstNonEmptyPtr(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
