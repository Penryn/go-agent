package chat_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/chat"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/qq"
	"github.com/phlin/go-agent/internal/replay"
)

func seedPreference(t *testing.T, store *replay.Store, group int64, id string) {
	t.Helper()
	if err := store.SaveNotes(context.Background(), group, []memory.Note{{
		ID: id, GroupID: group, Kind: "preference", Key: "nickname", Text: "称呼小林", State: "active", Updated: time.Now(),
	}}, nil); err != nil {
		t.Fatal(err)
	}
}

func noteUsage(t *testing.T, store *replay.Store, group int64, id string) memory.Note {
	t.Helper()
	notes, err := store.ActiveNotes(context.Background(), group, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range notes {
		if note.ID == id {
			return note
		}
	}
	t.Fatalf("note %s not found", id)
	return memory.Note{}
}

type failingFinishStore struct{ *replay.Store }

func (s *failingFinishStore) FinishAttempt(context.Context, chat.Attempt, qq.Event, []string) error {
	return errors.New("archive unavailable")
}

// TestArchiveFailureIsRecordedAsUnknownAndBookedNowhere covers the local
// transaction failure after a confirmed QQ send: the attempt must become
// unknown with the archive error, and no usage may be booked.
func TestArchiveFailureIsRecordedAsUnknownAndBookedNowhere(t *testing.T) {
	base := replay.NewStore()
	store := &failingFinishStore{Store: base}
	seedPreference(t, base, 1, "note-archive")
	r := chat.New(context.Background(), &chat.Runtime{Store: store, Model: generator{func(context.Context, any) (chat.Reply, error) {
		return chat.Reply{Action: "text", Text: "好"}, nil
	}}, Sender: &replay.Sender{}, Memory: &memory.Service{Store: store, TopK: 3}, SelfID: 99, Persona: chat.Persona{Name: "芙芙"},
		Config: chat.Config{Merge: 5 * time.Millisecond, MaxWait: 100 * time.Millisecond, PassiveInterval: time.Millisecond, ActiveWindow: time.Second, Timeout: time.Second, SpontaneousInterval: time.Hour, MaxPerMinute: 20, History: 80, ContextRunes: 20000}})
	t.Cleanup(r.Close)
	a, err := r.Process(context.Background(), payload(1, 1, 10, "确认", true))
	if err != nil || a.State != "unknown" || a.Error != "delivery confirmed but archive failed" {
		t.Fatalf("outcome = %+v err=%v", a, err)
	}
	stored, ok := base.Attempt(a.ID)
	if !ok || stored.State != "unknown" || stored.Error != "delivery confirmed but archive failed" {
		t.Fatalf("stored attempt = %+v ok=%v", stored, ok)
	}
	if len(base.Usage(a.ID)) != 0 || noteUsage(t, base, 1, "note-archive").UseCount != 0 {
		t.Fatal("failed archive booked usage")
	}
}

// TestUsageLedgerOnlyCountsConfirmedTurns exercises the turn states that must
// not book usage: silent, failed, limited, unknown and preview.
func TestUsageLedgerOnlyCountsConfirmedTurns(t *testing.T) {
	store := replay.NewStore()
	sender := &replay.Sender{}
	generator := generator{func(_ context.Context, input any) (chat.Reply, error) {
		text := input.(map[string]any)["batch"].([]qq.Event)[0].Text
		switch text {
		case "沉默":
			return chat.Reply{Action: "silent"}, nil
		case "失败":
			return chat.Reply{}, errors.New("model down")
		default:
			return chat.Reply{Action: "text", Text: "好"}, nil
		}
	}}
	r := chat.New(context.Background(), &chat.Runtime{Store: store, Model: generator, Sender: sender, Memory: &memory.Service{Store: store, TopK: 3}, SelfID: 99, Persona: chat.Persona{Name: "芙芙"},
		Config: chat.Config{Merge: 5 * time.Millisecond, MaxWait: 100 * time.Millisecond, PassiveInterval: time.Millisecond, ActiveWindow: time.Second, Timeout: time.Second, SpontaneousInterval: time.Hour, MaxPerMinute: 20, History: 80, ContextRunes: 20000}})
	t.Cleanup(r.Close)

	cases := []struct {
		group int64
		text  string
		state string
	}{
		{1, "确认", "confirmed"},
		{2, "沉默", "silent"},
		{3, "失败", "failed"},
		{4, "未知", "unknown"},
		{5, "预演", "preview"},
	}
	for _, tc := range cases {
		noteID := fmt.Sprintf("note-%d", tc.group)
		seedPreference(t, store, tc.group, noteID)
		if tc.state == "unknown" {
			sender.Error = &qq.SendError{Err: context.DeadlineExceeded, Definite: false}
		}
		if tc.state == "preview" {
			r.Sender = nil
		}
		a, err := r.Process(context.Background(), payload(int(tc.group), int(tc.group), 10, tc.text, true))
		sender.Error = nil
		r.Sender = sender
		if err != nil || a.State != tc.state {
			t.Fatalf("%s turn: state=%s err=%v", tc.text, a.State, err)
		}
		booked := store.Usage(a.ID)
		note := noteUsage(t, store, tc.group, noteID)
		if tc.state == "confirmed" {
			if len(booked) != 1 || booked[0] != noteID || note.UseCount != 1 {
				t.Fatalf("confirmed turn booked usage=%v count=%d", booked, note.UseCount)
			}
			continue
		}
		if len(booked) != 0 || note.UseCount != 0 {
			t.Fatalf("%s turn booked usage=%v count=%d", tc.state, booked, note.UseCount)
		}
	}

	// The send budget rejects the second spontaneous turn before retrieval, so
	// only the first confirmed turn is booked.
	seedPreference(t, store, 6, "note-6")
	first, err := r.Process(context.Background(), payload(61, 6, 10, "第一句", true))
	if err != nil || first.State != "confirmed" {
		t.Fatal(first, err)
	}
	r.Config.MaxPerMinute = 1
	second, err := r.Process(context.Background(), payload(62, 6, 10, "第二句", false))
	if err != nil || second.State != "limited" {
		t.Fatal(second, err)
	}
	if len(store.Usage(second.ID)) != 0 || noteUsage(t, store, 6, "note-6").UseCount != 1 {
		t.Fatal("limited turn booked usage")
	}
}
