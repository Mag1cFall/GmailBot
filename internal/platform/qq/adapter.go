package qq

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	baseplatform "gmailbot/internal/platform"

	"github.com/tencent-connect/botgo"
	qqdto "github.com/tencent-connect/botgo/dto"
	qqevent "github.com/tencent-connect/botgo/event"
	qqopenapi "github.com/tencent-connect/botgo/openapi"
	qqtoken "github.com/tencent-connect/botgo/token"
)

var mentionPattern = regexp.MustCompile(`<@!?[^>]+>`)

// replyTarget QQ 消息回御目标，区分群聊和 C2C
type replyTarget struct {
	scene  string
	target string
	msgID  string
}

// Adapter QQ 官方机器人适配器，支持群聊 @ 和 C2C 私聊
type Adapter struct {
	appID       string
	secret      string
	enableGroup bool
	api         qqopenapi.OpenAPI
	handler     baseplatform.MessageHandler
	targets     sync.Map
	stopOnce    sync.Once
}

// NewAdapter 创建 QQ 适配器
func NewAdapter(appID, secret string, enableGroup bool) *Adapter {
	return &Adapter{
		appID:       strings.TrimSpace(appID),
		secret:      strings.TrimSpace(secret),
		enableGroup: enableGroup,
	}
}

// Name 返回适配器名称
func (a *Adapter) Name() string {
	return "qq"
}

// Start 建立 QQ 官方 WebSocket 连接并开始接收消息
func (a *Adapter) Start(ctx context.Context, handler baseplatform.MessageHandler) error {
	if a.appID == "" || a.secret == "" {
		return nil
	}
	a.handler = handler
	if err := botgo.SelectOpenAPIVersion(qqopenapi.APIv1); err != nil {
		return err
	}
	tokenSource := qqtoken.NewQQBotTokenSource(&qqtoken.QQBotCredentials{AppID: a.appID, AppSecret: a.secret})
	a.api = botgo.NewOpenAPI(a.appID, tokenSource)
	intents := make([]interface{}, 0, 2)
	if a.enableGroup {
		intents = append(intents, qqevent.GroupATMessageEventHandler(a.handleGroupMessage))
	}
	intents = append(intents, qqevent.C2CMessageEventHandler(a.handleC2CMessage))
	registered := qqevent.RegisterHandlers(intents...)
	wsInfo, err := a.api.WS(ctx, nil, "")
	if err != nil {
		return err
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- botgo.NewSessionManager().Start(wsInfo, tokenSource, &registered)
	}()
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

// Stop 退出连接
func (a *Adapter) Stop() error {
	a.stopOnce.Do(func() {})
	return nil
}

// Send 对群聊或 C2C 发送文本回复
func (a *Adapter) Send(ctx context.Context, userID string, resp baseplatform.UnifiedResponse) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return errors.New("user id is required")
	}
	value, ok := a.targets.Load(userID)
	if !ok {
		return fmt.Errorf("no reply target for qq user %s", userID)
	}
	target := value.(replyTarget)
	message := &qqdto.MessageToCreate{Content: strings.TrimSpace(resp.Text), MsgType: qqdto.TextMsg, MsgID: target.msgID}
	var err error
	switch target.scene {
	case "group":
		_, err = a.api.PostGroupMessage(ctx, target.target, message)
	case "c2c":
		_, err = a.api.PostC2CMessage(ctx, target.target, message)
	default:
		err = fmt.Errorf("unsupported qq scene %s", target.scene)
	}
	return err
}

func (a *Adapter) handleGroupMessage(event *qqdto.WSPayload, data *qqdto.WSGroupATMessageData) error {
	return a.handleMessage(context.Background(), (*qqdto.Message)(data), "group")
}

func (a *Adapter) handleC2CMessage(event *qqdto.WSPayload, data *qqdto.WSC2CMessageData) error {
	return a.handleMessage(context.Background(), (*qqdto.Message)(data), "c2c")
}

func (a *Adapter) handleMessage(ctx context.Context, message *qqdto.Message, scene string) error {
	if message == nil || message.Author == nil {
		return nil
	}
	userID := strings.TrimSpace(message.Author.ID)
	if userID == "" {
		return nil
	}
	targetID := userID
	if scene == "group" {
		targetID = strings.TrimSpace(message.GroupID)
		if targetID == "" {
			return nil
		}
	}
	a.targets.Store(userID, replyTarget{scene: scene, target: targetID, msgID: strings.TrimSpace(message.ID)})
	text := strings.TrimSpace(mentionPattern.ReplaceAllString(message.Content, ""))
	if text == "" {
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
