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
 group_id BIGINT PRIMARY KEY,after_seq BIGINT NOT NULL DEFAULT 0,retry_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS groupfriend.notes (
 id TEXT PRIMARY KEY,group_id BIGINT NOT NULL,subject BIGINT NOT NULL,kind TEXT NOT NULL,key TEXT NOT NULL,
 text TEXT NOT NULL,evidence JSONB NOT NULL,revision BIGINT NOT NULL,source_seq BIGINT NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('active','forgotten')),updated_at TIMESTAMPTZ NOT NULL,
 expires_at TIMESTAMPTZ,embedding halfvec(__DIM__),indexed_revision BIGINT NOT NULL DEFAULT 0,embedding_model TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS friend_notes_scope ON groupfriend.notes(group_id,subject,state);
CREATE INDEX IF NOT EXISTS friend_notes_vector ON groupfriend.notes USING hnsw(embedding halfvec_cosine_ops);
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
