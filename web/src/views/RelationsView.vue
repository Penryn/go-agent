<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { Search } from '@element-plus/icons-vue'
import { relativeTime } from '@/lib/format'
import { getRelationships, api } from '@/lib/api'
import { useDashboardStore } from '@/stores/dashboard'
import type { Relationship, RelationshipEvent, ProjectionSnapshot } from '@/types'

const store = useDashboardStore()
const { selectedGroup, token } = storeToRefs(store)
const query = ref('')
const rows = ref<Awaited<ReturnType<typeof getRelationships>>['items']>([])
const total = ref(0)
const page = ref(1)
const loading = ref(false)
const loadError = ref('')

// 展开的详情
const expandedRow = ref<Relationship | null>(null)
const events = ref<RelationshipEvent[]>([])
const projectionHistory = ref<ProjectionSnapshot[]>([])
const detailLoading = ref(false)

// 直接定义 API 函数以绕过导入问题
async function loadRelationshipEvents(groupID: number, userID: number, tokenStr: string): Promise<RelationshipEvent[]> {
  const response = await api.get<RelationshipEvent[]>(`/relationships/${groupID}/${userID}/events`, {
    headers: tokenStr ? { Authorization: `Bearer ${tokenStr}` } : undefined,
  })
  return response.data
}

async function loadRelationshipProjectionHistory(groupID: number, userID: number, tokenStr: string): Promise<ProjectionSnapshot[]> {
  const response = await api.get<ProjectionSnapshot[]>(`/relationships/${groupID}/${userID}/projection-history`, {
    headers: tokenStr ? { Authorization: `Bearer ${tokenStr}` } : undefined,
  })
  return response.data
}

async function load(nextPage = page.value) {
  loading.value = true
  page.value = nextPage
  loadError.value = ''
  try {
    const result = await getRelationships(selectedGroup.value, query.value, page.value, token.value)
    rows.value = result.items
    total.value = result.total
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : String(error)
  } finally {
    loading.value = false
  }
}

async function handleRowClick(row: Relationship) {
  if (expandedRow.value?.user_id === row.user_id && expandedRow.value?.group_id === row.group_id) {
    expandedRow.value = null
    return
  }

  expandedRow.value = row
  detailLoading.value = true

  try {
    const [eventsData, historyData] = await Promise.all([
      loadRelationshipEvents(row.group_id, row.user_id, token.value),
      loadRelationshipProjectionHistory(row.group_id, row.user_id, token.value)
    ])
    events.value = eventsData
    projectionHistory.value = historyData
  } catch (error) {
    console.error('加载关系详情失败:', error)
  } finally {
    detailLoading.value = false
  }
}

function getEventLabel(kind: string): string {
  const labels: Record<string, string> = {
    'message_received': '收到消息',
    'direct_reply': '直接回复',
    'positive_feedback': '正面反馈',
    'negative_feedback': '负面反馈',
    'help_given': '提供帮助',
    'help_received': '接受帮助',
    'user_correction': '用户纠正',
    'user_teasing': '用户调侃',
    'user_rejected_teasing': '拒绝调侃',
    'bot_overtalked': '机器人话多',
    'conversation_continued': '对话继续',
    'conversation_dropped': '对话中断'
  }
  return labels[kind] || kind
}

function getValenceColor(valence: number): string {
  if (valence > 0.3) return '#67c23a'
  if (valence < -0.3) return '#f56c6c'
  return '#909399'
}

function getFieldChange(index: number, field: keyof ProjectionSnapshot): string {
  if (index >= projectionHistory.value.length - 1) return ''
  const current = projectionHistory.value[index][field] as number
  const previous = projectionHistory.value[index + 1][field] as number
  const diff = current - previous
  if (Math.abs(diff) < 0.01) return ''
  return diff > 0 ? `↑ +${diff.toFixed(2)}` : `↓ ${diff.toFixed(2)}`
}

onMounted(load)
watch(selectedGroup, () => load(1))
watch(query, () => {
  page.value = 1
  load(1)
})
</script>

<template>
  <section class="view-wrapper">
    <h2>关系管理</h2>
    <el-input v-model="query" placeholder="搜索成员名称..." :prefix-icon="Search" clearable @clear="load(1)" />
    <el-alert v-if="loadError" type="error" :title="loadError" :closable="false" style="margin-top: 1em;" />
    <el-table :data="rows" stripe style="width: 100%; margin-top: 1em;" v-loading="loading" @row-click="handleRowClick" :row-class-name="({ row }: { row: Relationship }) => expandedRow?.user_id === row.user_id && expandedRow?.group_id === row.group_id ? 'expanded-row' : ''">
      <el-table-column label="成员" width="180" prop="name" />
      <el-table-column label="亲密度" width="100" sortable prop="affinity"><template #default="{ row }">{{ row.affinity.toFixed(2) }}</template></el-table-column>
      <el-table-column label="熟悉度" width="100" sortable prop="familiarity"><template #default="{ row }">{{ row.familiarity.toFixed(2) }}</template></el-table-column>
      <el-table-column label="玩笑容忍" width="110" prop="tease_tolerance"><template #default="{ row }">{{ row.tease_tolerance.toFixed(2) }}</template></el-table-column>
      <el-table-column label="信任" width="90" sortable prop="trust"><template #default="{ row }">{{ row.trust.toFixed(2) }}</template></el-table-column>
      <el-table-column label="摩擦" width="90" sortable prop="friction"><template #default="{ row }">{{ row.friction.toFixed(2) }}</template></el-table-column>
      <el-table-column label="互动" width="100" sortable prop="message_count"><template #default="{ row }">{{ row.message_count }} 次</template></el-table-column>
      <el-table-column label="最近互动" width="130"><template #default="{ row }">{{ relativeTime(row.last_interact_at) }}</template></el-table-column>
    </el-table>

    <!-- 展开的详情面板 -->
    <el-card v-if="expandedRow" class="detail-card" shadow="hover">
      <template #header>
        <div class="card-header">
          <span>{{ expandedRow.name }} 的关系详情</span>
          <el-button text @click="expandedRow = null">关闭</el-button>
        </div>
      </template>

      <el-tabs v-loading="detailLoading">
        <el-tab-pane label="关系事件">
          <div v-if="events.length === 0" class="empty-tip">暂无关系事件</div>
          <el-timeline v-else>
            <el-timeline-item
              v-for="event in events"
              :key="event.event_id"
              :timestamp="relativeTime(event.created_at)"
              placement="top"
            >
              <el-tag :type="event.valence > 0.3 ? 'success' : event.valence < -0.3 ? 'danger' : 'info'" size="small">
                {{ getEventLabel(event.kind) }}
              </el-tag>
              <span style="margin-left: 8px; color: var(--el-text-color-secondary);">
                情绪值: <span :style="{ color: getValenceColor(event.valence), fontWeight: 'bold' }">{{ event.valence.toFixed(2) }}</span>
              </span>
              <div v-if="event.evidence_event_id" style="font-size: 12px; color: var(--el-text-color-secondary); margin-top: 4px;">
                证据事件: {{ event.evidence_event_id }}
              </div>
            </el-timeline-item>
          </el-timeline>
        </el-tab-pane>

        <el-tab-pane label="投影历史">
          <div v-if="projectionHistory.length === 0" class="empty-tip">暂无历史数据</div>
          <div v-else class="history-list">
            <el-card v-for="(snapshot, index) in projectionHistory" :key="snapshot.revision" shadow="hover" class="history-item">
              <div class="history-header">
                <el-tag type="info" size="small">版本 {{ snapshot.revision }}</el-tag>
                <span class="history-time">{{ relativeTime(snapshot.updated_at) }}</span>
              </div>
              <div v-if="snapshot.trigger_kind" class="trigger-info">
                <span>🔗</span>
                触发: {{ getEventLabel(snapshot.trigger_kind) }}
              </div>
              <el-descriptions :column="2" border size="small" style="margin-top: 8px;">
                <el-descriptions-item label="熟悉度">
                  <span>{{ snapshot.familiarity.toFixed(2) }}</span>
                  <span v-if="getFieldChange(index, 'familiarity')" :style="{ marginLeft: '8px', fontSize: '12px', color: getFieldChange(index, 'familiarity').startsWith('↑') ? '#67c23a' : '#f56c6c' }">
                    {{ getFieldChange(index, 'familiarity') }}
                  </span>
                </el-descriptions-item>
                <el-descriptions-item label="亲密度">
                  <span>{{ snapshot.affinity.toFixed(2) }}</span>
                  <span v-if="getFieldChange(index, 'affinity')" :style="{ marginLeft: '8px', fontSize: '12px', color: getFieldChange(index, 'affinity').startsWith('↑') ? '#67c23a' : '#f56c6c' }">
                    {{ getFieldChange(index, 'affinity') }}
                  </span>
                </el-descriptions-item>
                <el-descriptions-item label="信任">
                  <span>{{ snapshot.trust.toFixed(2) }}</span>
                  <span v-if="getFieldChange(index, 'trust')" :style="{ marginLeft: '8px', fontSize: '12px', color: getFieldChange(index, 'trust').startsWith('↑') ? '#67c23a' : '#f56c6c' }">
                    {{ getFieldChange(index, 'trust') }}
                  </span>
                </el-descriptions-item>
                <el-descriptions-item label="玩笑容忍">
                  <span>{{ snapshot.tease_tolerance.toFixed(2) }}</span>
                  <span v-if="getFieldChange(index, 'tease_tolerance')" :style="{ marginLeft: '8px', fontSize: '12px', color: getFieldChange(index, 'tease_tolerance').startsWith('↑') ? '#67c23a' : '#f56c6c' }">
                    {{ getFieldChange(index, 'tease_tolerance') }}
                  </span>
                </el-descriptions-item>
                <el-descriptions-item label="摩擦">
                  <span>{{ snapshot.friction.toFixed(2) }}</span>
                  <span v-if="getFieldChange(index, 'friction')" :style="{ marginLeft: '8px', fontSize: '12px', color: getFieldChange(index, 'friction').startsWith('↑') ? '#f56c6c' : '#67c23a' }">
                    {{ getFieldChange(index, 'friction') }}
                  </span>
                </el-descriptions-item>
              </el-descriptions>
            </el-card>
          </div>
        </el-tab-pane>
      </el-tabs>
    </el-card>

    <el-pagination v-if="total > 50" class="relation-pagination" layout="prev, pager, next" :current-page="page" :page-size="50" :total="total" @current-change="load" />
  </section>
</template>

<style scoped>
.view-wrapper {
  padding: 1em;
}

.relation-pagination {
  margin-top: 1em;
  justify-content: center;
}

.detail-card {
  margin-top: 1em;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.empty-tip {
  text-align: center;
  color: var(--el-text-color-secondary);
  padding: 2em 0;
}

.history-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.history-item {
  transition: all 0.3s;
}

.history-item:hover {
  transform: translateX(4px);
}

.history-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}

.history-time {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.trigger-info {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  color: var(--el-text-color-regular);
  margin-bottom: 8px;
}

:deep(.expanded-row) {
  background-color: var(--el-fill-color-light);
}

:deep(.el-table__row) {
  cursor: pointer;
}

:deep(.el-table__row:hover) {
  background-color: var(--el-fill-color-light);
}
</style>
