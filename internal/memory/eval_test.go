package memory_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/qq"
	"github.com/phlin/go-agent/internal/replay"
)

type evalNote struct {
	ID        string            `json:"id"`
	GroupID   int64             `json:"group_id"`
	Subject   int64             `json:"subject"`
	Kind      string            `json:"kind"`
	Key       string            `json:"key"`
	TopicID   string            `json:"topic_id"`
	Text      string            `json:"text"`
	State     string            `json:"state"`
	Updated   string            `json:"updated"`
	ValidFrom string            `json:"valid_from"`
	ValidTo   string            `json:"valid_to"`
	Evidence  []memory.Evidence `json:"evidence"`
}

type evalCase struct {
	Name       string   `json:"name"`
	GroupID    int64    `json:"group_id"`
	UserID     int64    `json:"user_id"`
	Query      string   `json:"query"`
	Topics     []string `json:"topics"`
	Wanted     []string `json:"wanted"`
	Forbidden  []string `json:"forbidden"`
	Correction bool     `json:"correction"`
}

type evalRebuild struct {
	GroupID   int64  `json:"group_id"`
	Subject   int64  `json:"subject"`
	Kind      string `json:"kind"`
	Key       string `json:"key"`
	Text      string `json:"text"`
	EventID   string `json:"event_id"`
	EventText string `json:"event_text"`
	Quote     string `json:"quote"`
	UntilSeq  int64  `json:"until_seq"`
}

type evalSet struct {
	Name    string      `json:"name"`
	Notes   []evalNote  `json:"notes"`
	Cases   []evalCase  `json:"cases"`
	Rebuild evalRebuild `json:"rebuild"`
}

func evalOffset(t *testing.T, value string, now time.Time) *time.Time {
	t.Helper()
	if value == "" {
		return nil
	}
	offset, err := time.ParseDuration(strings.TrimPrefix(value, "+"))
	if err != nil {
		t.Fatalf("invalid eval offset %q: %v", value, err)
	}
	at := now.Add(offset)
	return &at
}

// TestMemoryEvaluationSet runs the sanitized recall fixture and reports the
// quality baseline. It intentionally uses lexical/contextual/topic recall only;
// semantic recall needs a live embedding model and is out of scope here.
func TestMemoryEvaluationSet(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "tests", "memory", "eval_set.json"))
	if err != nil {
		t.Fatal(err)
	}
	var set evalSet
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now()
	store := replay.NewStore()
	all := []memory.Note{}
	byGroup := map[int64][]memory.Note{}
	for _, raw := range set.Notes {
		note := memory.Note{ID: raw.ID, GroupID: raw.GroupID, Subject: raw.Subject, Kind: raw.Kind, Key: raw.Key, TopicID: raw.TopicID, Text: raw.Text, State: raw.State, Evidence: raw.Evidence, Updated: now}
		if note.State == "" {
			note.State = "active"
		}
		if raw.Updated != "" {
			if at := evalOffset(t, raw.Updated, now); at != nil {
				note.Updated = *at
			}
		}
		note.ValidFrom = evalOffset(t, raw.ValidFrom, now)
		note.ValidTo = evalOffset(t, raw.ValidTo, now)
		all = append(all, note)
		byGroup[note.GroupID] = append(byGroup[note.GroupID], note)
	}
	for group, notes := range byGroup {
		if err := store.SaveNotes(ctx, group, notes, nil); err != nil {
			t.Fatal(err)
		}
	}
	stateByID := map[string]string{}
	for _, note := range all {
		stateByID[note.ID] = note.State
	}
	service := memory.Service{Store: store, TopK: 8, Budgets: memory.RecallBudgets{Preference: 6, Member: 8, Episode: 6}}
	totalHits, totalWanted, totalReturned := 0, 0, 0
	forbiddenViolations, forgottenViolations, negativeUnexpected := 0, 0, 0
	correctionCases, correctionOK := 0, 0
	for _, c := range set.Cases {
		notes, _, err := service.RecallCached(ctx, memory.Query{
			GroupID:  c.GroupID,
			Events:   []qq.Event{{ID: "eval-" + c.Name, GroupID: c.GroupID, UserID: c.UserID, Text: c.Query}},
			Text:     c.Query,
			TopicIDs: c.Topics,
		}, 0)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		returned := map[string]bool{}
		returnedIDs := []string{}
		for _, note := range notes {
			returned[note.ID] = true
			returnedIDs = append(returnedIDs, note.ID)
			if stateByID[note.ID] == "forgotten" {
				forgottenViolations++
			}
		}
		t.Logf("case %-26s returned=%v wanted=%v", c.Name, returnedIDs, c.Wanted)
		for _, id := range c.Forbidden {
			if returned[id] {
				forbiddenViolations++
			}
		}
		if len(c.Wanted) == 0 {
			negativeUnexpected += len(notes)
			continue
		}
		hits := 0
		for _, id := range c.Wanted {
			if returned[id] {
				hits++
			}
		}
		totalHits += hits
		totalWanted += len(c.Wanted)
		totalReturned += len(notes)
		if c.Correction {
			correctionCases++
			ok := true
			for _, id := range c.Wanted {
				ok = ok && returned[id]
			}
			for _, id := range c.Forbidden {
				ok = ok && !returned[id]
			}
			if ok {
				correctionOK++
			}
		}
	}
	identities := map[memory.Identity]int{}
	for _, note := range all {
		identities[memory.NoteIdentity(note)]++
	}
	duplicates := 0
	for _, count := range identities {
		if count > 1 {
			duplicates++
		}
	}
	duplicateRate := float64(duplicates) / float64(len(identities))
	precision, recall := 0.0, 0.0
	if totalReturned > 0 {
		precision = float64(totalHits) / float64(totalReturned)
	}
	if totalWanted > 0 {
		recall = float64(totalHits) / float64(totalWanted)
	}
	correctionConsistency := 0.0
	if correctionCases > 0 {
		correctionConsistency = float64(correctionOK) / float64(correctionCases)
	}

	r := set.Rebuild
	event := qq.Event{ID: r.EventID, GroupID: r.GroupID, UserID: r.Subject, Text: r.EventText, At: now, Seq: r.UntilSeq}
	change := memory.Change{Subject: r.Subject, Kind: r.Kind, Key: r.Key, Text: r.Text, Evidence: []memory.Evidence{{EventID: r.EventID, Quote: r.Quote}}}
	if err := service.Apply(ctx, r.GroupID, []qq.Event{event}, []memory.Change{change}, &memory.Progress{GroupID: r.GroupID, After: 0, Until: r.UntilSeq}); err == nil {
		forgottenViolations++
	}
	if len(store.MemoryEvents()) == 0 {
		t.Fatal("blocked rebuild was not recorded as a memory event")
	}

	t.Logf("memory eval %q: precision@k=%.2f recall@k=%.2f duplicate_rate=%.2f forgotten_violations=%d forbidden_violations=%d negative_unexpected=%d correction_consistency=%.2f",
		set.Name, precision, recall, duplicateRate, forgottenViolations, forbiddenViolations, negativeUnexpected, correctionConsistency)
	if precision < 0.8 || recall < 0.8 {
		t.Fatalf("recall quality regressed: precision=%.2f recall=%.2f", precision, recall)
	}
	if forgottenViolations != 0 || forbiddenViolations != 0 || negativeUnexpected != 0 {
		t.Fatalf("isolation violations: forgotten=%d forbidden=%d negative=%d", forgottenViolations, forbiddenViolations, negativeUnexpected)
	}
	if correctionCases > 0 && correctionOK != correctionCases {
		t.Fatalf("correction consistency = %d/%d", correctionOK, correctionCases)
	}
	if duplicateRate > 0.1 {
		t.Fatalf("duplicate note rate = %.2f", duplicateRate)
	}
}
