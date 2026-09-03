<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { useRoute } from 'vue-router'
import { ChatDotRound, Connection, DataAnalysis, Memo, Refresh, Setting, TrendCharts, User, List, Picture } from '@element-plus/icons-vue'
import { useDashboardStore } from '@/stores/dashboard'

const store = useDashboardStore()
const { snapshot, selectedGroup, loading, needsToken, error } = storeToRefs(store)
const route = useRoute()
const tokenInput = ref('')
const pageTitle = computed(() => String(route.meta.title ?? '实时概览'))
const detailedSnapshotRoutes = new Set(['overview', 'monitoring'])

const navSections = [
  { label: '运营', items: [{ to: '/', label: '实时概览', icon: DataAnalysis }, { to: '/activity', label: '运行记录', icon: ChatDotRound }, { to: '/tasks', label: '任务队列', icon: List }] },
  { label: '知识', items: [{ to: '/memory', label: '长期记忆', icon: Memo }, { to: '/relations', label: '群友关系', icon: User }, { to: '/memes', label: '表情包库', icon: Picture }] },
  { label: '观测', items: [{ to: '/monitoring', label: '监控指标', icon: TrendCharts }] },
  { label: '配置', items: [{ to: '/mcp', label: 'MCP 工具', icon: Setting }] },
]

function saveToken() {
  if (tokenInput.value.trim()) store.setToken(tokenInput.value)
}

onMounted(store.startPolling)
onUnmounted(store.stopPolling)
watch(() => route.name, (name) => store.setSnapshotMode(detailedSnapshotRoutes.has(String(name))), { immediate: true })
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
        <section v-for="section in navSections" :key="section.label" class="nav-section">
          <span class="nav-section-label">{{ section.label }}</span>
          <RouterLink v-for="item in section.items" :key="item.to" :to="item.to" class="nav-item">
            <el-icon><component :is="item.icon" /></el-icon>
            <span>{{ item.label }}</span>
          </RouterLink>
        </section>
      </nav>

      <div class="sidebar-status">
        <div class="status-orbit" :data-online="snapshot?.status.qq_connected === true"><i /></div>
        <div>
          <strong>{{ snapshot?.status.qq_connected ? 'Bot 运行中' : 'Bot 未连接' }}</strong>
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

      <RouterView />
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
