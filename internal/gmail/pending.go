// pending.go 邮件发送草稿暂存，等待用户在 Telegram 确认后才真正发出
package gmail

import (
	"context"
	"errors"
	"sync"
)

// DraftType 草稿类型
type DraftType string

const (
	DraftSend    DraftType = "send"
	DraftReply   DraftType = "reply"
	DraftForward DraftType = "forward"
)

// PendingDraft 等待用户确认的邮件草稿
type PendingDraft struct {
	Type      DraftType
	TgUserID  int64
	To        string
	Subject   string
	Body      string
	RefMailID string
}

// PendingStore 线程安全的草稿暂存所
type PendingStore struct {
	mu    sync.Mutex
	store map[int64]*PendingDraft
}

// NewPendingStore 创建草稿暂存所
func NewPendingStore() *PendingStore {
	return &PendingStore{store: make(map[int64]*PendingDraft)}
}

// Set 写入或覆盖一个用户的待确认草稿
func (ps *PendingStore) Set(draft *PendingDraft) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.store[draft.TgUserID] = draft
}

// Get 读取但不移除草稿
func (ps *PendingStore) Get(tgUserID int64) (*PendingDraft, bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	d, ok := ps.store[tgUserID]
	return d, ok
}

// Pop 读取并移除草稿（确认或取消时调用）
func (ps *PendingStore) Pop(tgUserID int64) (*PendingDraft, bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	d, ok := ps.store[tgUserID]
	if ok {
		delete(ps.store, tgUserID)
	}
	return d, ok
}

// Confirm 取出草稿并调用 Service 真正发送
func (ps *PendingStore) Confirm(ctx context.Context, svc *Service, tgUserID int64) (string, error) {
	draft, ok := ps.Pop(tgUserID)
	if !ok {
		return "", errors.New("没有待确认的草稿")
	}
	switch draft.Type {
	case DraftSend:
		id, err := svc.SendEmail(ctx, tgUserID, draft.To, draft.Subject, draft.Body)
		if err != nil {
			return "", err
		}
		return id, nil
	case DraftReply:
		id, err := svc.ReplyEmail(ctx, tgUserID, draft.RefMailID, draft.Body)
		if err != nil {
			return "", err
		}
		return id, nil
	case DraftForward:
		id, err := svc.ForwardEmail(ctx, tgUserID, draft.RefMailID, draft.To)
		if err != nil {
			return "", err
		}
		return id, nil
	default:
		return "", errors.New("未知草稿类型")
	}
}
