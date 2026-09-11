CREATE TABLE IF NOT EXISTS messages (
  event_id VARCHAR(128) PRIMARY KEY,
  origin VARCHAR(16) NOT NULL DEFAULT 'inbound',
  group_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  sender_qq_nickname VARCHAR(255) NOT NULL DEFAULT '',
  sender_group_card VARCHAR(255) NOT NULL DEFAULT '',
  message_id VARCHAR(128) NOT NULL,
  reply_to_message_id VARCHAR(128) NULL,
  kind VARCHAR(32) NOT NULL,
  text_content TEXT NOT NULL,
  segments_json JSONB NOT NULL,
  attachments_json JSONB NOT NULL,
  mentioned_bot BOOLEAN NOT NULL DEFAULT FALSE,
  named_bot BOOLEAN NOT NULL DEFAULT FALSE,
  is_reply_to_bot BOOLEAN NOT NULL DEFAULT FALSE,
  occurred_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);
ALTER TABLE messages ADD COLUMN IF NOT EXISTS sender_qq_nickname VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN IF NOT EXISTS sender_group_card VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN IF NOT EXISTS origin VARCHAR(16) NOT NULL DEFAULT 'inbound';
CREATE INDEX IF NOT EXISTS idx_messages_group_occurred ON messages (group_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_messages_message_id ON messages (message_id);

CREATE TABLE IF NOT EXISTS memories (
  memory_id VARCHAR(128) PRIMARY KEY,
  scope VARCHAR(128) NOT NULL,
  type VARCHAR(64) NOT NULL,
  subject VARCHAR(255) NOT NULL,
  content TEXT NOT NULL,
  source_event_id VARCHAR(128) NOT NULL,
  source_event_ids_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  source_session_id VARCHAR(128) NOT NULL DEFAULT '',
  origin VARCHAR(32) NOT NULL DEFAULT 'agent',
  supersedes_memory_id VARCHAR(128) NOT NULL DEFAULT '',
  descriptor_ref VARCHAR(255) NOT NULL,
  confidence DOUBLE PRECISION NOT NULL,
  importance DOUBLE PRECISION NOT NULL,
  revision BIGINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL,
  expires_at TIMESTAMPTZ NULL,
  recall_count INT NOT NULL DEFAULT 0,
  last_recalled_at TIMESTAMPTZ NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_memories_scope_type ON memories (scope, type);
CREATE INDEX IF NOT EXISTS idx_memories_created ON memories (created_at);
ALTER TABLE memories ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE memories ADD COLUMN IF NOT EXISTS source_event_ids_json JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE memories ADD COLUMN IF NOT EXISTS source_session_id VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE memories ADD COLUMN IF NOT EXISTS origin VARCHAR(32) NOT NULL DEFAULT 'agent';
ALTER TABLE memories ADD COLUMN IF NOT EXISTS supersedes_memory_id VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE memories ADD COLUMN IF NOT EXISTS recall_count INT NOT NULL DEFAULT 0;
ALTER TABLE memories ADD COLUMN IF NOT EXISTS last_recalled_at TIMESTAMPTZ NULL;

CREATE TABLE IF NOT EXISTS learning_event_progress (
  event_id VARCHAR(128) NOT NULL,
  extractor_version VARCHAR(32) NOT NULL,
  group_id BIGINT NOT NULL,
  processed_at TIMESTAMPTZ NOT NULL,
  outcome VARCHAR(32) NOT NULL,
  skip_reason VARCHAR(64) NOT NULL DEFAULT '',
  memory_count INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (event_id, extractor_version)
);
CREATE INDEX IF NOT EXISTS idx_learning_progress_group ON learning_event_progress (group_id, created_at DESC);

CREATE TABLE IF NOT EXISTS member_profiles (
  group_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  nickname VARCHAR(255) NOT NULL,
  qq_nickname VARCHAR(255) NOT NULL DEFAULT '',
  group_card VARCHAR(255) NOT NULL DEFAULT '',
  message_count BIGINT NOT NULL,
  last_spoke_at TIMESTAMPTZ NOT NULL,
  active_score DOUBLE PRECISION NOT NULL,
  tags_json JSONB NOT NULL,
  common_phrases_json JSONB NOT NULL,
  interests_json JSONB NOT NULL,
  traits_json JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (group_id, user_id)
);
ALTER TABLE member_profiles ADD COLUMN IF NOT EXISTS qq_nickname VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE member_profiles ADD COLUMN IF NOT EXISTS group_card VARCHAR(255) NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS relationships (
  persona_id VARCHAR(128) NOT NULL,
  group_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  familiarity DOUBLE PRECISION NOT NULL,
  affinity DOUBLE PRECISION NOT NULL,
  tease_tolerance DOUBLE PRECISION NOT NULL,
  last_interact_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (persona_id, group_id, user_id)
);
ALTER TABLE relationships ADD COLUMN IF NOT EXISTS trust DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE relationships ADD COLUMN IF NOT EXISTS friction DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE relationships ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE relationships ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE relationships DROP COLUMN IF EXISTS grudge_score;

CREATE TABLE IF NOT EXISTS relationship_events (
  event_id VARCHAR(128) PRIMARY KEY,
  persona_id VARCHAR(128) NOT NULL,
  group_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  kind VARCHAR(64) NOT NULL,
  valence DOUBLE PRECISION NOT NULL DEFAULT 0,
  intensity DOUBLE PRECISION NOT NULL DEFAULT 0,
  reason TEXT NOT NULL DEFAULT '',
  evidence_event_id VARCHAR(128) NOT NULL DEFAULT '',
  decision_id VARCHAR(128) NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_relationship_events_subject
  ON relationship_events (persona_id, group_id, user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS relationship_history (
  id BIGSERIAL PRIMARY KEY,
  persona_id VARCHAR(128) NOT NULL,
  group_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  revision BIGINT NOT NULL,
  familiarity DOUBLE PRECISION NOT NULL,
  affinity DOUBLE PRECISION NOT NULL,
  trust DOUBLE PRECISION NOT NULL,
  tease_tolerance DOUBLE PRECISION NOT NULL,
  friction DOUBLE PRECISION NOT NULL,
  trigger_event_id VARCHAR(128) NOT NULL DEFAULT '',
  trigger_kind VARCHAR(64) NOT NULL DEFAULT '',
  snapshot_at TIMESTAMPTZ NOT NULL,
  UNIQUE (persona_id, group_id, user_id, revision)
);
CREATE INDEX IF NOT EXISTS idx_relationship_history_subject
  ON relationship_history (persona_id, group_id, user_id, snapshot_at DESC);
CREATE INDEX IF NOT EXISTS idx_relationship_history_revision
  ON relationship_history (persona_id, group_id, user_id, revision DESC);

CREATE TABLE IF NOT EXISTS group_scenes (
  group_id BIGINT PRIMARY KEY,
  scene_json JSONB NOT NULL,
  revision BIGINT NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_group_scenes_updated ON group_scenes (updated_at);

CREATE TABLE IF NOT EXISTS meme_assets (
  meme_id VARCHAR(128) PRIMARY KEY,
  group_id BIGINT NOT NULL,
  source_event_id VARCHAR(128) NOT NULL,
  object_key VARCHAR(255) NOT NULL,
  file_ext VARCHAR(32) NOT NULL,
  content_hash VARCHAR(128) NOT NULL,
  perceptual_hash VARCHAR(128) NOT NULL,
  width INT NOT NULL,
  height INT NOT NULL,
  animated BOOLEAN NOT NULL DEFAULT FALSE,
  status VARCHAR(32) NOT NULL,
  revision BIGINT NOT NULL DEFAULT 1,
  send_count BIGINT NOT NULL DEFAULT 0,
  dud_count BIGINT NOT NULL DEFAULT 0,
  last_sent_at TIMESTAMPTZ NULL,
  created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_meme_assets_group ON meme_assets (group_id);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_meme_content_hash ON meme_assets (content_hash);
ALTER TABLE meme_assets ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1;

CREATE TABLE IF NOT EXISTS meme_descriptors (
  meme_id VARCHAR(128) PRIMARY KEY,
  title VARCHAR(255) NOT NULL,
  summary TEXT NOT NULL,
  keywords_json JSONB NOT NULL,
  emotion_tags_json JSONB NOT NULL,
  scene_tags_json JSONB NOT NULL,
  usage_hints_json JSONB NOT NULL,
  language VARCHAR(32) NOT NULL,
  confidence DOUBLE PRECISION NOT NULL,
  reviewed BOOLEAN NOT NULL DEFAULT FALSE,
  updated_at TIMESTAMPTZ NOT NULL,
  CONSTRAINT fk_meme_descriptor_asset FOREIGN KEY (meme_id) REFERENCES meme_assets(meme_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS memory_claims (
  claim_id VARCHAR(128) PRIMARY KEY,
  scope VARCHAR(128) NOT NULL,
  type VARCHAR(64) NOT NULL,
  subject VARCHAR(255) NOT NULL,
  content TEXT NOT NULL,
  evidence_event_ids_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
  suggested_ttl VARCHAR(64) NOT NULL DEFAULT '',
  source VARCHAR(32) NOT NULL DEFAULT 'model',
  status VARCHAR(32) NOT NULL DEFAULT 'staged',
  supersedes_id VARCHAR(128) NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_memory_claims_scope_status ON memory_claims (scope, status, updated_at DESC);

CREATE TABLE IF NOT EXISTS group_working_memory (
  group_id BIGINT PRIMARY KEY,
  state_json JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_group_working_memory_updated ON group_working_memory (updated_at);

CREATE TABLE IF NOT EXISTS thought_records (
  thought_id VARCHAR(128) PRIMARY KEY,
  candidate_id VARCHAR(128) NOT NULL,
  group_id BIGINT NOT NULL,
  event_id VARCHAR(128) NOT NULL,
  interpretation TEXT NOT NULL,
  evidence_json JSONB NOT NULL,
  uncertainty DOUBLE PRECISION NOT NULL,
  chosen_action VARCHAR(64) NOT NULL,
  outcome VARCHAR(64) NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_thought_records_group_created ON thought_records (group_id, created_at);

CREATE TABLE IF NOT EXISTS retrieval_traces (
  trace_id VARCHAR(128) PRIMARY KEY,
  event_id VARCHAR(128) NOT NULL,
  group_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  query TEXT NOT NULL,
  candidate_count INT NOT NULL DEFAULT 0,
  hit_memory_ids_json JSONB NOT NULL,
  selected_memory_ids_json JSONB NOT NULL,
	outcome VARCHAR(64) NOT NULL DEFAULT '',
	vector_enabled BOOLEAN NOT NULL DEFAULT FALSE,
	vector_error BOOLEAN NOT NULL DEFAULT FALSE,
	lexical_ranks_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	vector_ranks_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	candidate_scores_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	latency_ms BIGINT NOT NULL DEFAULT 0,
	degraded_tracks_json JSONB NOT NULL DEFAULT '[]'::jsonb,
	selection_reason VARCHAR(255) NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL
);
ALTER TABLE retrieval_traces ADD COLUMN IF NOT EXISTS vector_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE retrieval_traces ADD COLUMN IF NOT EXISTS vector_error BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE retrieval_traces ADD COLUMN IF NOT EXISTS lexical_ranks_json JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE retrieval_traces ADD COLUMN IF NOT EXISTS vector_ranks_json JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE retrieval_traces ADD COLUMN IF NOT EXISTS candidate_scores_json JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE retrieval_traces ADD COLUMN IF NOT EXISTS latency_ms BIGINT NOT NULL DEFAULT 0;
ALTER TABLE retrieval_traces ADD COLUMN IF NOT EXISTS degraded_tracks_json JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE retrieval_traces ADD COLUMN IF NOT EXISTS selection_reason VARCHAR(255) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_retrieval_traces_group_created ON retrieval_traces (group_id, created_at);
CREATE INDEX IF NOT EXISTS idx_retrieval_traces_event ON retrieval_traces (event_id);

CREATE TABLE IF NOT EXISTS model_usage_records (
  id BIGSERIAL PRIMARY KEY,
  event_id VARCHAR(128) NOT NULL DEFAULT '',
  trace_id VARCHAR(128) NOT NULL,
  group_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  trigger VARCHAR(128) NOT NULL,
  phase VARCHAR(128) NOT NULL,
  iteration INT NOT NULL,
  input_tokens INT NOT NULL DEFAULT 0,
  cached_tokens INT NOT NULL DEFAULT 0,
  cache_miss_tokens INT NOT NULL DEFAULT 0,
  output_tokens INT NOT NULL DEFAULT 0,
  reasoning_tokens INT NOT NULL DEFAULT 0,
  duration_ms BIGINT NOT NULL DEFAULT 0,
  tools_json JSONB NOT NULL,
  tool_calls_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  usage_available BOOLEAN NOT NULL DEFAULT FALSE,
  error TEXT NOT NULL DEFAULT '',
  sent BOOLEAN NOT NULL DEFAULT FALSE,
  final_action VARCHAR(64) NOT NULL DEFAULT '',
  drop_reason VARCHAR(128) NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL
);
ALTER TABLE model_usage_records ADD COLUMN IF NOT EXISTS event_id VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE model_usage_records ADD COLUMN IF NOT EXISTS tool_calls_json JSONB NOT NULL DEFAULT '[]'::jsonb;
CREATE INDEX IF NOT EXISTS idx_model_usage_group_created ON model_usage_records (group_id, created_at);
CREATE INDEX IF NOT EXISTS idx_model_usage_trace ON model_usage_records (trace_id);
CREATE INDEX IF NOT EXISTS idx_model_usage_event ON model_usage_records (event_id);

CREATE TABLE IF NOT EXISTS runtime_mcp_config (
  config_id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (config_id = 1),
  servers_json JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS async_outbox (
  task_id VARCHAR(128) PRIMARY KEY,
  kind VARCHAR(64) NOT NULL,
  idempotency_key VARCHAR(255) NOT NULL,
  payload_json JSONB NOT NULL,
  status VARCHAR(32) NOT NULL,
  attempts INT NOT NULL DEFAULT 0,
  max_attempts INT NOT NULL DEFAULT 5,
  available_at TIMESTAMPTZ NOT NULL,
  locked_until TIMESTAMPTZ NULL,
  locked_by VARCHAR(128) NULL,
  last_error TEXT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_async_outbox_idempotency ON async_outbox (kind, idempotency_key);
CREATE INDEX IF NOT EXISTS idx_async_outbox_claim ON async_outbox (status, available_at, locked_until);
CREATE INDEX IF NOT EXISTS idx_async_outbox_updated ON async_outbox (updated_at);

-- 运行时状态(原 Redis runtime_state / persona_state 两类 key)。
-- expires_at 取代 Redis TTL:读取时 WHERE expires_at > NOW(),过期即视为不存在。
CREATE TABLE IF NOT EXISTS runtime_states (
  key VARCHAR(255) PRIMARY KEY,
  state_json JSONB NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_runtime_states_expires ON runtime_states (expires_at);

-- 人物事实采用追加式事件记录。读取时按 (fact_key, status) 选择最新的未过期值，
-- verified 是当前事实，reported 是带来源的短期转述。
CREATE TABLE IF NOT EXISTS persona_fact_events (
  fact_id VARCHAR(128) PRIMARY KEY,
  persona_id VARCHAR(128) NOT NULL,
  fact_key VARCHAR(96) NOT NULL,
  fact_value TEXT NOT NULL,
  status VARCHAR(16) NOT NULL,
  source_kind VARCHAR(32) NOT NULL,
  source_group_id BIGINT NOT NULL DEFAULT 0,
  source_user_id BIGINT NOT NULL DEFAULT 0,
  source_event_id VARCHAR(128) NOT NULL DEFAULT '',
  supersedes_fact_id VARCHAR(128) NULL,
  definition_hash VARCHAR(128) NOT NULL DEFAULT '',
  resolution_state VARCHAR(32) NOT NULL DEFAULT 'active',
  confidence DOUBLE PRECISION NOT NULL,
  effective_at TIMESTAMPTZ NOT NULL,
  expires_at TIMESTAMPTZ NULL,
  recorded_at TIMESTAMPTZ NOT NULL
);
ALTER TABLE persona_fact_events ADD COLUMN IF NOT EXISTS supersedes_fact_id VARCHAR(128) NULL;
ALTER TABLE persona_fact_events ADD COLUMN IF NOT EXISTS definition_hash VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE persona_fact_events ADD COLUMN IF NOT EXISTS resolution_state VARCHAR(32) NOT NULL DEFAULT 'active';
ALTER TABLE persona_fact_events ALTER COLUMN fact_key TYPE VARCHAR(96);
CREATE INDEX IF NOT EXISTS idx_persona_fact_current
  ON persona_fact_events (persona_id, fact_key, status, effective_at DESC, recorded_at DESC);
CREATE INDEX IF NOT EXISTS idx_persona_fact_expiry ON persona_fact_events (expires_at);

CREATE TABLE IF NOT EXISTS persona_fact_reservations (
  reservation_id VARCHAR(128) NOT NULL,
  persona_id VARCHAR(128) NOT NULL,
  fact_key VARCHAR(96) NOT NULL,
  fact_value TEXT NOT NULL,
  expected_fact_id VARCHAR(128) NOT NULL DEFAULT '',
  definition_hash VARCHAR(128) NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (reservation_id, fact_key),
  UNIQUE (persona_id, fact_key)
);
CREATE INDEX IF NOT EXISTS idx_persona_fact_reservations_expiry ON persona_fact_reservations (expires_at);

-- 使用 halfvec(2048) 保留 ark embedding-large 的完整输出；halfvec HNSW 上限为 4000 维。
CREATE TABLE IF NOT EXISTS memory_vectors (
  memory_id VARCHAR(128) PRIMARY KEY,
  content   TEXT NOT NULL,
  embedding halfvec(2048) NOT NULL,
  source_revision BIGINT NOT NULL DEFAULT 0
);
ALTER TABLE memory_vectors ADD COLUMN IF NOT EXISTS source_revision BIGINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_memory_vectors_embedding ON memory_vectors USING hnsw (embedding halfvec_cosine_ops);

CREATE TABLE IF NOT EXISTS meme_vectors (
  meme_id   VARCHAR(128) PRIMARY KEY,
  group_id  BIGINT NOT NULL,
  text      TEXT NOT NULL,
  embedding halfvec(2048) NOT NULL,
  source_revision BIGINT NOT NULL DEFAULT 0
);
ALTER TABLE meme_vectors ADD COLUMN IF NOT EXISTS source_revision BIGINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_meme_vectors_group ON meme_vectors (group_id);
CREATE INDEX IF NOT EXISTS idx_meme_vectors_embedding ON meme_vectors USING hnsw (embedding halfvec_cosine_ops);

-- 群姿态：按群隔离的人格姿态，变化慢于即时状态
CREATE TABLE IF NOT EXISTS group_persona_postures (
  persona_id VARCHAR(128) NOT NULL,
  group_id BIGINT NOT NULL,
  familiarity DOUBLE PRECISION NOT NULL DEFAULT 0.3,
  participation_bias DOUBLE PRECISION NOT NULL DEFAULT 0.0,
  humor_level DOUBLE PRECISION NOT NULL DEFAULT 0.5,
  helpfulness_bias DOUBLE PRECISION NOT NULL DEFAULT 0.6,
  formality DOUBLE PRECISION NOT NULL DEFAULT 0.4,
  trust_in_group DOUBLE PRECISION NOT NULL DEFAULT 0.5,
  preferred_topics_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  updated_at TIMESTAMPTZ NOT NULL,
  revision BIGINT NOT NULL DEFAULT 1,
  PRIMARY KEY (persona_id, group_id)
);
CREATE INDEX IF NOT EXISTS idx_group_persona_postures_group ON group_persona_postures (group_id);

-- 即时状态：按群隔离并自然衰减的即时情绪和精力
CREATE TABLE IF NOT EXISTS group_persona_ephemeral (
  persona_id VARCHAR(128) NOT NULL,
  group_id BIGINT NOT NULL,
  mood VARCHAR(32) NOT NULL DEFAULT 'steady',
  energy VARCHAR(32) NOT NULL DEFAULT 'normal',
  social_patience DOUBLE PRECISION NOT NULL DEFAULT 0.8,
  last_trigger VARCHAR(255) NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (persona_id, group_id)
);
CREATE INDEX IF NOT EXISTS idx_group_persona_ephemeral_expires ON group_persona_ephemeral (expires_at);

-- 参与决策记录：替代部分 thought_records，只记录决策和结果
CREATE TABLE IF NOT EXISTS participation_decisions (
  decision_id VARCHAR(128) PRIMARY KEY,
  group_id BIGINT NOT NULL,
  trigger_event_id VARCHAR(128) NOT NULL,
  decided_at TIMESTAMPTZ NOT NULL,
  participate BOOLEAN NOT NULL,
  reason_code VARCHAR(64) NOT NULL,
  target_user_id BIGINT,
  audience VARCHAR(32) NOT NULL DEFAULT 'group',
  intent VARCHAR(32) NOT NULL DEFAULT 'observe',
  social_value DOUBLE PRECISION NOT NULL DEFAULT 0.0,
  interruption_risk DOUBLE PRECISION NOT NULL DEFAULT 0.0,
  confidence_score DOUBLE PRECISION NOT NULL DEFAULT 0.0,
  expires_at TIMESTAMPTZ NOT NULL,
  rule_hits_json JSONB NOT NULL DEFAULT '[]'::jsonb
);
CREATE INDEX IF NOT EXISTS idx_participation_decisions_group_decided ON participation_decisions (group_id, decided_at);
CREATE INDEX IF NOT EXISTS idx_participation_decisions_event ON participation_decisions (trigger_event_id);

-- 动作反馈：发送后收集到的互动反馈
CREATE TABLE IF NOT EXISTS action_feedbacks (
  feedback_id VARCHAR(128) PRIMARY KEY,
  action_id VARCHAR(128) NOT NULL,
  decision_id VARCHAR(128) NOT NULL,
  group_id BIGINT NOT NULL,
  collected_at TIMESTAMPTZ NOT NULL,
  observed_event_ids_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  feedback_type VARCHAR(32) NOT NULL,
  signals_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  overall_sentiment DOUBLE PRECISION NOT NULL DEFAULT 0.0,
  engagement_level DOUBLE PRECISION NOT NULL DEFAULT 0.0,
  key_evidence_event_id VARCHAR(128),
  summary_note TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_action_feedbacks_action ON action_feedbacks (action_id);
CREATE INDEX IF NOT EXISTS idx_action_feedbacks_decision ON action_feedbacks (decision_id);
CREATE INDEX IF NOT EXISTS idx_action_feedbacks_group_collected ON action_feedbacks (group_id, collected_at);

-- 反馈观察窗口：跟踪正在观察的反馈窗口
CREATE TABLE IF NOT EXISTS feedback_windows (
  window_id VARCHAR(128) PRIMARY KEY,
  decision_id VARCHAR(128) NOT NULL,
  action_id VARCHAR(128) NOT NULL,
  group_id BIGINT NOT NULL,
  sent_at TIMESTAMPTZ NOT NULL,
  observe_duration_seconds INT NOT NULL,
  max_events INT NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'observing',
  closed_at TIMESTAMPTZ,
  observed_event_ids_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  feedback_type VARCHAR(32),
  feedback_note TEXT
);
CREATE INDEX IF NOT EXISTS idx_feedback_windows_status ON feedback_windows (status, sent_at);
CREATE INDEX IF NOT EXISTS idx_feedback_windows_action ON feedback_windows (action_id);
