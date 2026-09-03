import axios from 'axios'
import type { ActivityPage, BotSnapshot, EventDetail, MCPServerConfig, MemePage, MemoryPage, RelationshipPage, TaskPage } from '@/types'

export const api = axios.create({
  baseURL: '/admin/api',
  timeout: 5000,
})

export async function getSnapshot(groupID: number, token: string, windowMinutes = 1440, detail = true) {
  const response = await api.get<BotSnapshot>('/snapshot', {
    params: { ...(groupID ? { group_id: groupID } : {}), window_minutes: windowMinutes, ...(detail ? {} : { mode: 'core' }) },
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

export async function getActivity(groupID: number, windowMinutes: number, type: string, page: number, token: string) {
  const response = await api.get<ActivityPage>('/activity', { params: { group_id: groupID || undefined, window_minutes: windowMinutes, type: type === 'all' ? undefined : type, page }, headers: token ? { Authorization: `Bearer ${token}` } : undefined })
  return response.data
}

export async function getTasks(status: string, token: string, page = 1) {
  const response = await api.get<TaskPage>('/tasks', { params: { ...(status ? { status } : {}), page }, headers: token ? { Authorization: `Bearer ${token}` } : undefined })
  return response.data
}

export async function retryTask(taskID: string, token: string) {
  await api.post(`/tasks/${encodeURIComponent(taskID)}/retry`, undefined, { headers: token ? { Authorization: `Bearer ${token}` } : undefined })
}

export async function getMemories(groupID: number, status: string, type: string, query: string, page: number, token: string) {
  const response = await api.get<MemoryPage>('/memories', { params: { group_id: groupID || undefined, status, type: type || undefined, q: query.trim() || undefined, page }, headers: token ? { Authorization: `Bearer ${token}` } : undefined })
  return response.data
}

export async function getMemes(groupID: number, query: string, page: number, token: string) {
  const response = await api.get<MemePage>('/memes', {
    params: { group_id: groupID || undefined, q: query.trim() || undefined, page },
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  })
  return response.data
}

export async function getRelationships(groupID: number, query: string, page: number, token: string) {
  const response = await api.get<RelationshipPage>('/relationships', {
    params: { group_id: groupID || undefined, q: query.trim() || undefined, page },
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  })
  return response.data
}

export async function deleteMeme(memeID: string, token: string) {
  await api.delete(`/memes/${encodeURIComponent(memeID)}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  })
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
