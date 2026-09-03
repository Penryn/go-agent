export interface BotSnapshot {
  updated_at: string
  window_minutes: number
  window_metrics: { decisions: number; action_decisions: number; replies: number; tasks: number; failed_tasks: number }
  selected_group: number
  status: { mode: string; qq_enabled: boolean; qq_connected: boolean; self_id: number; database_ok: boolean; queue_backlog: number; last_error_at?: string; main_model_status: string; vector_search_status: string }
  stats: { groups: number; members: number; memories: number; pending_tasks: number }
  persona: {
    id: string
    name: string
    description: string
    mood: string
    energy: string
    talk_bias: number
    runtime: {
      state: string
      cooldown_until: string
      last_bot_speak_at: string
      replies_last_10min: number
    }
    facts: PersonaFact[]
    interests: string[]
  }
  groups: GroupSummary[]
  memories: MemoryRecord[]
  relationships: Relationship[]
  activity: Activity[]
  retrieval: {
    queries: number
    queries_with_hits: number
    hit_rate: number
    avg_candidate_count: number
    result_recorded_queries: number
    selected_queries: number
    selection_rate: number
  }
  model_usage: { calls: number; input_tokens: number; output_tokens: number; avg_duration_ms: number; error_calls: number }
}

export interface MCPServerConfig {
  name: string
  enabled: boolean
  required: boolean
  transport: 'stdio' | 'http'
  command: string
  args: string[]
  url: string
  tools: string[]
  timeout: string
}

export interface PersonaFact {
  fact_id: string
  key: string
  value: string
  status: string
  source_kind: string
  confidence: number
  recorded_at: string
}

export interface GroupSummary {
  group_id: number
  messages: number
  members: number
  active_topic: string
  last_activity: string
}

export interface MemoryRecord {
  id: string
  scope: string
  type: string
  subject: string
  content: string
  confidence: number
  importance: number
  created_at: string
  expires_at?: string
  source_event_id: string
  updated_at: string
}

export interface MemeRecord {
  meme_id: string
  group_id: number
  source_event_id: string
  object_key: string
  file_ext: string
  preview_url: string
  width: number
  height: number
  animated: boolean
  status: string
  send_count: number
  dud_count: number
  created_at: string
  last_sent_at?: string
  title: string
  summary: string
  keywords: string[]
  emotion_tags: string[]
  scene_tags: string[]
  confidence: number
  reviewed: boolean
}

export interface TaskRecord {
  id: string; kind: string; status: string; attempts: number; max_attempts: number
  available_at: string; last_error: string; created_at: string; updated_at: string
}

export interface TaskPage {
  items: TaskRecord[]
  total: number
  page: number
  page_size: number
}

export interface Relationship {
  group_id: number
  user_id: number
  name: string
  affinity: number
  familiarity: number
  tease_tolerance: number
  grudge_score: number
  message_count: number
  last_interact_at: string
}

export interface Activity {
  event_id: string
  at: string
  group_id: number
  type: 'message' | 'decision'
  label: string
  subject: string
  detail: string
}

export interface ActivityPage {
  items: Activity[]
  total: number
  page: number
  page_size: number
}

export interface EventDetail {
  event_id: string
  message_id: string
  group_id: number
  user_id: number
  kind: string
  text: string
  sender: string
  occurred_at: string
  duration_ms: number
  decision?: { thought_id: string; action: string; outcome: string; interpretation: string; evidence: string[]; uncertainty: number; created_at: string }
  retrievals: { trace_id: string; query: string; candidate_count: number; hit_memory_ids: string[]; selected_ids: string[]; outcome: string; created_at: string }[]
  model_usages: { trace_id: string; iteration: number; input_tokens: number; output_tokens: number; duration_ms: number; tools: string[]; usage_available: boolean; error: string; sent: boolean; final_action: string; drop_reason: string; created_at: string }[]
}
