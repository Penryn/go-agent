package postgresstore

import (
	"context"
	"database/sql"
	"encoding/json"

	feedbackdomain "github.com/phlin/go-agent/internal/domain/feedback"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
)

// PostureRepository 群姿态存储实现
type PostureRepository struct {
	db *sql.DB
}

// NewPostureRepository 创建群姿态存储
func NewPostureRepository(db *sql.DB) *PostureRepository {
	return &PostureRepository{db: db}
}

// GetGroupPosture 获取群姿态
func (r *PostureRepository) GetGroupPosture(
	ctx context.Context,
	personaID string,
	groupID int64,
) (*personadomain.GroupPosture, error) {
	query := `
		SELECT persona_id, group_id, familiarity, participation_bias, humor_level,
			helpfulness_bias, formality, trust_in_group, preferred_topics_json,
			updated_at, revision
		FROM group_persona_postures
		WHERE persona_id = $1 AND group_id = $2
	`

	var posture personadomain.GroupPosture
	var topicsJSON []byte

	err := r.db.QueryRowContext(ctx, query, personaID, groupID).Scan(
		&posture.PersonaID,
		&posture.GroupID,
		&posture.Familiarity,
		&posture.ParticipationBias,
		&posture.HumorLevel,
		&posture.HelpfulnessBias,
		&posture.Formality,
		&posture.TrustInGroup,
		&topicsJSON,
		&posture.UpdatedAt,
		&posture.Revision,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(topicsJSON, &posture.PreferredTopics); err != nil {
		return nil, err
	}

	return &posture, nil
}

// UpdateGroupPosture 更新或插入群姿态
func (r *PostureRepository) UpdateGroupPosture(
	ctx context.Context,
	posture *personadomain.GroupPosture,
) error {
	topicsJSON, err := json.Marshal(posture.PreferredTopics)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO group_persona_postures (
			persona_id, group_id, familiarity, participation_bias, humor_level,
			helpfulness_bias, formality, trust_in_group, preferred_topics_json,
			updated_at, revision
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (persona_id, group_id)
		DO UPDATE SET
			familiarity = EXCLUDED.familiarity,
			participation_bias = EXCLUDED.participation_bias,
			humor_level = EXCLUDED.humor_level,
			helpfulness_bias = EXCLUDED.helpfulness_bias,
			formality = EXCLUDED.formality,
			trust_in_group = EXCLUDED.trust_in_group,
			preferred_topics_json = EXCLUDED.preferred_topics_json,
			updated_at = EXCLUDED.updated_at,
			revision = EXCLUDED.revision
	`

	_, err = r.db.ExecContext(ctx, query,
		posture.PersonaID,
		posture.GroupID,
		posture.Familiarity,
		posture.ParticipationBias,
		posture.HumorLevel,
		posture.HelpfulnessBias,
		posture.Formality,
		posture.TrustInGroup,
		topicsJSON,
		posture.UpdatedAt,
		posture.Revision,
	)

	return err
}

// EphemeralStateRepository 即时状态存储实现
type EphemeralStateRepository struct {
	db *sql.DB
}

// NewEphemeralStateRepository 创建即时状态存储
func NewEphemeralStateRepository(db *sql.DB) *EphemeralStateRepository {
	return &EphemeralStateRepository{db: db}
}

// GetEphemeralState 获取即时状态
func (r *EphemeralStateRepository) GetEphemeralState(
	ctx context.Context,
	personaID string,
	groupID int64,
) (*personadomain.EphemeralState, error) {
	query := `
		SELECT persona_id, group_id, mood, energy, social_patience, last_trigger,
			updated_at, expires_at
		FROM group_persona_ephemeral
		WHERE persona_id = $1 AND group_id = $2 AND expires_at > NOW()
	`

	var state personadomain.EphemeralState

	err := r.db.QueryRowContext(ctx, query, personaID, groupID).Scan(
		&state.PersonaID,
		&state.GroupID,
		&state.Mood,
		&state.Energy,
		&state.SocialPatience,
		&state.LastTrigger,
		&state.UpdatedAt,
		&state.ExpiresAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &state, nil
}

// UpdateEphemeralState 更新或插入即时状态
func (r *EphemeralStateRepository) UpdateEphemeralState(
	ctx context.Context,
	state *personadomain.EphemeralState,
) error {
	query := `
		INSERT INTO group_persona_ephemeral (
			persona_id, group_id, mood, energy, social_patience, last_trigger,
			updated_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (persona_id, group_id)
		DO UPDATE SET
			mood = EXCLUDED.mood,
			energy = EXCLUDED.energy,
			social_patience = EXCLUDED.social_patience,
			last_trigger = EXCLUDED.last_trigger,
			updated_at = EXCLUDED.updated_at,
			expires_at = EXCLUDED.expires_at
	`

	_, err := r.db.ExecContext(ctx, query,
		state.PersonaID,
		state.GroupID,
		state.Mood,
		state.Energy,
		state.SocialPatience,
		state.LastTrigger,
		state.UpdatedAt,
		state.ExpiresAt,
	)

	return err
}

// DecisionRepository 决策记录存储实现
type DecisionRepository struct {
	db *sql.DB
}

// NewDecisionRepository 创建决策记录存储
func NewDecisionRepository(db *sql.DB) *DecisionRepository {
	return &DecisionRepository{db: db}
}

// SaveDecision 保存参与决策记录
func (r *DecisionRepository) SaveDecision(
	ctx context.Context,
	decision interface{}, // presencedomain.ParticipationDecision
) error {
	d, ok := decision.(*presencedomain.ParticipationDecision)
	if !ok {
		return nil // 类型不匹配，跳过
	}

	ruleHitsJSON, err := json.Marshal(d.RuleHits)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO participation_decisions (
			decision_id, group_id, trigger_event_id, decided_at,
			participate, reason_code, target_user_id, audience, intent,
			social_value, interruption_risk, confidence_score,
			expires_at, rule_hits_json
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (decision_id) DO NOTHING
	`

	_, err = r.db.ExecContext(ctx, query,
		d.DecisionID,
		d.GroupID,
		d.TriggerEventID,
		d.DecidedAt,
		d.Participate,
		d.ReasonCode,
		sql.NullInt64{Int64: d.TargetUserID, Valid: d.TargetUserID > 0},
		d.Audience,
		d.Intent,
		d.SocialValue,
		d.InterruptionRisk,
		d.ConfidenceScore,
		d.ExpiresAt,
		ruleHitsJSON,
	)

	return err
}

// FeedbackRepository 反馈存储实现
type FeedbackRepository struct {
	db *sql.DB
}

// NewFeedbackRepository 创建反馈存储
func NewFeedbackRepository(db *sql.DB) *FeedbackRepository {
	return &FeedbackRepository{db: db}
}

// SaveFeedback 保存动作反馈
func (r *FeedbackRepository) SaveFeedback(
	ctx context.Context,
	feedback interface{}, // feedbackdomain.ActionFeedback
) error {
	fb, ok := feedback.(*feedbackdomain.ActionFeedback)
	if !ok {
		return nil // 类型不匹配，跳过
	}

	eventIDsJSON, err := json.Marshal(fb.ObservedEventIDs)
	if err != nil {
		return err
	}

	signalsJSON, err := json.Marshal(fb.Signals)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO action_feedbacks (
			feedback_id, action_id, decision_id, group_id, collected_at,
			observed_event_ids_json, feedback_type, signals_json,
			overall_sentiment, engagement_level, key_evidence_event_id, summary_note
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (feedback_id) DO NOTHING
	`

	_, err = r.db.ExecContext(ctx, query,
		fb.FeedbackID,
		fb.ActionID,
		fb.DecisionID,
		fb.GroupID,
		fb.CollectedAt,
		eventIDsJSON,
		string(fb.Type),
		signalsJSON,
		fb.OverallSentiment,
		fb.EngagementLevel,
		sql.NullString{String: fb.KeyEvidenceEventID, Valid: fb.KeyEvidenceEventID != ""},
		fb.SummaryNote,
	)

	return err
}

// SaveFeedbackWindow 保存或更新反馈观察窗口
func (r *FeedbackRepository) SaveFeedbackWindow(
	ctx context.Context,
	window interface{}, // presencedomain.FeedbackWindow
) error {
	w, ok := window.(*presencedomain.FeedbackWindow)
	if !ok {
		return nil // 类型不匹配，跳过
	}

	eventIDsJSON, err := json.Marshal(w.ObservedEventIDs)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO feedback_windows (
			window_id, decision_id, action_id, group_id, sent_at,
			observe_duration_seconds, max_events, status, closed_at,
			observed_event_ids_json, feedback_type, feedback_note
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (window_id)
		DO UPDATE SET
			status = EXCLUDED.status,
			closed_at = EXCLUDED.closed_at,
			observed_event_ids_json = EXCLUDED.observed_event_ids_json,
			feedback_type = EXCLUDED.feedback_type,
			feedback_note = EXCLUDED.feedback_note
	`

	_, err = r.db.ExecContext(ctx, query,
		w.WindowID,
		w.DecisionID,
		w.ActionID,
		w.GroupID,
		w.SentAt,
		int(w.ObserveDuration.Seconds()),
		w.MaxEvents,
		w.Status,
		sql.NullTime{Time: w.ClosedAt, Valid: !w.ClosedAt.IsZero()},
		eventIDsJSON,
		sql.NullString{String: w.FeedbackType, Valid: w.FeedbackType != ""},
		sql.NullString{String: w.FeedbackNote, Valid: w.FeedbackNote != ""},
	)

	return err
}
