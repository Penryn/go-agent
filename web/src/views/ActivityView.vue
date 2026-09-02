<script setup lang="ts">
import { computed, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { activityText, relativeTime } from '@/lib/format'
import { useDashboardStore } from '@/stores/dashboard'

const { snapshot } = storeToRefs(useDashboardStore())
const filter = ref('all')
const rows = computed(() => (snapshot.value?.activity || []).filter((item) => filter.value === 'all' || item.type === filter.value))
</script>

<template>
  <section class="glass-panel page-panel">
    <div class="page-panel-head"><div><span>EVENT STREAM</span><h2>运行记录</h2><p>群消息、Bot 决策和后台任务的统一时间线</p></div><el-segmented v-model="filter" :options="[{ label: '全部', value: 'all' }, { label: '消息', value: 'message' }, { label: '决策', value: 'decision' }, { label: '任务', value: 'task' }]" /></div>
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
