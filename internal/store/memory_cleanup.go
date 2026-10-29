package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/phlin/go-agent/internal/memory"
)

type DuplicateNote struct {
	ID        string    `json:"id"`
	UpdatedAt time.Time `json:"updated_at"`
	Text      string    `json:"text"`
	Evidence  int       `json:"evidence"`
}

type DuplicateGroup struct {
	GroupID int64           `json:"group_id"`
	Subject int64           `json:"subject"`
	Kind    string          `json:"kind"`
	Key     string          `json:"key"`
	TopicID string          `json:"topic_id,omitempty"`
	Keep    DuplicateNote   `json:"keep"`
	Archive []DuplicateNote `json:"archive"`
}

type NormalizedNote struct {
	ID         string `json:"id"`
	OldKey     string `json:"old_key"`
	NewKey     string `json:"new_key"`
	OldTopicID string `json:"old_topic_id,omitempty"`
	NewTopicID string `json:"new_topic_id,omitempty"`
}

type DuplicateReport struct {
	Applied      bool             `json:"applied"`
	IndexExists  bool             `json:"index_exists"`
	IndexCreated bool             `json:"index_created"`
	Normalized   []NormalizedNote `json:"normalized"`
	Duplicates   []DuplicateGroup `json:"duplicates"`
}

type cleanupQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type cleanupNote struct {
	id       string
	group    int64
	subject  int64
	kind     string
	key      string
	topicID  string
	state    string
	text     string
	updated  time.Time
	evidence int
}

type cleanupIdentity struct {
	group   int64
	subject int64
	kind    string
	key     string
	topicID string
}

// MemoryDuplicates normalizes note identities and archives active duplicates.
// A dry run only reports; apply runs normalization, archiving and active
// identity index creation in one transaction under an exclusive maintenance
// lock. Forgotten rows participate in normalization but are never archived or
// merged.
func (s *Store) MemoryDuplicates(ctx context.Context, apply bool) (DuplicateReport, error) {
	if !apply {
		return readDuplicateReport(ctx, s.DB)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return DuplicateReport{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, memoryMaintenanceLock); err != nil {
		return DuplicateReport{}, err
	}
	report, err := readDuplicateReport(ctx, tx)
	if err != nil {
		return DuplicateReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS groupfriend.friend_notes_active_identity`); err != nil {
		return DuplicateReport{}, err
	}
	for _, note := range report.Normalized {
		result, err := tx.ExecContext(ctx, `UPDATE groupfriend.notes SET key=$2,topic_id=$3,revision=revision+1,memory_revision=memory_revision+1,embedding=NULL,indexed_revision=0 WHERE id=$1 AND key=$4 AND topic_id=$5`, note.ID, note.NewKey, note.NewTopicID, note.OldKey, note.OldTopicID)
		if err != nil {
			return DuplicateReport{}, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return DuplicateReport{}, fmt.Errorf("note %s changed during identity cleanup", note.ID)
		}
	}
	for _, group := range report.Duplicates {
		for _, archived := range group.Archive {
			result, err := tx.ExecContext(ctx, `UPDATE groupfriend.notes SET state='archived',revision=revision+1,memory_revision=memory_revision+1,embedding=NULL,indexed_revision=0 WHERE id=$1 AND state='active'`, archived.ID)
			if err != nil {
				return DuplicateReport{}, err
			}
			if affected, _ := result.RowsAffected(); affected != 1 {
				return DuplicateReport{}, fmt.Errorf("note %s changed during duplicate cleanup", archived.ID)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, activeIdentityIndexSQL); err != nil {
		return DuplicateReport{}, err
	}
	if err := tx.Commit(); err != nil {
		return DuplicateReport{}, err
	}
	report.Applied = true
	report.IndexCreated = !report.IndexExists
	report.IndexExists = true
	return report, nil
}

func readDuplicateReport(ctx context.Context, q cleanupQueryer) (DuplicateReport, error) {
	report := DuplicateReport{Normalized: []NormalizedNote{}, Duplicates: []DuplicateGroup{}}
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE schemaname='groupfriend' AND indexname='friend_notes_active_identity')`).Scan(&report.IndexExists); err != nil {
		return report, err
	}
	rows, err := q.QueryContext(ctx, `SELECT id,group_id,subject,kind,key,topic_id,state,text,updated_at,evidence FROM groupfriend.notes ORDER BY group_id,subject,kind,key,topic_id,updated_at DESC,id`)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	groups := map[cleanupIdentity][]cleanupNote{}
	for rows.Next() {
		var note cleanupNote
		var evidence []byte
		if err := rows.Scan(&note.id, &note.group, &note.subject, &note.kind, &note.key, &note.topicID, &note.state, &note.text, &note.updated, &evidence); err != nil {
			return report, err
		}
		var proofs []memory.Evidence
		if err := json.Unmarshal(evidence, &proofs); err != nil {
			return report, err
		}
		note.evidence = len(proofs)
		newKey := memory.NormalizeKey(note.key)
		newTopic := memory.NormalizeTopicID(note.topicID)
		if newKey != note.key || newTopic != note.topicID {
			report.Normalized = append(report.Normalized, NormalizedNote{ID: note.id, OldKey: note.key, NewKey: newKey, OldTopicID: note.topicID, NewTopicID: newTopic})
		}
		if note.state != "active" {
			continue
		}
		identity := cleanupIdentity{group: note.group, subject: note.subject, kind: note.kind, key: newKey}
		if note.kind == "episode" {
			identity.topicID = newTopic
		}
		groups[identity] = append(groups[identity], note)
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	for identity, notes := range groups {
		if len(notes) < 2 {
			continue
		}
		sort.Slice(notes, func(i, j int) bool {
			if notes[i].updated.Equal(notes[j].updated) {
				return notes[i].id < notes[j].id
			}
			return notes[i].updated.After(notes[j].updated)
		})
		group := DuplicateGroup{GroupID: identity.group, Subject: identity.subject, Kind: identity.kind, Key: identity.key, TopicID: identity.topicID, Keep: duplicateNote(notes[0])}
		for _, note := range notes[1:] {
			group.Archive = append(group.Archive, duplicateNote(note))
		}
		report.Duplicates = append(report.Duplicates, group)
	}
	sort.Slice(report.Normalized, func(i, j int) bool { return report.Normalized[i].ID < report.Normalized[j].ID })
	sort.Slice(report.Duplicates, func(i, j int) bool {
		a, b := report.Duplicates[i], report.Duplicates[j]
		if a.GroupID != b.GroupID {
			return a.GroupID < b.GroupID
		}
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		return a.TopicID < b.TopicID
	})
	return report, nil
}

func duplicateNote(n cleanupNote) DuplicateNote {
	return DuplicateNote{ID: n.id, UpdatedAt: n.updated, Text: n.text, Evidence: n.evidence}
}
