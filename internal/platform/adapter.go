// 平台适配器接口
package platform

import "context"

// Adapter 平台适配器，抽象 Telegram 等平台差异
type Adapter interface {
	Name() string
	Start(ctx context.Context, handler MessageHandler) error
	Stop() error
	Send(ctx context.Context, userID string, resp UnifiedResponse) error
}

// MessageHandler 消息处理函数
type MessageHandler func(ctx context.Context, msg UnifiedMessage) (UnifiedResponse, error)
