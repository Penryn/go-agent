<script setup lang="ts">
import { computed } from 'vue'
import { storeToRefs } from 'pinia'
import { useDashboardStore } from '@/stores/dashboard'

const store = useDashboardStore()
const { snapshot, windowMinutes } = storeToRefs(store)
const memories = computed(() => snapshot.value?.memories || [])
const decisions = computed(() => snapshot.value?.window_metrics.decisions ?? 0)
const tasks = computed(() => snapshot.value?.window_metrics.tasks ?? 0)
const avgConfidence = computed(() => memories.value.length ? memories.value.reduce((sum, item) => sum + item.confidence, 0) / memories.value.length : 0)
const avgImportance = computed(() => memories.value.length ? memories.value.reduce((sum, item) => sum + item.importance, 0) / memories.value.length : 0)
const taskFailures = computed(() => snapshot.value?.window_metrics.failed_tasks ?? 0)
const taskFailureRate = computed(() => tasks.value ? taskFailures.value / tasks.value : 0)
const decisionReplyRate = computed(() => decisions.value ? (snapshot.value?.window_metrics.action_decisions ?? 0) / decisions.value : 0)
const retrieval = computed(() => snapshot.value?.retrieval || { queries: 0, queries_with_hits: 0, hit_rate: 0, avg_candidate_count: 0, result_recorded_queries: 0, selected_queries: 0, selection_rate: 0 })
const modelUsage = computed(() => snapshot.value?.model_usage || { calls: 0, input_tokens: 0, output_tokens: 0, avg_duration_ms: 0, error_calls: 0 })
const windowMinutesLabel = computed(() => windowMinutes.value === 1440 ? '近 24 小时' : windowMinutes.value === 60 ? '近 1 小时' : '近 10 分钟')
</script>

<template>
  <section class="glass-panel page-panel monitoring-page">
    <div class="page-panel-head"><div><span>OBSERVABILITY</span><h2>监控指标</h2><p>检索质量与后续决策采用情况</p></div><el-segmented v-model="windowMinutes" :options="[{ label: '10 分钟', value: 10 }, { label: '1 小时', value: 60 }, { label: '24 小时', value: 1440 }]" @change="store.setWindow" /></div>

    <section class="monitor-grid">
      <article class="monitor-card" data-tone="mint"><span>窗口内实际发言</span><strong>{{ snapshot?.window_metrics.replies ?? 0 }}</strong><small>{{ windowMinutesLabel }} · outcome=sent</small></article>
      <article class="monitor-card" data-tone="violet"><span>决策中有动作</span><strong>{{ Math.round(decisionReplyRate * 100) }}%</strong><small>{{ decisions }} 条决策记录</small></article>
      <article class="monitor-card" data-tone="amber"><span>记忆平均置信度</span><strong>{{ avgConfidence.toFixed(2) }}</strong><small>{{ memories.length }} 条有效记忆</small></article>
      <article class="monitor-card" data-tone="blue"><span>任务失败占比</span><strong>{{ Math.round(taskFailureRate * 100) }}%</strong><small>{{ tasks }} 条任务记录</small></article>
      <article class="monitor-card" data-tone="violet"><span>检索命中率</span><strong>{{ Math.round(retrieval.hit_rate * 100) }}%</strong><small>{{ retrieval.queries_with_hits }} / {{ retrieval.queries }} 次有结果</small></article>
      <article class="monitor-card" data-tone="blue"><span>平均候选数</span><strong>{{ retrieval.avg_candidate_count.toFixed(1) }}</strong><small>{{ retrieval.queries }} 次检索</small></article>
      <article class="monitor-card" data-tone="mint"><span>召回采用率</span><strong>{{ Math.round(retrieval.selection_rate * 100) }}%</strong><small>{{ retrieval.selected_queries }} / {{ retrieval.queries_with_hits }} 次进入决策</small></article>
      <article class="monitor-card" data-tone="amber"><span>模型平均耗时</span><strong>{{ Math.round(modelUsage.avg_duration_ms) }}ms</strong><small>当前窗口 · {{ modelUsage.calls }} 次调用</small></article>
    </section>

    <section class="monitor-panels">
      <article class="monitor-detail">
        <div class="panel-title"><div><span>MEMORY QUALITY</span><h3>记忆质量基线</h3></div><el-tag effect="plain">可用</el-tag></div>
        <div class="quality-row"><span>平均置信度</span><strong>{{ avgConfidence.toFixed(2) }}</strong><div class="quality-track"><i :style="{ width: `${avgConfidence * 100}%` }" /></div></div>
        <div class="quality-row"><span>平均重要度</span><strong>{{ avgImportance.toFixed(2) }}</strong><div class="quality-track amber"><i :style="{ width: `${avgImportance * 100}%` }" /></div></div>
        <p>这反映记忆库本身的质量基线，不等于某次对话的召回正确率。</p>
      </article>

      <article class="monitor-detail monitor-gap">
        <div class="panel-title"><div><span>RETRIEVAL TELEMETRY</span><h3>检索与召回</h3></div><el-tag effect="plain">已接入</el-tag></div>
        <div class="quality-row"><span>检索次数</span><strong>{{ retrieval.queries }}</strong></div>
        <div class="quality-row"><span>已回写结果</span><strong>{{ retrieval.result_recorded_queries }}</strong></div>
        <p>采用率表示召回内容进入最终决策上下文，不代表记忆内容一定正确；正确率仍需人工标注或用户反馈。</p>
      </article>
      <article class="monitor-detail">
        <div class="panel-title"><div><span>MODEL USAGE</span><h3>模型用量（当前窗口）</h3></div><el-tag effect="plain">{{ modelUsage.error_calls }} 次错误</el-tag></div>
        <div class="quality-row"><span>输入 token</span><strong>{{ modelUsage.input_tokens.toLocaleString() }}</strong></div>
        <div class="quality-row"><span>输出 token</span><strong>{{ modelUsage.output_tokens.toLocaleString() }}</strong></div>
      </article>
    </section>
  </section>
</template>
