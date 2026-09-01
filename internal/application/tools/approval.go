package tools

import (
	"context"
	"strings"
	"sync"
	"time"
)

// WriteApprovalStore keeps the short-lived, one-shot QQ confirmation needed
// before a Codex turn may mutate the configured workspace.
type WriteApprovalStore struct {
	mu    sync.Mutex
	ttl   time.Duration
	items map[approvalKey]*writeApproval
}

type approvalKey struct {
	groupID int64
	userID  int64
}

type writeApproval struct {
	task      string
	confirmed bool
	expiresAt time.Time
}

func NewWriteApprovalStore(ttl time.Duration) *WriteApprovalStore {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &WriteApprovalStore{ttl: ttl, items: make(map[approvalKey]*writeApproval)}
}

func (s *WriteApprovalStore) Request(groupID, userID int64, task string, now time.Time) string {
	if s == nil {
		return ""
	}
	task = strings.TrimSpace(task)
	s.mu.Lock()
	s.items[approvalKey{groupID: groupID, userID: userID}] = &writeApproval{
		task: task, expiresAt: now.Add(s.ttl),
	}
	s.mu.Unlock()
	return "这个 Codex 写入任务可能有破坏性影响，需要你的明确确认。任务：\n" + task + "\n允许写入目录：当前项目 cwd（可能创建、覆盖或删除文件）。确认有效期 " + s.ttl.String() + "。请回复“确认”或“允许”后我再执行。"
}

// ObserveConfirmation accepts only a short, unambiguous confirmation phrase.
// It deliberately does not infer approval from ordinary sentences.
func (s *WriteApprovalStore) ObserveConfirmation(groupID, userID int64, text string, now time.Time) {
	if s == nil || !isExplicitConfirmation(text) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.items[approvalKey{groupID: groupID, userID: userID}]
	if item == nil || !now.Before(item.expiresAt) {
		if item != nil {
			delete(s.items, approvalKey{groupID: groupID, userID: userID})
		}
		return
	}
	item.confirmed = true
}

func (s *WriteApprovalStore) Consume(groupID, userID int64, task string, now time.Time) bool {
	if s == nil {
		return false
	}
	key := approvalKey{groupID: groupID, userID: userID}
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.items[key]
	if item == nil || !item.confirmed || !now.Before(item.expiresAt) || item.task != strings.TrimSpace(task) {
		if item != nil && !now.Before(item.expiresAt) {
			delete(s.items, key)
		}
		return false
	}
	delete(s.items, key)
	return true
}

func isExplicitConfirmation(text string) bool {
	switch strings.TrimSpace(strings.ToLower(text)) {
	case "确认", "允许", "执行", "确认执行", "允许执行", "confirm", "approve", "approved", "yes":
		return true
	default:
		return false
	}
}

type toolIdentityKey struct{}

func withToolIdentity(ctx context.Context, groupID, userID int64) context.Context {
	return context.WithValue(ctx, toolIdentityKey{}, approvalKey{groupID: groupID, userID: userID})
}

func toolIdentity(ctx context.Context) (int64, int64, bool) {
	identity, ok := ctx.Value(toolIdentityKey{}).(approvalKey)
	return identity.groupID, identity.userID, ok
}
