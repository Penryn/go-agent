-- 维度 2000:pgvector hnsw 索引上限(hnsw 不支持超过 2000 维)。
-- ark embedding-large 输出 2048 维,超限;上层在写入前需截断/投影到 2000 维,
-- 或换 halfvec(4000 维上限)。当前按 2000 建,超出维度由 PG 报错兜底(fail-fast)。
CREATE TABLE IF NOT EXISTS memory_vectors (
  memory_id VARCHAR(128) PRIMARY KEY,
  content   TEXT NOT NULL,
  embedding vector(2000) NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_memory_vectors_embedding ON memory_vectors USING hnsw (embedding vector_cosine_ops);

CREATE TABLE IF NOT EXISTS meme_vectors (
  meme_id   VARCHAR(128) PRIMARY KEY,
  group_id  BIGINT NOT NULL,
  text      TEXT NOT NULL,
  embedding vector(2000) NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_meme_vectors_group ON meme_vectors (group_id);
CREATE INDEX IF NOT EXISTS idx_meme_vectors_embedding ON meme_vectors USING hnsw (embedding vector_cosine_ops);
