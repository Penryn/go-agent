<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { getTasks } from '@/lib/api'
import { relativeTime } from '@/lib/format'
import { useDashboardStore } from '@/stores/dashboard'

const store = useDashboardStore()
const status = ref('')
const rows = ref<Awaited<ReturnType<typeof getTasks>>>([])
const loading = ref(false)
const counts = computed(() => rows.value.reduce<Record<string, number>>((result, row) => { result[row.status] = (result[row.status] || 0) + 1; return result }, {}))
async function load() { loading.value = true; try { rows.value = await getTasks(status.value, store.token) } finally { loading.value = false } }
onMounted(load)
</script>

<template>
  <section class="glass-panel page-panel">
    <div class="page-panel-head"><div><span>ASYNC WORK QUEUE</span><h2>任务队列</h2><p>后台任务独立展示，运行记录只保留消息与决策</p></div><div class="filters"><el-select v-model="status" clearable placeholder="全部状态" @change="load"><el-option label="待处理" value="pending" /><el-option label="运行中" value="running" /><el-option label="重试中" value="retry" /><el-option label="已完成" value="completed" /><el-option label="死信" value="dead_letter" /></el-select><el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button></div></div>
    <div class="task-summary"><span>当前 {{ rows.length }} 条</span><span>待处理 {{ counts.pending || 0 }}</span><span>重试 {{ counts.retry || 0 }}</span><span>失败 {{ counts.dead_letter || 0 }}</span></div>
    <el-table v-loading="loading" :data="rows" class="relation-table" empty-text="暂无后台任务"><el-table-column label="任务" min-width="220"><template #default="{ row }"><strong>{{ row.kind }}</strong><small class="table-sub">{{ row.id }}</small></template></el-table-column><el-table-column label="状态" width="110"><template #default="{ row }"><el-tag effect="plain">{{ row.status }}</el-tag></template></el-table-column><el-table-column label="重试" width="90"><template #default="{ row }">{{ row.attempts }} / {{ row.max_attempts }}</template></el-table-column><el-table-column label="最近更新" width="140"><template #default="{ row }">{{ relativeTime(row.updated_at) }}</template></el-table-column><el-table-column label="错误" min-width="280"><template #default="{ row }"><span class="task-error">{{ row.last_error || '—' }}</span></template></el-table-column></el-table>
  </section>
</template>
