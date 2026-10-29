CREATE EXTENSION IF NOT EXISTS vector;
CREATE SCHEMA IF NOT EXISTS groupfriend;
CREATE TABLE IF NOT EXISTS groupfriend.settings (key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS groupfriend.messages (
 seq BIGSERIAL PRIMARY KEY,event_id TEXT UNIQUE NOT NULL,group_id BIGINT NOT NULL,user_id BIGINT NOT NULL,
 message_id TEXT NOT NULL DEFAULT '',outbound BOOLEAN NOT NULL DEFAULT FALSE,recalled BOOLEAN NOT NULL DEFAULT FALSE,
 event_at TIMESTAMPTZ NOT NULL,body JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS friend_messages_group_seq ON groupfriend.messages(group_id,seq DESC);
CREATE INDEX IF NOT EXISTS friend_messages_platform ON groupfriend.messages(group_id,message_id);
CREATE TABLE IF NOT EXISTS groupfriend.group_progress (
 group_id BIGINT PRIMARY KEY,after_seq BIGINT NOT NULL DEFAULT 0,summary_after_seq BIGINT NOT NULL DEFAULT 0,retry_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE groupfriend.group_progress ADD COLUMN IF NOT EXISTS summary_after_seq BIGINT NOT NULL DEFAULT 0;
ALTER TABLE groupfriend.group_progress ADD COLUMN IF NOT EXISTS summary_retry_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE TABLE IF NOT EXISTS groupfriend.notes (
 id TEXT PRIMARY KEY,group_id BIGINT NOT NULL,subject BIGINT NOT NULL,kind TEXT NOT NULL,key TEXT NOT NULL,
 text TEXT NOT NULL,evidence JSONB NOT NULL,revision BIGINT NOT NULL,source_seq BIGINT NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('active','forgotten')),updated_at TIMESTAMPTZ NOT NULL,
 expires_at TIMESTAMPTZ,embedding halfvec(__DIM__),indexed_revision BIGINT NOT NULL DEFAULT 0,embedding_model TEXT NOT NULL DEFAULT ''
);
ALTER TABLE groupfriend.notes ADD COLUMN IF NOT EXISTS salience SMALLINT NOT NULL DEFAULT 50;
ALTER TABLE groupfriend.notes ADD COLUMN IF NOT EXISTS confidence REAL NOT NULL DEFAULT 1;
ALTER TABLE groupfriend.notes ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ;
ALTER TABLE groupfriend.notes ADD COLUMN IF NOT EXISTS use_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE groupfriend.notes ADD COLUMN IF NOT EXISTS valid_from TIMESTAMPTZ;
ALTER TABLE groupfriend.notes ADD COLUMN IF NOT EXISTS valid_to TIMESTAMPTZ;
ALTER TABLE groupfriend.notes ADD COLUMN IF NOT EXISTS memory_revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE groupfriend.notes ADD COLUMN IF NOT EXISTS topic_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS friend_notes_scope ON groupfriend.notes(group_id,subject,state);
CREATE INDEX IF NOT EXISTS friend_notes_topic ON groupfriend.notes(group_id,topic_id,kind,state);
CREATE INDEX IF NOT EXISTS friend_notes_vector ON groupfriend.notes USING hnsw(embedding halfvec_cosine_ops);
CREATE TABLE IF NOT EXISTS groupfriend.conversation_topics (
 group_id BIGINT NOT NULL,topic_id TEXT NOT NULL,status TEXT NOT NULL,
 participants JSONB NOT NULL DEFAULT '[]',first_seq BIGINT NOT NULL,last_seq BIGINT NOT NULL,
 revision BIGINT NOT NULL DEFAULT 1,last_active_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(group_id,topic_id)
);
CREATE TABLE IF NOT EXISTS groupfriend.topic_summaries (
 group_id BIGINT NOT NULL,topic_id TEXT NOT NULL,revision BIGINT NOT NULL,
 summary TEXT NOT NULL,evidence JSONB NOT NULL DEFAULT '[]',source_seq BIGINT NOT NULL,
 embedding halfvec(__DIM__),embedding_model TEXT NOT NULL DEFAULT '',updated_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(group_id,topic_id,revision)
);
CREATE INDEX IF NOT EXISTS friend_topic_summary_scope ON groupfriend.topic_summaries(group_id,topic_id,revision DESC);
-- Older builds advanced the summary cursor without generating summaries.
UPDATE groupfriend.group_progress p SET summary_after_seq=0 WHERE summary_after_seq>0 AND NOT EXISTS (SELECT 1 FROM groupfriend.topic_summaries s WHERE s.group_id=p.group_id);
CREATE TABLE IF NOT EXISTS groupfriend.conversation_topic_events (
 group_id BIGINT NOT NULL,topic_id TEXT NOT NULL,event_id TEXT NOT NULL,
 message_id TEXT NOT NULL DEFAULT '',seq BIGINT NOT NULL,assigned_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(group_id,topic_id,event_id)
);
CREATE INDEX IF NOT EXISTS friend_topic_events_recent ON groupfriend.conversation_topic_events(group_id,seq DESC);
CREATE TABLE IF NOT EXISTS groupfriend.stickers (
 id TEXT PRIMARY KEY,content_hash TEXT UNIQUE,source_event TEXT NOT NULL,source_group BIGINT NOT NULL,
 url TEXT NOT NULL,path TEXT NOT NULL DEFAULT '',mime TEXT NOT NULL DEFAULT '',size BIGINT NOT NULL DEFAULT 0,
 platform_sticker BOOLEAN NOT NULL DEFAULT FALSE,description TEXT NOT NULL DEFAULT '',tags JSONB NOT NULL DEFAULT '[]',
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','ready','rejected','blocked','evicted')),
 revision BIGINT NOT NULL DEFAULT 1,last_seen TIMESTAMPTZ NOT NULL,retry_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 attempts INTEGER NOT NULL DEFAULT 0,last_error TEXT NOT NULL DEFAULT '',
 embedding halfvec(__DIM__),indexed_revision BIGINT NOT NULL DEFAULT 0,embedding_model TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS friend_sticker_vector ON groupfriend.stickers USING hnsw(embedding halfvec_cosine_ops);
CREATE TABLE IF NOT EXISTS groupfriend.reply_attempts (
 id TEXT PRIMARY KEY,group_id BIGINT NOT NULL,input_seq BIGINT NOT NULL,state TEXT NOT NULL,
 sticker_id TEXT NOT NULL DEFAULT '',at TIMESTAMPTZ NOT NULL,updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),body JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS friend_attempt_group_time ON groupfriend.reply_attempts(group_id,updated_at DESC);
CREATE INDEX IF NOT EXISTS friend_attempt_sticker ON groupfriend.reply_attempts(group_id,sticker_id,updated_at DESC) WHERE state='confirmed';
