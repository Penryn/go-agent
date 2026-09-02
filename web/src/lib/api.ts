import axios from 'axios'
import type { BotSnapshot } from '@/types'

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
