<script setup lang="ts">
import { computed } from 'vue'
import { storeToRefs } from 'pinia'
import { useDashboardStore } from '@/stores/dashboard'

const { snapshot } = storeToRefs(useDashboardStore())
const memories = computed(() => snapshot.value?.memories || [])
const decisions = computed(() => (snapshot.value?.activity || []).filter((item) => item.type === 'decision'))
const tasks = computed(() => (snapshot.value?.activity || []).filter((item) => item.type === 'task'))
const avgConfidence = computed(() => memories.value.length ? memories.value.reduce((sum, item) => sum + item.confidence, 0) / memories.value.length : 0)
const avgImportance = computed(() => memories.value.length ? memories.value.reduce((sum, item) => sum + item.importance, 0) / memories.value.length : 0)
const taskFailures = computed(() => tasks.value.filter((item) => /failed|error|dead/i.test(item.subject)).length)
const taskFailureRate = computed(() => tasks.value.length ? taskFailures.value / tasks.value.length : 0)
const replyActions = computed(() => decisions.value.filter((item) => /reply|react|meme|poke/i.test(item.label)).length)
const decisionReplyRate = computed(() => decisions.value.length ? replyActions.value / decisions.value.length : 0)
</script>

<template>
  <section class="glass-panel page-panel monitoring-page">
    <div class="page-panel-head"><div><span>OBSERVABILITY</span><h2>监控指标</h2><p>先展示已有数据能证明的信号；检索命中率与召回效果等待埋点接入</p></div></div>

    <section class="monitor-grid">
      <article class="monitor-card" data-tone="mint"><span>近 10 分钟发言</span><strong>{{ snapshot?.persona.runtime.replies_last_10min ?? 0 }}</strong><small>当前群聊</small></article>
      <article class="monitor-card" data-tone="violet"><span>决策中有动作</span><strong>{{ Math.round(decisionReplyRate * 100) }}%</strong><small>{{ decisions.length }} 条决策记录</small></article>
      <article class="monitor-card" data-tone="amber"><span>记忆平均置信度</span><strong>{{ avgConfidence.toFixed(2) }}</strong><small>{{ memories.length }} 条有效记忆</small></article>
      <article class="monitor-card" data-tone="blue"><span>任务失败占比</span><strong>{{ Math.round(taskFailureRate * 100) }}%</strong><small>{{ tasks.length }} 条任务记录</small></article>
    </section>

    <section class="monitor-panels">
      <article class="monitor-detail">
        <div class="panel-title"><div><span>MEMORY QUALITY</span><h3>记忆质量基线</h3></div><el-tag effect="plain">可用</el-tag></div>
        <div class="quality-row"><span>平均置信度</span><strong>{{ avgConfidence.toFixed(2) }}</strong><div class="quality-track"><i :style="{ width: `${avgConfidence * 100}%` }" /></div></div>
        <div class="quality-row"><span>平均重要度</span><strong>{{ avgImportance.toFixed(2) }}</strong><div class="quality-track amber"><i :style="{ width: `${avgImportance * 100}%` }" /></div></div>
        <p>这反映记忆库本身的质量基线，不等于某次对话的召回正确率。</p>
      </article>

      <article class="monitor-detail monitor-gap">
        <div class="panel-title"><div><span>RETRIEVAL TELEMETRY</span><h3>检索与召回</h3></div><el-tag type="warning" effect="plain">待埋点</el-tag></div>
        <div class="gap-state"><strong>当前无法可靠计算</strong><p>现有数据只保存最终决策和有效记忆，没有记录每次检索的 query、候选数、命中记忆及后续反馈。</p></div>
        <div class="telemetry-list"><span>建议记录</span><b>query · top_k · hit_count · selected_ids · outcome</b></div>
      </article>
    </section>
  </section>
</template>
