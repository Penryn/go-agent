package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/chat"
	"github.com/phlin/go-agent/internal/conversation"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/qq"
)

func TestConfirmActionProjectsTopicConsultationAndMemory(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	group := int64(1)
	if err := s.SaveTopics(ctx, group, []conversation.TopicView{{ID: "topic:1:1", Status: "active", Participants: []int64{10}, LastActiveAt: time.Now().Add(-time.Hour), Revision: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveNotes(ctx, group, []memory.Note{{ID: "note-consult", GroupID: group, Subject: 10, Kind: "preference", Key: "nickname", Text: "称呼小林", State: "active", Updated: time.Now(), Version: 1, Evidence: []memory.Evidence{}}}, nil); err != nil {
		t.Fatal(err)
	}
	attempt := chat.Attempt{ID: "turn:1:1", GroupID: group, InputSeq: 1, State: "planning", At: time.Now()}
	if ok, err := s.BeginAttempt(ctx, attempt); err != nil || !ok {
		t.Fatal(ok, err)
	}
	attempt.State = "sending"
	if err := s.UpdateAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	pending := memory.Note{ID: "note-pending", GroupID: group, Subject: 10, Kind: "preference", Key: "habit", Text: "喜欢早睡", State: "active", Updated: time.Now(), Version: 0, Evidence: []memory.Evidence{}}
	if err := s.SavePendingMemory(ctx, attempt.ID, group, memory.NoteSnapshots([]memory.Note{pending})); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	if err := s.ConfirmAction(ctx, attempt, chat.ActionRecord{GroupID: group, TopicID: "topic:1:1", Kind: "reaction", TargetMessageID: "55", ConfirmedAt: at}, []string{"note-consult", "note-other-group"}); err != nil {
		t.Fatal(err)
	}

	var state, deliveryKind, consultKind string
	var botActionAt sql.NullTime
	if err := s.DB.QueryRowContext(ctx, `SELECT state,delivery_kind FROM groupfriend.reply_attempts WHERE id=$1`, attempt.ID).Scan(&state, &deliveryKind); err != nil {
		t.Fatal(err)
	}
	if state != "confirmed" || deliveryKind != "action" {
		t.Fatal("attempt not confirmed as action", state, deliveryKind)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT bot_action_at FROM groupfriend.conversation_topics WHERE group_id=$1 AND topic_id='topic:1:1'`, group).Scan(&botActionAt); err != nil {
		t.Fatal(err)
	}
	if !botActionAt.Valid || botActionAt.Time.Before(at.Add(-time.Minute)) {
		t.Fatal("bot_action_at was not projected", botActionAt)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT action_kind FROM groupfriend.conversation_topic_actions WHERE attempt_id=$1 AND topic_id='topic:1:1'`, attempt.ID).Scan(&consultKind); err != nil {
		t.Fatal(err)
	}
	if consultKind != "reaction" {
		t.Fatal("action fact missing", consultKind)
	}
	var pendingState string
	var consultCount int
	if err := s.DB.QueryRowContext(ctx, `SELECT state FROM groupfriend.reply_memory_pending WHERE attempt_id=$1`, attempt.ID).Scan(&pendingState); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM groupfriend.note_consultation WHERE attempt_id=$1 AND note_id='note-consult'`, attempt.ID).Scan(&consultCount); err != nil {
		t.Fatal(err)
	}
	var useCount int64
	if err := s.DB.QueryRowContext(ctx, `SELECT use_count FROM groupfriend.notes WHERE id='note-consult'`).Scan(&useCount); err != nil {
		t.Fatal(err)
	}
	var applied string
	err := s.DB.QueryRowContext(ctx, `SELECT text FROM groupfriend.notes WHERE id='note-pending'`).Scan(&applied)
	if pendingState != "committed" || consultCount != 1 || useCount != 0 {
		t.Fatal("confirm action projections wrong", pendingState, consultCount, useCount)
	}
	if err != nil || applied != "喜欢早睡" {
		t.Fatal("pending memory was not committed", applied, err)
	}
	// Replaying the confirmation is rejected: the attempt is no longer pending.
	if err := s.ConfirmAction(ctx, attempt, chat.ActionRecord{GroupID: group, TopicID: "topic:1:1", Kind: "reaction", ConfirmedAt: time.Now()}, nil); err == nil {
		t.Fatal("confirmed attempt was confirmed twice")
	}
}

func TestPendingMemoryLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	group := int64(2)
	attempt := chat.Attempt{ID: "turn:2:1", GroupID: group, State: "planning", At: time.Now()}
	if ok, err := s.BeginAttempt(ctx, attempt); err != nil || !ok {
		t.Fatal(ok, err)
	}
	note := memory.Note{ID: "note-lifecycle", GroupID: group, Subject: 10, Kind: "preference", Key: "habit", Text: "喜欢夜宵", State: "active", Updated: time.Now(), Version: 0, Evidence: []memory.Evidence{}}
	if err := s.SavePendingMemory(ctx, attempt.ID, group, memory.NoteSnapshots([]memory.Note{note})); err != nil {
		t.Fatal(err)
	}
	// A re-staged attempt keeps its original snapshot.
	changed := memory.NoteSnapshots([]memory.Note{note})
	changed[0].Note.Text = "覆盖"
	if err := s.SavePendingMemory(ctx, attempt.ID, group, changed); err != nil {
		t.Fatal(err)
	}
	if err := s.DiscardPendingMemory(ctx, attempt.ID, "failed"); err != nil {
		t.Fatal(err)
	}
	var state, reason, text string
	if err := s.DB.QueryRowContext(ctx, `SELECT state,last_error FROM groupfriend.reply_memory_pending WHERE attempt_id=$1`, attempt.ID).Scan(&state, &reason); err != nil {
		t.Fatal(err)
	}
	rawErr := s.DB.QueryRowContext(ctx, `SELECT changes->0->>'text' FROM groupfriend.reply_memory_pending WHERE attempt_id=$1`, attempt.ID).Scan(&text)
	if state != "discarded" || reason != "failed" || rawErr != nil || text != "喜欢夜宵" {
		t.Fatal("discard kept the wrong snapshot", state, reason, text, rawErr)
	}
	// A discarded snapshot never commits: confirming the attempt writes no note.
	attempt.State = "sending"
	if err := s.UpdateAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmAction(ctx, attempt, chat.ActionRecord{GroupID: group, Kind: "poke", ConfirmedAt: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM groupfriend.notes WHERE id='note-lifecycle'`).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "0" {
		t.Fatal("discarded memory was committed")
	}
}

func TestFinishAttemptCommitsPendingMemory(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	group := int64(3)
	attempt := chat.Attempt{ID: "turn:3:1", GroupID: group, State: "planning", At: time.Now()}
	if ok, err := s.BeginAttempt(ctx, attempt); err != nil || !ok {
		t.Fatal(ok, err)
	}
	attempt.State = "sending"
	if err := s.UpdateAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	note := memory.Note{ID: "note-finish", GroupID: group, Subject: 10, Kind: "preference", Key: "habit", Text: "喜欢午睡", State: "active", Updated: time.Now(), Version: 0, Evidence: []memory.Evidence{}}
	if err := s.SavePendingMemory(ctx, attempt.ID, group, memory.NoteSnapshots([]memory.Note{note})); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDelivery(ctx, attempt.ID, "77", "message"); err != nil {
		t.Fatal(err)
	}
	out := qqEvent(group, "77")
	if err := s.FinishAttempt(ctx, attempt, out, nil); err != nil {
		t.Fatal(err)
	}
	var state string
	var text string
	if err := s.DB.QueryRowContext(ctx, `SELECT state FROM groupfriend.reply_memory_pending WHERE attempt_id=$1`, attempt.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	noteErr := s.DB.QueryRowContext(ctx, `SELECT text FROM groupfriend.notes WHERE id='note-finish'`).Scan(&text)
	if state != "committed" || noteErr != nil || text != "喜欢午睡" {
		t.Fatal("finish attempt did not commit pending memory", state, text, noteErr)
	}
	var deliveryID string
	if err := s.DB.QueryRowContext(ctx, `SELECT delivery_message_id FROM groupfriend.reply_attempts WHERE id=$1`, attempt.ID).Scan(&deliveryID); err != nil || deliveryID != "77" {
		t.Fatal("delivery receipt not persisted", deliveryID, err)
	}
}

func TestOutboundUniqueIndexSkipsDuplicates(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	required := dsn != ""
	if dsn == "" {
		dsn = "postgres://qqbot:qqbotpass@127.0.0.1:5432/postgres?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", dsn)
	if err == nil {
		err = admin.PingContext(ctx)
	}
	cancel()
	if err != nil {
		if admin != nil {
			admin.Close()
		}
		if required {
			t.Fatal(err)
		}
		t.Skipf("PostgreSQL unavailable: %v", err)
	}
	name := fmt.Sprintf("groupfriend_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(context.Background(), "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close()
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + name
	first, err := Open(context.Background(), parsed.String(), 3)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a pre-feature database: drop the guard, then archive two
	// outbound rows that share a platform id.
	if _, err := first.DB.ExecContext(context.Background(), `DROP INDEX groupfriend.friend_messages_outbound_unique`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"out:a", "out:b"} {
		if _, err := first.DB.ExecContext(context.Background(), `INSERT INTO groupfriend.messages(event_id,group_id,user_id,message_id,outbound,event_at,body) VALUES($1,1,99,'77',TRUE,NOW(),'{}')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(context.Background(), parsed.String(), 3)
	if err != nil {
		t.Fatal("reopen with duplicates failed", err)
	}
	defer second.Close()
	var skipped string
	err = second.DB.QueryRowContext(context.Background(), `SELECT value FROM groupfriend.settings WHERE key='outbound_unique_index'`).Scan(&skipped)
	if err != nil || skipped != "skipped_duplicates" {
		t.Fatal("duplicate skip was not recorded", skipped, err)
	}
	var indexed int
	if err := second.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM pg_indexes WHERE indexname='friend_messages_outbound_unique'`).Scan(&indexed); err != nil || indexed != 0 {
		t.Fatal("unique index was created over duplicates", indexed, err)
	}
}

func qqEvent(group int64, messageID string) qq.Event {
	return qq.Event{ID: "out:" + messageID, MessageID: messageID, GroupID: group, UserID: 99, Outbound: true, At: time.Now()}
}

func TestReconcileOutboundClosesUnknownAttempt(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	group := int64(4)
	if err := s.SaveNotes(ctx, group, []memory.Note{{ID: "note-7", GroupID: group, Subject: 10, Kind: "preference", Key: "nickname", Text: "称呼小林", State: "active", Updated: time.Now(), Version: 1, Evidence: []memory.Evidence{}}}, nil); err != nil {
		t.Fatal(err)
	}
	attempt := chat.Attempt{ID: "turn:4:1", GroupID: group, State: "planning", At: time.Now(), Reply: chat.Reply{Action: "text", Text: "记住了"}, Retrieval: memory.Trace{Selected: []string{"note-7"}}}
	if ok, err := s.BeginAttempt(ctx, attempt); err != nil || !ok {
		t.Fatal(ok, err)
	}
	attempt.State = "sending"
	if err := s.UpdateAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDelivery(ctx, attempt.ID, "77", "message"); err != nil {
		t.Fatal(err)
	}
	// Simulate an archive failure after a confirmed platform send.
	attempt.State = "unknown"
	attempt.Error = "delivery confirmed but archive failed"
	if err := s.UpdateAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePendingMemory(ctx, attempt.ID, group, memory.NoteSnapshots([]memory.Note{{ID: "note-reconcile", GroupID: group, Subject: 10, Kind: "preference", Key: "habit", Text: "喜欢夜跑", State: "active", Updated: time.Now(), Version: 0, Evidence: []memory.Evidence{}}})); err != nil {
		t.Fatal(err)
	}
	due, err := s.ReconcileDue(ctx, 10)
	if err != nil || len(due) != 1 || due[0].Kind != "message" || due[0].MessageID != "77" {
		t.Fatal("candidate not surfaced", due, err)
	}
	if err := s.ReconcileOutbound(ctx, due[0].Attempt, qqEvent(group, "77"), []string{"note-7"}); err != nil {
		t.Fatal(err)
	}
	var state, reconcileState string
	if err := s.DB.QueryRowContext(ctx, `SELECT state,reconcile_state FROM groupfriend.reply_attempts WHERE id=$1`, attempt.ID).Scan(&state, &reconcileState); err != nil {
		t.Fatal(err)
	}
	if state != "confirmed" || reconcileState != "resolved" {
		t.Fatal("attempt not resolved", state, reconcileState)
	}
	reply, err := s.IsBotReply(ctx, group, "77")
	if err != nil || !reply {
		t.Fatal("reconciled message is not an archived bot reply", reply, err)
	}
	var useCount int64
	var pendingState string
	if err := s.DB.QueryRowContext(ctx, `SELECT use_count FROM groupfriend.notes WHERE id='note-7'`).Scan(&useCount); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT state FROM groupfriend.reply_memory_pending WHERE attempt_id=$1`, attempt.ID).Scan(&pendingState); err != nil {
		t.Fatal(err)
	}
	if useCount != 1 || pendingState != "committed" {
		t.Fatal("reconcile did not finish projections", useCount, pendingState)
	}
	// The closure is single-shot: an already resolved attempt no longer closes.
	if err := s.ReconcileOutbound(ctx, due[0].Attempt, qqEvent(group, "77"), nil); err == nil {
		t.Fatal("resolved attempt was closed twice")
	}
	if due, err = s.ReconcileDue(ctx, 10); err != nil || len(due) != 0 {
		t.Fatal("resolved attempt still due", due, err)
	}
}

func TestReconcileDeferAndFailCursor(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	group := int64(5)
	attempt := chat.Attempt{ID: "turn:5:1", GroupID: group, State: "unknown", At: time.Now()}
	if ok, err := s.BeginAttempt(ctx, attempt); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := s.UpdateAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkReconcile(ctx, attempt.ID, "action confirmed but archive failed"); err != nil {
		t.Fatal(err)
	}
	due, err := s.ReconcileDue(ctx, 10)
	if err != nil || len(due) != 1 || due[0].Kind != "action" {
		t.Fatal("action candidate not surfaced", due, err)
	}
	if err := s.DeferReconcile(ctx, attempt.ID, "query uncertain", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if due, err = s.ReconcileDue(ctx, 10); err != nil || len(due) != 0 {
		t.Fatal("deferred candidate still due", due, err)
	}
	if err := s.FailReconcile(ctx, attempt.ID, "identity mismatch"); err != nil {
		t.Fatal(err)
	}
	var reconcileState, reason string
	if err := s.DB.QueryRowContext(ctx, `SELECT reconcile_state,reconcile_error FROM groupfriend.reply_attempts WHERE id=$1`, attempt.ID).Scan(&reconcileState, &reason); err != nil {
		t.Fatal(err)
	}
	if reconcileState != "unresolvable" || reason != "identity mismatch" {
		t.Fatal("unresolvable state wrong", reconcileState, reason)
	}
	// Uncertain sends without a receipt never surface: there is no query.
	bare := chat.Attempt{ID: "turn:5:2", GroupID: group, State: "unknown", At: time.Now()}
	if ok, err := s.BeginAttempt(ctx, bare); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := s.UpdateAttempt(ctx, bare); err != nil {
		t.Fatal(err)
	}
	if due, err = s.ReconcileDue(ctx, 10); err != nil || len(due) != 0 {
		t.Fatal("receipt-less unknown surfaced", due, err)
	}
}
