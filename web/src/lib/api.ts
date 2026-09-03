import axios from 'axios'
import type { BotSnapshot, EventDetail, MCPServerConfig } from '@/types'

export const api = axios.create({
  baseURL: '/admin/api',
  timeout: 5000,
})

export async function getSnapshot(groupID: number, token: string) {
  const response = await api.get<BotSnapshot>('/snapshot', {
    params: groupID ? { group_id: groupID } : undefined,
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
