export interface BotSnapshot {
  updated_at: string
  selected_group: number
  status: { mode: string; qq_enabled: boolean; self_id: number }
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
  at: string
  group_id: number
  type: 'message' | 'decision' | 'task'
  label: string
  subject: string
  detail: string
}
