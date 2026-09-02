<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { useRoute } from 'vue-router'
import { ChatDotRound, Connection, DataAnalysis, Memo, Refresh, User } from '@element-plus/icons-vue'
import { useDashboardStore } from '@/stores/dashboard'

const store = useDashboardStore()
const { snapshot, selectedGroup, loading, needsToken, error } = storeToRefs(store)
const route = useRoute()
const tokenInput = ref('')
const pageTitle = computed(() => String(route.meta.title ?? '实时概览'))

const nav = [
  { to: '/', label: '实时概览', icon: DataAnalysis },
  { to: '/memory', label: '长期记忆', icon: Memo },
  { to: '/relations', label: '群友关系', icon: User },
  { to: '/activity', label: '运行记录', icon: ChatDotRound },
]

function saveToken() {
  if (tokenInput.value.trim()) store.setToken(tokenInput.value)
}

onMounted(store.startPolling)
onUnmounted(store.stopPolling)
</script>

<template>
  <div class="app-shell">
    <aside class="sidebar">
      <div class="brand-lockup">
        <div class="brand-mark">{{ snapshot?.persona.name?.slice(0, 1) || '芙' }}</div>
        <div>
          <span>FUFU</span>
          <strong>Observatory</strong>
        </div>
      </div>

      <nav class="nav-list" aria-label="后台导航">
        <RouterLink v-for="item in nav" :key="item.to" :to="item.to" class="nav-item">
          <el-icon><component :is="item.icon" /></el-icon>
          <span>{{ item.label }}</span>
        </RouterLink>
      </nav>

      <div class="sidebar-status">
        <div class="status-orbit"><i /></div>
        <div>
          <strong>{{ snapshot?.status.qq_enabled ? 'Bot 运行中' : 'Bot 未连接' }}</strong>
          <span>{{ snapshot?.status.mode || 'loading' }} · {{ snapshot?.status.self_id || '—' }}</span>
        </div>
      </div>
    </aside>

    <main class="main-stage">
      <header class="topbar">
        <div>
          <span class="page-kicker">LIVE CONSOLE</span>
          <h1>{{ pageTitle }}</h1>
        </div>
        <div class="topbar-tools">
          <div class="updated"><i /><span>{{ error || (loading ? '同步中' : '每 3 秒自动更新') }}</span></div>
          <el-select
            :model-value="selectedGroup"
            class="group-select"
            placeholder="选择群聊"
            aria-label="选择群聊"
            @change="store.setGroup"
          >
            <el-option
              v-for="group in snapshot?.groups || []"
              :key="group.group_id"
              :label="`群 ${group.group_id} · ${group.members} 人`"
              :value="group.group_id"
            />
          </el-select>
          <el-button :icon="Refresh" circle :loading="loading" aria-label="立即刷新" @click="store.refresh" />
        </div>
      </header>

      <RouterView v-slot="{ Component }">
        <Transition name="page" mode="out-in">
          <component :is="Component" />
        </Transition>
      </RouterView>
    </main>

    <el-dialog
      v-model="needsToken"
      width="min(420px, calc(100vw - 32px))"
      :close-on-click-modal="false"
      :close-on-press-escape="false"
      :show-close="false"
      align-center
    >
      <template #header>
        <div class="token-heading"><el-icon><Connection /></el-icon><div><strong>连接观测台</strong><span>输入后台访问令牌</span></div></div>
      </template>
      <el-input v-model="tokenInput" type="password" show-password size="large" placeholder="server.admin_token" @keyup.enter="saveToken" />
      <template #footer><el-button type="primary" size="large" class="w-full" @click="saveToken">进入后台</el-button></template>
    </el-dialog>
  </div>
</template>
