<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getTasks, retryTask } from '@/lib/api'
import { relativeTime } from '@/lib/format'
import { useDashboardStore } from '@/stores/dashboard'

const store = useDashboardStore()
const status = ref('')
const rows = ref<Awaited<ReturnType<typeof getTasks>>['items']>([])
const total = ref(0)
const page = ref(1)
const loading = ref(false)
const retrying = ref('')
const counts = computed(() => rows.value.reduce<Record<string, number>>((result, row) => { result[row.status] = (result[row.status] || 0) + 1; return result }, {}))
async function load(nextPage = page.value) { loading.value = true; page.value = nextPage; try { const result = await getTasks(status.value, store.token, page.value); rows.value = result.items; total.value = result.total } finally { loading.value = false } }
async function retry(taskID: string) {
  try {
    await ElMessageBox.confirm('重新入队后会从头执行该任务，继续吗？', '重试死信任务', { type: 'warning', confirmButtonText: '重新入队', cancelButtonText: '取消' })
    retrying.value = taskID
    await retryTask(taskID, store.token)
    ElMessage.success('任务已重新入队')
    await load()
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(`重试失败：${error instanceof Error ? error.message : String(error)}`)
  } finally { retrying.value = '' }
}
onMounted(load)
</script>

<template>
  <section class="glass-panel page-panel">
    <div class="page-panel-head"><div><span>ASYNC WORK QUEUE</span><h2>任务队列</h2><p>后台任务独立展示，运行记录只保留消息与决策</p></div><div class="filters"><el-select v-model="status" clearable placeholder="全部状态" @change="load(1)"><el-option label="待处理" value="pending" /><el-option label="运行中" value="running" /><el-option label="重试中" value="retry" /><el-option label="已完成" value="completed" /><el-option label="死信" value="dead_letter" /></el-select><el-button :icon="Refresh" :loading="loading" @click="load()">刷新</el-button></div></div>
    <div class="task-summary"><span>共 {{ total }} 条 · 本页 {{ rows.length }} 条</span><span>待处理 {{ counts.pending || 0 }}</span><span>重试 {{ counts.retry || 0 }}</span><span>死信 {{ counts.dead_letter || 0 }}</span></div>
    <el-table v-loading="loading" :data="rows" class="relation-table" empty-text="暂无后台任务"><el-table-column label="任务" min-width="220"><template #default="{ row }"><strong>{{ row.kind }}</strong><small class="table-sub">{{ row.id }}</small></template></el-table-column><el-table-column label="上下文" min-width="220"><template #default="{ row }"><span v-if="row.context.group_id">群 {{ row.context.group_id }}</span><small class="table-sub">{{ row.context.payload_keys.join(', ') || '无负载字段' }}</small></template></el-table-column><el-table-column label="状态" width="110"><template #default="{ row }"><el-tag effect="plain">{{ row.status }}</el-tag></template></el-table-column><el-table-column label="重试" width="90"><template #default="{ row }">{{ row.attempts }} / {{ row.max_attempts }}</template></el-table-column><el-table-column label="最近更新" width="140"><template #default="{ row }">{{ relativeTime(row.updated_at) }}</template></el-table-column><el-table-column label="错误" min-width="280"><template #default="{ row }"><span class="task-error">{{ row.last_error || '—' }}</span></template></el-table-column><el-table-column label="操作" width="100" fixed="right"><template #default="{ row }"><el-button v-if="row.status === 'dead_letter'" link type="warning" :loading="retrying === row.id" @click="retry(row.id)">重试</el-button></template></el-table-column></el-table>
    <el-pagination v-if="total > 50" class="task-pagination" layout="prev, pager, next" :current-page="page" :page-size="50" :total="total" @current-change="load" />
  </section>
</template>
