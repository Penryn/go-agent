<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { activityText, relativeTime } from '@/lib/format'
import { getEventDetail } from '@/lib/api'
import type { EventDetail } from '@/types'
import { useDashboardStore } from '@/stores/dashboard'
import { useRoute } from 'vue-router'

const store = useDashboardStore()
const route = useRoute()
const { snapshot } = storeToRefs(store)
const filter = ref('all')
const detail = ref<EventDetail>()
const detailVisible = computed({ get: () => !!detail.value, set: (value: boolean) => { if (!value) detail.value = undefined } })
const rows = computed(() => (snapshot.value?.activity || []).filter((item) => filter.value === 'all' || item.type === filter.value))
const counts = computed(() => {
  const activity = snapshot.value?.activity || []
  return {
    message: activity.filter((item) => item.type === 'message').length,
    decision: activity.filter((item) => item.type === 'decision').length,
  }
})

async function openDetail(eventID: string) {
  if (!eventID) return
  detail.value = await getEventDetail(eventID, store.token)
}

onMounted(() => {
  const eventID = route.query.event_id
  if (typeof eventID === 'string') void openDetail(eventID)
})
</script>

<template>
  <section class="glass-panel page-panel">
    <div class="page-panel-head"><div><span>EVENT STREAM</span><h2>运行记录</h2><p>消息与决策时间线；后台任务请到任务队列查看</p></div><el-segmented v-model="filter" :options="[{ label: '全部', value: 'all' }, { label: '消息', value: 'message' }, { label: '决策', value: 'decision' }]" /></div>
    <div class="activity-summary" aria-label="记录分类统计">
      <button :class="{ active: filter === 'message' }" @click="filter = 'message'"><span class="summary-mark message" /><strong>{{ counts.message }}</strong><span>消息输入</span></button>
      <button :class="{ active: filter === 'decision' }" @click="filter = 'decision'"><span class="summary-mark decision" /><strong>{{ counts.decision }}</strong><span>Bot 决策</span></button>
    </div>
    <div v-if="rows.length" class="timeline">
      <article v-for="item in rows" :key="`${item.type}-${item.at}-${item.subject}`" class="timeline-item" :class="{ clickable: !!item.event_id }" @click="openDetail(item.event_id)">
        <div class="timeline-rail"><i :data-type="item.type" /></div>
        <time>{{ relativeTime(item.at) }}</time>
        <div class="timeline-copy"><div><el-tag effect="plain" round>{{ activityText(item.type) }}</el-tag><span>{{ item.label }}<template v-if="item.group_id"> · 群 {{ item.group_id }}</template></span></div><h3>{{ item.subject || item.label }}</h3><p>{{ item.detail || '—' }}</p></div>
      </article>
    </div>
    <el-empty v-else description="暂无运行记录" />
  </section>

  <el-dialog v-model="detailVisible" title="单次对话 / 决策详情" width="min(720px, calc(100vw - 28px))">
    <div v-if="detail" class="event-detail">
      <div class="detail-meta"><span>事件 {{ detail.event_id }} · 消息 {{ detail.message_id }}</span><span>群 {{ detail.group_id }} · 用户 {{ detail.user_id }} · 链路耗时 {{ detail.duration_ms }}ms</span><time>{{ relativeTime(detail.occurred_at) }}</time></div>
      <div class="detail-section"><span class="detail-label">输入消息</span><p class="detail-message">{{ detail.text || '—' }}</p><small>{{ detail.sender || '未知发送者' }} · {{ detail.kind }}</small></div>
      <div v-if="detail.decision" class="detail-section"><span class="detail-label">Bot 决策</span><div class="detail-grid"><span>动作 <b>{{ detail.decision.action || '—' }}</b></span><span>结果 <b>{{ detail.decision.outcome || '—' }}</b></span><span>不确定度 <b>{{ detail.decision.uncertainty.toFixed(2) }}</b></span></div><p>{{ detail.decision.interpretation || '—' }}</p></div>
      <div class="detail-section"><span class="detail-label">检索记录 · {{ detail.retrievals.length ? '已执行' : '未执行' }}</span><div v-if="detail.retrievals.length" class="retrieval-list"><div v-for="item in detail.retrievals" :key="item.trace_id" class="retrieval-row"><div><b>{{ item.query || '空 query' }}</b><small>trace {{ item.trace_id }} · {{ item.candidate_count }} 个候选 · 命中 {{ item.hit_memory_ids.length }} 条 · 采用 {{ item.selected_ids.length }} 条</small></div><el-tag v-if="item.outcome" effect="plain">{{ item.outcome }}</el-tag></div></div><el-empty v-else description="该事件没有检索记录" :image-size="48" /></div>
      <div v-if="detail.model_usages.length" class="detail-section"><span class="detail-label">模型调用</span><div class="retrieval-list"><div v-for="item in detail.model_usages" :key="`${item.trace_id}-${item.iteration}`" class="retrieval-row"><div><b>第 {{ item.iteration }} 次调用</b><small>trace {{ item.trace_id }} · {{ item.input_tokens }} 输入 token · {{ item.output_tokens }} 输出 token · {{ item.duration_ms }}ms · {{ item.tools.join(', ') || '无工具' }}</small><small v-if="item.error" class="task-error">{{ item.error }}</small></div><el-tag v-if="item.error" type="danger" effect="plain">失败</el-tag></div></div></div>
    </div>
  </el-dialog>
</template>
