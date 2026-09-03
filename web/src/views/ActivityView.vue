<script setup lang="ts">
import { computed, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { activityText, relativeTime } from '@/lib/format'
import { useDashboardStore } from '@/stores/dashboard'

const { snapshot } = storeToRefs(useDashboardStore())
const filter = ref('all')
const rows = computed(() => (snapshot.value?.activity || []).filter((item) => filter.value === 'all' || item.type === filter.value))
const counts = computed(() => {
  const activity = snapshot.value?.activity || []
  return {
    message: activity.filter((item) => item.type === 'message').length,
    decision: activity.filter((item) => item.type === 'decision').length,
    task: activity.filter((item) => item.type === 'task').length,
  }
})
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
      <article v-for="item in rows" :key="`${item.type}-${item.at}-${item.subject}`" class="timeline-item">
        <div class="timeline-rail"><i :data-type="item.type" /></div>
        <time>{{ relativeTime(item.at) }}</time>
        <div class="timeline-copy"><div><el-tag effect="plain" round>{{ activityText(item.type) }}</el-tag><span>{{ item.label }}<template v-if="item.group_id"> · 群 {{ item.group_id }}</template></span></div><h3>{{ item.subject || item.label }}</h3><p>{{ item.detail || '—' }}</p></div>
      </article>
    </div>
    <el-empty v-else description="暂无运行记录" />
  </section>
</template>
