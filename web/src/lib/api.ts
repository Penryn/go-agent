import axios from 'axios'
import type { BotSnapshot, EventDetail, MCPServerConfig, TaskRecord } from '@/types'
import type { MemeRecord } from '@/types'

export const api = axios.create({
  baseURL: '/admin/api',
  timeout: 5000,
})

export async function getSnapshot(groupID: number, token: string, windowMinutes = 1440) {
  const response = await api.get<BotSnapshot>('/snapshot', {
    params: { ...(groupID ? { group_id: groupID } : {}), window_minutes: windowMinutes },
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  })
  return response.data
}

export async function getEventDetail(eventID: string, token: string) {
  const response = await api.get<EventDetail>(`/events/${encodeURIComponent(eventID)}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  })
  return response.data
}

export async function getTasks(status: string, token: string) {
  const response = await api.get<TaskRecord[]>('/tasks', { params: status ? { status } : undefined, headers: token ? { Authorization: `Bearer ${token}` } : undefined })
  return response.data
}

export async function getMemes(groupID: number, query: string, token: string) {
  const response = await api.get<MemeRecord[]>('/memes', {
    params: { group_id: groupID || undefined, q: query.trim() || undefined },
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  })
  return response.data
}

export async function getMCPConfig(token: string) {
  const response = await api.get<{ servers: MCPServerConfig[] }>('/mcp', {
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  })
  return response.data
}

export async function updateMCPConfig(servers: MCPServerConfig[], token: string) {
  const response = await api.put<{ servers: MCPServerConfig[] }>('/mcp', { servers }, {
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    timeout: 25000,
  })
  return response.data
}
