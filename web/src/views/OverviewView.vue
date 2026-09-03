<script setup lang="ts">
import { computed } from 'vue'
import { storeToRefs } from 'pinia'
import { useDashboardStore } from '@/stores/dashboard'
import { energyText, moodText, relativeTime, talkBiasText } from '@/lib/format'

const store = useDashboardStore()
const { snapshot, loading } = storeToRefs(store)
const persona = computed(() => snapshot.value?.persona)
const groups = computed(() => [...(snapshot.value?.groups || [])].sort((a, b) => b.messages - a.messages))
const stats = computed(() => [
  { label: '活跃群聊', value: snapshot.value?.stats.groups ?? 0, code: 'GROUPS', tone: 'mint' },
  { label: '已识别群友', value: snapshot.value?.stats.members ?? 0, code: 'PEOPLE', tone: 'violet' },
  { label: '有效记忆', value: snapshot.value?.stats.memories ?? 0, code: 'MEMORY', tone: 'amber' },
  { label: '后台任务', value: snapshot.value?.stats.pending_tasks ?? 0, code: 'QUEUE', tone: 'blue' },
])
</script>

<template>
  <el-skeleton :loading="loading && !snapshot" animated :rows="8">
    <section class="metric-grid">
      <article v-for="item in stats" :key="item.label" class="metric-card" :data-tone="item.tone">
        <span>{{ item.label }}</span><small>{{ item.code }}</small><strong>{{ item.value }}</strong><i />
      </article>
    </section>

    <section class="overview-grid">
      <article class="glass-panel identity-panel">
        <div class="panel-title"><div><span>IDENTITY</span><h2>此刻身份</h2></div><el-tag :type="snapshot?.status.qq_connected ? 'success' : 'info'" effect="dark" round>{{ snapshot?.status.qq_connected ? '在线' : '离线' }}</el-tag></div>
        <div class="identity-hero">
          <div class="portrait"><span>{{ persona?.name?.slice(0, 1) || '芙' }}</span><i :data-online="snapshot?.status.qq_connected === true" /></div>
          <div class="identity-copy">
            <h3>{{ persona?.name || 'Bot' }}</h3>
            <p>{{ persona?.description || '等待身份数据' }}</p>
            <div class="state-pills">
              <span>情绪 <b>{{ moodText(persona?.mood) }}</b></span>
              <span>精力 <b>{{ energyText(persona?.energy) }}</b></span>
              <span>发言倾向 <b>{{ talkBiasText(persona?.talk_bias) }}</b></span>
              <span>状态 <b>{{ persona?.runtime.state || 'observing' }}</b></span>
            </div>
          </div>
        </div>
        <div class="fact-grid">
          <div v-for="fact in persona?.facts || []" :key="fact.fact_id" class="fact-cell">
            <span>{{ fact.key }}</span><strong :title="fact.value">{{ fact.value }}</strong>
          </div>
          <el-empty v-if="!persona?.facts.length" description="暂无人物事实" :image-size="56" />
        </div>
      </article>

      <article class="glass-panel runtime-panel">
        <div class="panel-title"><div><span>RUNTIME HEALTH</span><h2>运行体征</h2></div><el-tag :type="snapshot?.status.qq_connected ? 'success' : 'danger'" effect="plain">{{ snapshot?.status.qq_connected ? '正常' : '需检查' }}</el-tag></div>
        <div class="health-status"><span class="health-dot" :data-online="snapshot?.status.qq_connected === true" /><div><strong>{{ snapshot?.status.qq_connected ? 'QQ 连接稳定' : 'QQ 连接中断' }}</strong><span>{{ snapshot?.status.mode || '—' }} · 账号 {{ snapshot?.status.self_id || '—' }}</span></div></div>
        <div class="runtime-metrics">
          <div><span>当前状态</span><strong>{{ persona?.runtime.state || '—' }}</strong></div>
          <div><span>近 10 分钟发言</span><strong>{{ persona?.runtime.replies_last_10min ?? 0 }}</strong></div>
          <div><span>兴趣标签</span><strong>{{ persona?.interests.length ?? 0 }}</strong></div>
        </div>
        <div class="runtime-note">QQ {{ snapshot?.status.qq_connected ? '正常' : '异常' }} · 数据库 {{ snapshot?.status.database_ok ? '正常' : '异常' }} · 主模型 {{ snapshot?.status.main_model_status === 'ready' ? '已就绪' : '未配置' }} · 向量检索 {{ snapshot?.status.vector_search_status === 'ready' ? '已启用' : '未启用' }} · 队列积压 {{ snapshot?.status.queue_backlog ?? 0 }} · 最近错误 {{ relativeTime(snapshot?.status.last_error_at) }} · 详细任务请到任务队列查看。</div>
      </article>

      <article class="glass-panel groups-panel">
        <div class="panel-title"><div><span>GROUP COVERAGE</span><h2>群聊状态</h2></div><span class="panel-hint">按消息量排序</span></div>
        <div v-if="groups.length" class="group-list">
          <div v-for="group in groups" :key="group.group_id" class="group-row">
            <div class="group-id"><span>群</span><strong>{{ group.group_id }}</strong></div>
            <div class="group-topic"><strong>{{ group.active_topic || '暂无活跃话题' }}</strong><span>{{ group.members }} 位群友 · {{ group.messages }} 条消息</span></div>
            <div class="group-load"><div class="load-track"><i :style="{ width: `${Math.min(100, Math.max(8, group.messages / Math.max(1, groups[0]?.messages || 1) * 100))}%` }" /></div><span>{{ group.messages }}</span></div>
          </div>
        </div>
        <el-empty v-else description="还没有接入群聊" :image-size="70" />
      </article>
    </section>
  </el-skeleton>
</template>
