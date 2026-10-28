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

export interface UpdateResponse {
  ok: boolean;
}
