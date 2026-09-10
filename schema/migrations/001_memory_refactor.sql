-- 记忆与学习系统重构 - Schema 变更
-- 状态：待执行

-- ============================================================================
-- 1. 重构 memories 表为权威记忆数据
-- ============================================================================

-- 备份现有表（实际执行时手动操作）
-- CREATE TABLE memories_old AS SELECT * FROM memories;

-- 删除旧表并重建
DROP TABLE IF EXISTS memories CASCADE;

CREATE TABLE memories (
  -- 身份
  memory_id VARCHAR(128) PRIMARY KEY,
  scope VARCHAR(128) NOT NULL,                    -- 可见范围（默认为来源群）
  subject_kind VARCHAR(32) NOT NULL,              -- 主体类型：user/group/bot
  subject_id VARCHAR(128) NOT NULL,               -- 主体ID（user_id/group_id/persona_id）
  type VARCHAR(64) NOT NULL,                      -- semantic/social/episodic
  subtype VARCHAR(64) NOT NULL DEFAULT '',        -- 细分类型

  -- 内容
  content TEXT NOT NULL,                          -- 自然语言内容
  predicate VARCHAR(64) NOT NULL DEFAULT '',      -- 结构化事实键（如 preferred_name）
  normalized_value TEXT NOT NULL DEFAULT '',      -- 规范化值
  qualifier VARCHAR(128) NOT NULL DEFAULT 'default', -- 限定符（default 或时间区间）

  -- 经历特有字段
  participant_ids_json JSONB NOT NULL DEFAULT '[]'::jsonb,  -- episodic 参与者
  bot_role VARCHAR(32) NOT NULL DEFAULT '',                 -- participant/observer
  anchor_event_id VARCHAR(128) NOT NULL DEFAULT '',         -- 稳定原事件锚点

  -- 状态
  status VARCHAR(32) NOT NULL DEFAULT 'active',   -- active/pending/superseded/expired/revoked/rejected
  revision BIGINT NOT NULL DEFAULT 1,
  supersedes_id VARCHAR(128) NOT NULL DEFAULT '', -- 替代的旧记忆ID

  -- 时间
  first_observed_at TIMESTAMPTZ NOT NULL,
  last_observed_at TIMESTAMPTZ NOT NULL,
  valid_until TIMESTAMPTZ NULL,                   -- 临时边界失效时间
  pending_until TIMESTAMPTZ NULL,                 -- pending 到期时间

  -- 来源
  source_kind VARCHAR(32) NOT NULL,               -- 'extraction'/'correction'/'manual'
  extractor_version VARCHAR(32) NOT NULL DEFAULT 'v1',

  -- 元数据
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

-- 索引
CREATE INDEX idx_memories_scope_status ON memories (scope, status);
CREATE INDEX idx_memories_subject ON memories (subject_kind, subject_id, status);
CREATE INDEX idx_memories_type ON memories (type, subtype, status);
CREATE INDEX idx_memories_participants ON memories USING GIN (participant_ids_json);
CREATE INDEX idx_memories_valid_until ON memories (valid_until) WHERE valid_until IS NOT NULL;
CREATE INDEX idx_memories_pending_until ON memories (pending_until) WHERE pending_until IS NOT NULL;
CREATE INDEX idx_memories_anchor ON memories (anchor_event_id) WHERE anchor_event_id != '';

-- 唯一约束：单值事实键
CREATE UNIQUE INDEX uniq_memories_fact_key ON memories (
  scope, subject_kind, subject_id, predicate, qualifier
) WHERE status = 'active' AND predicate != '';

-- ============================================================================
-- 2. 新增 memory_evidence 表
-- ============================================================================

CREATE TABLE IF NOT EXISTS memory_evidence (
  memory_id VARCHAR(128) NOT NULL,
  event_id VARCHAR(128) NOT NULL,
  source_role VARCHAR(32) NOT NULL DEFAULT 'primary',  -- primary/context/reference
  added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (memory_id, event_id)
);

CREATE INDEX idx_memory_evidence_event ON memory_evidence (event_id);
CREATE INDEX idx_memory_evidence_memory ON memory_evidence (memory_id, added_at);

-- ============================================================================
-- 3. 新增 memory_changes 表
-- ============================================================================

CREATE TABLE IF NOT EXISTS memory_changes (
  change_id VARCHAR(128) PRIMARY KEY,
  memory_id VARCHAR(128) NOT NULL,
  change_kind VARCHAR(32) NOT NULL,              -- created/updated/superseded/expired/revoked
  reason TEXT NOT NULL DEFAULT '',
  operator_kind VARCHAR(32) NOT NULL DEFAULT '', -- system/user/correction
  operator_id VARCHAR(128) NOT NULL DEFAULT '',
  from_revision BIGINT NOT NULL DEFAULT 0,
  to_revision BIGINT NOT NULL,
  field_diffs_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_memory_changes_memory ON memory_changes (memory_id, created_at DESC);
CREATE INDEX idx_memory_changes_kind ON memory_changes (change_kind, created_at DESC);

-- ============================================================================
-- 4. 新增 learning_event_progress 表（替代 learning_watermarks）
-- ============================================================================

CREATE TABLE IF NOT EXISTS learning_event_progress (
  event_id VARCHAR(128) NOT NULL,
  extractor_version VARCHAR(32) NOT NULL,
  group_id BIGINT NOT NULL,
  processed_at TIMESTAMPTZ NOT NULL,
  outcome VARCHAR(32) NOT NULL,                   -- completed/skipped
  skip_reason VARCHAR(64) NOT NULL DEFAULT '',    -- policy_excluded/already_forgotten/etc
  memory_count INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (event_id, extractor_version)
);

CREATE INDEX idx_learning_progress_group ON learning_event_progress (group_id, created_at DESC);
CREATE INDEX idx_learning_progress_outcome ON learning_event_progress (outcome);

-- ============================================================================
-- 5. 更新 memory_vectors 表（复用，调整索引）
-- ============================================================================

-- memory_vectors 表结构保持，只需确保 source_revision 存在
ALTER TABLE memory_vectors ADD COLUMN IF NOT EXISTS source_revision BIGINT NOT NULL DEFAULT 0;

-- ============================================================================
-- 6. 更新 retrieval_traces 表（添加 included 字段）
-- ============================================================================

ALTER TABLE retrieval_traces
  ADD COLUMN IF NOT EXISTS retrieved_ids_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN IF NOT EXISTS included_ids_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN IF NOT EXISTS included_revisions_json JSONB NOT NULL DEFAULT '{}'::jsonb;

-- ============================================================================
-- 7. 标记待删除的旧表（实际执行时逐步清理）
-- ============================================================================

-- 以下表将在重构完成后删除：
-- - learning_candidates
-- - learning_candidate_evidence
-- - memory_claims
-- - learning_watermarks

-- 暂时保留，添加注释
COMMENT ON TABLE learning_candidates IS 'DEPRECATED: Will be removed after memory refactor';
COMMENT ON TABLE learning_candidate_evidence IS 'DEPRECATED: Will be removed after memory refactor';
COMMENT ON TABLE memory_claims IS 'DEPRECATED: Will be removed after memory refactor';
COMMENT ON TABLE learning_watermarks IS 'DEPRECATED: Will be removed after memory refactor';
