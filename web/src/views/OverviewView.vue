<script setup lang="ts">
import { computed } from 'vue'
import { storeToRefs } from 'pinia'
import { useDashboardStore } from '@/stores/dashboard'
import { activityText, energyText, moodText, relativeTime, talkBiasText } from '@/lib/format'

const store = useDashboardStore()
const { snapshot, loading } = storeToRefs(store)
const persona = computed(() => snapshot.value?.persona)
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

      <article class="glass-panel relation-panel">
        <div class="panel-title"><div><span>RELATIONSHIP</span><h2>群友好感度</h2></div><RouterLink to="/relations">查看全部</RouterLink></div>
        <div v-if="snapshot?.relationships.length" class="relation-list">
          <div v-for="(item, index) in snapshot.relationships.slice(0, 6)" :key="item.user_id" class="relation-row">
            <div class="rank">{{ String(index + 1).padStart(2, '0') }}</div>
            <div class="member"><strong>{{ item.name }}</strong><span>{{ item.message_count }} 条消息 · {{ relativeTime(item.last_interact_at) }}</span></div>
            <div class="affinity"><el-progress :percentage="Math.round(item.affinity * 100)" :show-text="false" :stroke-width="5" /><span>{{ Math.round(item.affinity * 100) }}</span></div>
          </div>
        </div>
        <el-empty v-else description="还没有形成群友关系" :image-size="70" />
      </article>

      <article class="glass-panel activity-panel">
        <div class="panel-title"><div><span>ACTIVITY</span><h2>最近运行记录</h2></div><RouterLink to="/activity">打开时间线</RouterLink></div>
        <div v-if="snapshot?.activity.length" class="activity-stream">
          <div v-for="item in snapshot.activity.slice(0, 8)" :key="`${item.type}-${item.at}-${item.subject}`" class="activity-row">
            <i :data-type="item.type" /><time>{{ relativeTime(item.at) }}</time><el-tag size="small" effect="plain">{{ activityText(item.type) }}</el-tag>
            <div><strong>{{ item.subject || item.label }}</strong><span>{{ item.detail || '—' }}</span></div>
          </div>
        </div>
        <el-empty v-else description="暂无运行记录" :image-size="70" />
      </article>
    </section>
  </el-skeleton>
</template>
