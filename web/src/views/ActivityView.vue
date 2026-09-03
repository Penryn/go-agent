<script setup lang="ts">
import { computed, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { activityText, relativeTime } from '@/lib/format'
import { getEventDetail } from '@/lib/api'
import type { EventDetail } from '@/types'
import { useDashboardStore } from '@/stores/dashboard'

const store = useDashboardStore()
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
    task: activity.filter((item) => item.type === 'task').length,
  }
})

async function openDetail(eventID: string) {
  if (!eventID) return
  detail.value = await getEventDetail(eventID, store.token)
}
</script>

<template>
  <section class="glass-panel page-panel">
    <div class="page-panel-head"><div><span>EVENT STREAM</span><h2>运行记录</h2><p>统一时间线，按来源筛选；消息是输入，决策是选择，任务是后台执行</p></div><el-segmented v-model="filter" :options="[{ label: '全部', value: 'all' }, { label: '消息', value: 'message' }, { label: '决策', value: 'decision' }, { label: '任务', value: 'task' }]" /></div>
    <div class="activity-summary" aria-label="记录分类统计">
      <button :class="{ active: filter === 'message' }" @click="filter = 'message'"><span class="summary-mark message" /><strong>{{ counts.message }}</strong><span>消息输入</span></button>
      <button :class="{ active: filter === 'decision' }" @click="filter = 'decision'"><span class="summary-mark decision" /><strong>{{ counts.decision }}</strong><span>Bot 决策</span></button>
      <button :class="{ active: filter === 'task' }" @click="filter = 'task'"><span class="summary-mark task" /><strong>{{ counts.task }}</strong><span>后台任务</span></button>
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
      <div class="detail-meta"><span>群 {{ detail.group_id }} · 用户 {{ detail.user_id }}</span><time>{{ relativeTime(detail.occurred_at) }}</time></div>
      <div class="detail-section"><span class="detail-label">输入消息</span><p class="detail-message">{{ detail.text || '—' }}</p><small>{{ detail.sender || '未知发送者' }} · {{ detail.kind }}</small></div>
      <div v-if="detail.decision" class="detail-section"><span class="detail-label">Bot 决策</span><div class="detail-grid"><span>动作 <b>{{ detail.decision.action || '—' }}</b></span><span>结果 <b>{{ detail.decision.outcome || '—' }}</b></span><span>不确定度 <b>{{ detail.decision.uncertainty.toFixed(2) }}</b></span></div><p>{{ detail.decision.interpretation || '—' }}</p></div>
      <div class="detail-section"><span class="detail-label">检索记录</span><div v-if="detail.retrievals.length" class="retrieval-list"><div v-for="item in detail.retrievals" :key="item.trace_id" class="retrieval-row"><div><b>{{ item.query || '空 query' }}</b><small>{{ item.candidate_count }} 个候选 · 命中 {{ item.hit_memory_ids.length }} 条 · 采用 {{ item.selected_ids.length }} 条</small></div><el-tag v-if="item.outcome" effect="plain">{{ item.outcome }}</el-tag></div></div><el-empty v-else description="该事件没有检索记录" :image-size="48" /></div>
    </div>
  </el-dialog>
</template>
