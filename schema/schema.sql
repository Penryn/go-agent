CREATE TABLE IF NOT EXISTS messages (
  event_id VARCHAR(128) PRIMARY KEY,
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
CREATE INDEX IF NOT EXISTS idx_messages_group_occurred ON messages (group_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_messages_message_id ON messages (message_id);

CREATE TABLE IF NOT EXISTS memories (
  memory_id VARCHAR(128) PRIMARY KEY,
  scope VARCHAR(128) NOT NULL,
  type VARCHAR(64) NOT NULL,
  subject VARCHAR(255) NOT NULL,
  content TEXT NOT NULL,
  source_event_id VARCHAR(128) NOT NULL,
  descriptor_ref VARCHAR(255) NOT NULL,
  confidence DOUBLE PRECISION NOT NULL,
  importance DOUBLE PRECISION NOT NULL,
  revision BIGINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL,
  expires_at TIMESTAMPTZ NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_memories_scope_type ON memories (scope, type);
CREATE INDEX IF NOT EXISTS idx_memories_created ON memories (created_at);
ALTER TABLE memories ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1;

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
  grudge_score DOUBLE PRECISION NOT NULL,
  last_interact_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (persona_id, group_id, user_id)
);

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

CREATE TABLE IF NOT EXISTS learning_candidates (
  id VARCHAR(128) PRIMARY KEY,
  group_id BIGINT NOT NULL,
  kind VARCHAR(64) NOT NULL,
  value TEXT NOT NULL,
  meaning TEXT NOT NULL,
  evidence_count INT NOT NULL,
  example_event_ids_json JSONB NOT NULL,
  confidence DOUBLE PRECISION NOT NULL,
  status VARCHAR(32) NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS learning_watermarks (
  group_id BIGINT NOT NULL,
  kind VARCHAR(64) NOT NULL,
  occurred_at TIMESTAMPTZ NOT NULL,
  event_id VARCHAR(128) NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (group_id, kind)
);

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
  created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_retrieval_traces_group_created ON retrieval_traces (group_id, created_at);
CREATE INDEX IF NOT EXISTS idx_retrieval_traces_event ON retrieval_traces (event_id);

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
