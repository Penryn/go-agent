export interface MessageImage {
  id: string;
  url: string;
  sticker: boolean;
}

export interface Message {
  seq: number;
  id: string;
  message_id: string;
  group_id: number;
  user_id: number;
  self_id: number;
  name: string;
  text: string;
  reply_to?: string;
  directed: boolean;
  outbound: boolean;
  recalled: boolean;
  at: string;
  images?: MessageImage[];
}

export interface NoteEvidence {
  event_id: string;
  quote: string;
}

export interface Note {
  id: string;
  subject: number;
  kind: string;
  key: string;
  text: string;
  evidence: NoteEvidence[];
  version: number;
  state: string;
  updated: string;
  expires?: string;
}

export interface MemoryChange {
  id?: string;
  version: number;
  subject: number;
  kind: string;
  key: string;
  text: string;
  evidence: NoteEvidence[];
  forget: boolean;
  expires?: string;
  valid_from?: string;
  valid_to?: string;
  confidence?: number;
}

export interface Reply {
  action: string;
  text: string;
  quote?: string;
  sticker_id?: string;
  reason: string;
  plan?: Record<string, unknown>;
  memory?: MemoryChange[];
}

export interface Usage {
  input: number;
  output: number;
}

export interface RetrievalTrace {
  lexical?: string[];
  semantic?: string[];
  contextual?: string[];
  selected?: string[];
  degraded?: string[];
  millis: number;
}

export interface ReplyAttempt {
  id: string;
  group_id: number;
  input_seq: number;
  reply: Reply;
  state: string;
  error?: string;
  usage: Usage;
  retrieval: RetrievalTrace;
  at: string;
}

export interface Conversation {
  messages: Message[];
  notes: Note[];
  attempts: ReplyAttempt[];
}

export interface Sticker {
  id: string;
  description: string;
  tags: string[];
  state: string;
  version: number;
}

export interface LearningLag {
  group_id: number;
  lag: number;
}

export interface LearningStats {
  runs: number;
  succeeded: number;
  failed: number;
  avg_duration_ms: number;
  avg_extract_ms: number;
  avg_reconcile_ms: number;
  extract_tokens: number;
  reconcile_tokens: number;
  rejected_count: number;
  change_count: number;
}

export interface RecallStats {
  attempted: number;
  hits: number;
  empty: number;
  cache_hits: number;
  cache_misses: number;
  semantic_degraded: number;
  max_index_load_ms: number;
  hit_rate: number;
  empty_rate: number;
  cache_hit_rate: number;
}

export interface LedgerStats {
  confirmed_attempts: number;
  usage_rows: number;
  write_failures: number;
  write_retries: number;
}

export interface NoteStats {
  active_notes: number;
  total_notes: number;
  active_ratio: number;
  active_identity_index: boolean;
  duplicate_identities: number;
  total_identities: number;
  duplicate_rate: number;
  forgotten_rebuilds: number;
  forgotten_rebuild_rate: number;
}

export interface MemoryStatus {
  window_hours: number;
  groups: LearningLag[];
  learning: LearningStats;
  recall: RecallStats;
  ledger: LedgerStats;
  notes: NoteStats;
  pending_embeddings: number;
  usage_projection_diff: number;
}

export interface UpdateResponse {
  ok: boolean;
}

export type AdminView = "conversation" | "notes" | "attempts" | "memory" | "stickers";

export type NoticeTone = "info" | "success" | "error";

export interface AdminNotice {
  tone: NoticeTone;
  message: string;
}
