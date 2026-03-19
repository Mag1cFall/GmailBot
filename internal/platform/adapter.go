package platform

import "context"

type Adapter interface {
	Name() string
	Start(ctx context.Context, handler MessageHandler) error
	Stop() error
	Send(ctx context.Context, userID string, resp UnifiedResponse) error
}

type MessageHandler func(ctx context.Context, msg UnifiedMessage) (UnifiedResponse, error)
