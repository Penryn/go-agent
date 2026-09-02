import axios from 'axios'
import { defineStore } from 'pinia'
import { computed, onScopeDispose, ref } from 'vue'
import { getSnapshot } from '@/lib/api'
import type { BotSnapshot } from '@/types'

export const useDashboardStore = defineStore('dashboard', () => {
  const snapshot = ref<BotSnapshot>()
  const selectedGroup = ref(0)
  const token = ref(sessionStorage.getItem('bot-admin-token') ?? '')
  const loading = ref(false)
  const needsToken = ref(false)
  const error = ref('')
  let timer: number | undefined

  const persona = computed(() => snapshot.value?.persona)

  async function refresh() {
    if (loading.value || document.hidden) return
    loading.value = true
    try {
      const data = await getSnapshot(selectedGroup.value, token.value)
      snapshot.value = data
      selectedGroup.value ||= data.selected_group
      needsToken.value = false
      error.value = ''
    } catch (reason) {
      if (axios.isAxiosError(reason) && reason.response?.status === 401) {
        needsToken.value = true
      } else {
        error.value = axios.isAxiosError(reason) ? reason.message : String(reason)
      }
    } finally {
      loading.value = false
    }
  }

  function setGroup(groupID: number) {
    selectedGroup.value = groupID
    void refresh()
  }

  function setToken(value: string) {
    token.value = value.trim()
    sessionStorage.setItem('bot-admin-token', token.value)
    void refresh()
  }

  function startPolling() {
    stopPolling()
    void refresh()
    timer = window.setInterval(refresh, 3000)
  }

  function stopPolling() {
    if (timer !== undefined) window.clearInterval(timer)
    timer = undefined
  }

  onScopeDispose(stopPolling)
  return { snapshot, selectedGroup, token, loading, needsToken, error, persona, refresh, setGroup, setToken, startPolling, stopPolling }
})
