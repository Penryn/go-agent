package group_actor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
	"github.com/phlin/go-agent/internal/humanbot/runtime/ingress"
)

func TestManagerMergesShortBurstAndKeepsOutboundEvent(t *testing.T) {
	log := ingress.NewMemoryEventLog()
	store := inmemory.NewStore()
	manager := NewManager(log, WithTailSize(4), WithArchive(store))
	defer manager.Close()

	base := time.Unix(100, 0)
	first := eventRecord("e1", 1, 7, "今晚开黑吗", base)
	second := eventRecord("e2", 1, 7, "八点行不行", base.Add(time.Second))
	memory, err := manager.Observe(context.Background(), first)
	if err != nil {
		t.Fatalf("observe first: %v", err)
	}
	memory, err = manager.Observe(context.Background(), second)
	if err != nil {
		t.Fatalf("observe second: %v", err)
	}
	if len(memory.CurrentBurst.EventIDs) != 2 || memory.CurrentBurst.Text != "今晚开黑吗 八点行不行" {
		t.Fatalf("burst was not merged: %+v", memory.CurrentBurst)
	}
	if memory.Candidates[0].Status != humandomain.CandidateCancelled || memory.Candidates[1].Status != humandomain.CandidatePending {
		t.Fatalf("burst candidates were not superseded: %+v", memory.Candidates)
	}
	if ok, err := manager.CanExecute(context.Background(), 1, memory.Candidates[0].CandidateID, base.Add(time.Second)); err != nil || ok {
		t.Fatalf("superseded candidate must not execute: ok=%v err=%v", ok, err)
	}

	outbound := eventRecord("out-1", 1, 999, "那就八点", base.Add(3*time.Second))
	outbound.Origin = humandomain.OriginOutbound
	memory, err = manager.Observe(context.Background(), outbound)
	if err != nil {
		t.Fatalf("observe outbound: %v", err)
	}
	if len(memory.RecentTail) != 3 || memory.CurrentBurst.EventIDs != nil {
		t.Fatalf("outbound event did not settle burst: %+v", memory)
	}
	if _, err := manager.Observe(context.Background(), outbound); err != nil {
		t.Fatalf("observe duplicate outbound: %v", err)
	}
	archived, err := store.RecentEvents(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("read archived events: %v", err)
	}
	if len(archived) != 3 {
		t.Fatalf("duplicate event was archived: %d", len(archived))
	}
}

func TestClassifyDialogueActPreservesConversationPurpose(t *testing.T) {
	cases := []struct {
		name          string
		text          string
		direct        bool
		hasAttachment bool
		wantIntent    string
		wantReason    string
	}{
		{name: "direct help", text: "能不能帮我看看报错", direct: true, wantIntent: "request_help", wantReason: "direct_request"},
		{name: "direct distress beats request", text: "我好难受，能陪我一下吗", direct: true, wantIntent: "support", wantReason: "direct_distress"},
		{name: "ambient distress", text: "今天真的好累", wantIntent: "support", wantReason: "distress_observed"},
		{name: "question", text: "明天会下雨吗？", wantIntent: "question", wantReason: "question_observed"},
		{name: "banter", text: "笑死，这也太离谱了", wantIntent: "banter", wantReason: "banter_observed"},
		{name: "media", hasAttachment: true, wantIntent: "react", wantReason: "media_reaction"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			intent, _, reason := classifyDialogueAct(tc.text, tc.direct, tc.hasAttachment)
			if intent != tc.wantIntent || reason != tc.wantReason {
				t.Fatalf("classifyDialogueAct() = (%q, %q), want (%q, %q)", intent, reason, tc.wantIntent, tc.wantReason)
			}
		})
	}
}

func TestManagerEnrichesMediaOnlyForRecentEvent(t *testing.T) {
	manager := NewManager(ingress.NewMemoryEventLog())
	defer manager.Close()

	record := eventRecord("media-1", 1, 7, "", time.Unix(100, 0))
	record.Event.Attachments = []mediadomain.MultimodalAttachment{{AttachmentID: "sticker-1", Kind: mediadomain.MediaSticker}}
	if _, err := manager.Observe(context.Background(), record); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if err := manager.EnrichMedia(context.Background(), 1, "media-1", []mediadomain.MediaDescriptor{{AttachmentID: "sticker-1", Kind: mediadomain.MediaSticker, Summary: "开心小狗"}}); err != nil {
		t.Fatalf("enrich media: %v", err)
	}
	memory, err := manager.Snapshot(context.Background(), 1)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got := memory.MediaByEvent["media-1"][0].Summary; got != "开心小狗" {
		t.Fatalf("unexpected media summary: %q", got)
	}
	if err := manager.EnrichMedia(context.Background(), 1, "missing", []mediadomain.MediaDescriptor{{Summary: "ignored"}}); err != nil {
		t.Fatalf("enrich missing event: %v", err)
	}
	memory, err = manager.Snapshot(context.Background(), 1)
	if err != nil {
		t.Fatalf("snapshot after missing enrichment: %v", err)
	}
	if _, ok := memory.MediaByEvent["missing"]; ok {
		t.Fatal("missing event should not have media enrichment")
	}
}

func TestManagerEnqueuesProactiveCandidateThroughActor(t *testing.T) {
	store := inmemory.NewStore()
	manager := NewManager(ingress.NewMemoryEventLog(), WithStateStore(store))
	defer manager.Close()

	due := time.Unix(100, 0)
	candidate := humandomain.ThoughtCandidate{
		CandidateID: "follow-up-1",
		Intent:      "follow_up",
		ReasonCode:  "open_loop",
		Urgency:     0.8,
		DueAt:       due,
		ExpiresAt:   due.Add(time.Minute),
	}
	if err := manager.EnqueueCandidate(context.Background(), 9, candidate); err != nil {
		t.Fatalf("enqueue candidate: %v", err)
	}
	if err := manager.EnqueueCandidate(context.Background(), 9, candidate); err == nil {
		t.Fatal("duplicate candidate should be rejected")
	}
	selected, ok, err := manager.ClaimDue(context.Background(), 9, due.Add(time.Second), 0.5)
	if err != nil || !ok {
		t.Fatalf("claim enqueued candidate: candidate=%+v ok=%v err=%v", selected, ok, err)
	}
	if selected.CandidateID != candidate.CandidateID || selected.DeliveryTarget != "group" || selected.Status != humandomain.CandidateAccepted {
		t.Fatalf("unexpected candidate defaults: %+v", selected)
	}
	loaded, err := store.LoadWorkingMemory(context.Background(), 9)
	if err != nil || len(loaded.Candidates) != 1 || loaded.Candidates[0].Status != humandomain.CandidateAccepted {
		t.Fatalf("candidate was not persisted: memory=%+v err=%v", loaded, err)
	}
}

func TestManagerRestoresWorkingMemoryAfterRestart(t *testing.T) {
	store := inmemory.NewStore()
	record := eventRecord("persist-1", 9, 7, "状态要保留", time.Unix(100, 0))

	first := NewManager(ingress.NewMemoryEventLog(), WithStateStore(store))
	if _, err := first.Observe(context.Background(), record); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first manager: %v", err)
	}

	second := NewManager(ingress.NewMemoryEventLog(), WithStateStore(store))
	defer second.Close()
	memory, err := second.Snapshot(context.Background(), 9)
	if err != nil {
		t.Fatalf("restore snapshot: %v", err)
	}
	if len(memory.RecentTail) != 1 || len(memory.Candidates) != 1 {
		t.Fatalf("working memory was not restored: %+v", memory)
	}
	if memory.Candidates[0].Status != humandomain.CandidatePending {
		t.Fatalf("candidate status changed during restore: %+v", memory.Candidates[0])
	}
}

func TestManagerBoundsCandidateState(t *testing.T) {
	manager := NewManager(ingress.NewMemoryEventLog())
	defer manager.Close()

	base := time.Unix(1000, 0)
	for i := 0; i < defaultMaxCandidates+40; i++ {
		record := eventRecord(fmt.Sprintf("bounded-%d", i), 11, int64(i+1), "独立消息", base.Add(time.Duration(i)*time.Minute))
		if _, err := manager.Observe(context.Background(), record); err != nil {
			t.Fatalf("observe %d: %v", i, err)
		}
	}
	memory, err := manager.Snapshot(context.Background(), 11)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(memory.Candidates) > defaultMaxCandidates {
		t.Fatalf("candidate state exceeded bound: %d", len(memory.Candidates))
	}
}

func TestManagerPrunesIdleActorsButKeepsLiveCandidates(t *testing.T) {
	store := inmemory.NewStore()
	manager := NewManager(ingress.NewMemoryEventLog(), WithStateStore(store), WithIdleTTL(time.Minute))
	defer manager.Close()

	idleRecord := eventRecord("idle-1", 21, 7, "hello", time.Now())
	idleRecord.Origin = humandomain.OriginOutbound
	if _, err := manager.Observe(context.Background(), idleRecord); err != nil {
		t.Fatalf("observe: %v", err)
	}
	future := time.Now().Add(2 * time.Minute)
	if retired := manager.PruneIdle(context.Background(), future); retired != 1 {
		t.Fatalf("expected one idle actor retired, got %d", retired)
	}
	if ids := manager.GroupIDs(); len(ids) != 0 {
		t.Fatalf("idle actor still registered: %v", ids)
	}
	if _, err := store.LoadWorkingMemory(context.Background(), 21); err != nil {
		t.Fatalf("working memory was not persisted before retirement: %v", err)
	}

	due := time.Now()
	if err := manager.EnqueueCandidate(context.Background(), 22, humandomain.ThoughtCandidate{
		CandidateID: "live-1", DueAt: due, ExpiresAt: due.Add(time.Hour), Score: 1,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if retired := manager.PruneIdle(context.Background(), time.Now().Add(2*time.Minute)); retired != 0 {
		t.Fatalf("actor with live candidate should not retire, got %d", retired)
	}
}

func eventRecord(id string, groupID, userID int64, text string, at time.Time) humandomain.EventRecord {
	return humandomain.EventRecord{
		EventID:   id,
		GroupID:   groupID,
		UserID:    userID,
		Origin:    humandomain.OriginInbound,
		Timestamp: at,
		Event: conversationdomain.ConversationEvent{
			EventID:       id,
			GroupID:       groupID,
			UserID:        userID,
			Kind:          conversationdomain.EventMessage,
			Text:          text,
			TimestampUnix: at.Unix(),
		},
	}
}

func TestPokeEventProducesHighUrgencyCandidate(t *testing.T) {
	log := ingress.NewMemoryEventLog()
	manager := NewManager(log)
	defer manager.Close()

	poke := eventRecord("poke-1", 1, 7, "", time.Unix(100, 0))
	poke.Event.Kind = conversationdomain.EventPoke
	memory, err := manager.Observe(context.Background(), poke)
	if err != nil {
		t.Fatalf("observe poke: %v", err)
	}
	if len(memory.Candidates) != 1 {
		t.Fatalf("expected one poke candidate, got %d", len(memory.Candidates))
	}
	candidate := memory.Candidates[0]
	if candidate.Intent != "poke_reply" || candidate.Score < 0.7 {
		t.Fatalf("unexpected poke candidate: %+v", candidate)
	}
	// poke 不进 burst 合并
	if len(memory.CurrentBurst.EventIDs) != 0 {
		t.Fatalf("poke should not join the conversation burst: %+v", memory.CurrentBurst)
	}
	// 到期后可被 claim（DueAt = poke 时刻 + 1.2~3s 抖动）
	claimed, ok, err := manager.ClaimDue(context.Background(), 1, time.Unix(105, 0), 0.5)
	if err != nil || !ok || claimed.Intent != "poke_reply" {
		t.Fatalf("poke candidate should be claimable: ok=%v err=%v intent=%s", ok, err, claimed.Intent)
	}
}
