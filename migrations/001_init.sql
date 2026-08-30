CREATE TABLE IF NOT EXISTS messages (
  event_id VARCHAR(128) PRIMARY KEY,
  group_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  message_id VARCHAR(128) NOT NULL,
  reply_to_message_id VARCHAR(128) NULL,
  kind VARCHAR(32) NOT NULL,
  text_content TEXT NOT NULL,
  segments_json JSON NOT NULL,
  attachments_json JSON NOT NULL,
  mentioned_bot BOOLEAN NOT NULL DEFAULT FALSE,
  named_bot BOOLEAN NOT NULL DEFAULT FALSE,
  is_reply_to_bot BOOLEAN NOT NULL DEFAULT FALSE,
  occurred_at DATETIME(6) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  KEY idx_messages_group_occurred (group_id, occurred_at),
  KEY idx_messages_message_id (message_id)
);

CREATE TABLE IF NOT EXISTS memories (
  memory_id VARCHAR(128) PRIMARY KEY,
  scope VARCHAR(128) NOT NULL,
  type VARCHAR(64) NOT NULL,
  subject VARCHAR(255) NOT NULL,
  content TEXT NOT NULL,
  source_event_id VARCHAR(128) NOT NULL,
  descriptor_ref VARCHAR(255) NOT NULL,
  confidence DOUBLE NOT NULL,
  importance DOUBLE NOT NULL,
  created_at DATETIME(6) NOT NULL,
  expires_at DATETIME(6) NULL,
  updated_at DATETIME(6) NOT NULL,
  KEY idx_memories_scope_type (scope, type),
  KEY idx_memories_created (created_at)
);

CREATE TABLE IF NOT EXISTS member_profiles (
  group_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  nickname VARCHAR(255) NOT NULL,
  message_count BIGINT NOT NULL,
  last_spoke_at DATETIME(6) NOT NULL,
  active_score DOUBLE NOT NULL,
  tags_json JSON NOT NULL,
  common_phrases_json JSON NOT NULL,
  interests_json JSON NOT NULL,
  traits_json JSON NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (group_id, user_id)
);

CREATE TABLE IF NOT EXISTS relationships (
  persona_id VARCHAR(128) NOT NULL,
  group_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  familiarity DOUBLE NOT NULL,
  affinity DOUBLE NOT NULL,
  tease_tolerance DOUBLE NOT NULL,
  grudge_score DOUBLE NOT NULL,
  last_interact_at DATETIME(6) NOT NULL,
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
  send_count BIGINT NOT NULL DEFAULT 0,
  last_sent_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  KEY idx_meme_assets_group (group_id),
  UNIQUE KEY uniq_meme_content_hash (content_hash)
);

CREATE TABLE IF NOT EXISTS meme_descriptors (
  meme_id VARCHAR(128) PRIMARY KEY,
  title VARCHAR(255) NOT NULL,
  summary TEXT NOT NULL,
  keywords_json JSON NOT NULL,
  emotion_tags_json JSON NOT NULL,
  scene_tags_json JSON NOT NULL,
  usage_hints_json JSON NOT NULL,
  language VARCHAR(32) NOT NULL,
  confidence DOUBLE NOT NULL,
  reviewed BOOLEAN NOT NULL DEFAULT FALSE,
  updated_at DATETIME(6) NOT NULL,
  CONSTRAINT fk_meme_descriptor_asset FOREIGN KEY (meme_id) REFERENCES meme_assets(meme_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS learning_candidates (
  id VARCHAR(128) PRIMARY KEY,
  group_id BIGINT NOT NULL,
  kind VARCHAR(64) NOT NULL,
  value TEXT NOT NULL,
  meaning TEXT NOT NULL,
  evidence_count INT NOT NULL,
  example_event_ids_json JSON NOT NULL,
  confidence DOUBLE NOT NULL,
  status VARCHAR(32) NOT NULL,
  created_at DATETIME(6) NOT NULL
);

CREATE TABLE IF NOT EXISTS learning_watermarks (
  group_id BIGINT NOT NULL,
  kind VARCHAR(64) NOT NULL,
  occurred_at DATETIME(6) NOT NULL,
  event_id VARCHAR(128) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (group_id, kind)
);
