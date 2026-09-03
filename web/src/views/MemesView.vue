<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { Picture, Refresh, Search } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { getMemes } from '@/lib/api'
import { relativeTime } from '@/lib/format'
import type { MemeRecord } from '@/types'
import { useDashboardStore } from '@/stores/dashboard'

const store = useDashboardStore()
const { selectedGroup, token } = storeToRefs(store)
const memes = ref<MemeRecord[]>([])
const query = ref('')
const loading = ref(false)
let searchTimer: number | undefined

const sentCount = computed(() => memes.value.reduce((total, item) => total + item.send_count, 0))
const previewCount = computed(() => memes.value.filter((item) => item.preview_url).length)

async function load() {
  loading.value = true
  try {
    memes.value = await getMemes(selectedGroup.value, query.value, token.value)
  } catch (error) {
    ElMessage.error(`读取表情包失败：${error instanceof Error ? error.message : String(error)}`)
  } finally {
    loading.value = false
  }
}

function dudRate(item: MemeRecord) {
  return item.send_count ? Math.round(item.dud_count / item.send_count * 100) : 0
}

watch(selectedGroup, load)
watch(query, () => {
  window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(load, 250)
})
onMounted(load)
</script>

<template>
  <section class="glass-panel page-panel meme-page">
    <div class="page-panel-head">
      <div><span>REACTION ARCHIVE</span><h2>表情包库</h2><p>只在这里管理 Bot 收集的图片素材；首页与监控页不重复展示明细。</p></div>
      <div class="meme-filters"><el-input v-model="query" :prefix-icon="Search" clearable placeholder="搜索标题、摘要或标签" /><el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button></div>
    </div>

    <div class="meme-summary"><span><b>{{ memes.length }}</b> 个素材</span><span><b>{{ previewCount }}</b> 个可预览</span><span><b>{{ sentCount }}</b> 次发送</span></div>

    <div v-if="memes.length" v-loading="loading" class="meme-grid">
      <article v-for="item in memes" :key="item.meme_id" class="meme-card">
        <div class="meme-preview">
          <el-image v-if="item.preview_url" :src="item.preview_url" fit="contain" loading="lazy" :preview-src-list="[item.preview_url]" preview-teleported hide-on-click-modal />
          <div v-else class="meme-placeholder"><el-icon><Picture /></el-icon><span>暂无可访问预览</span><small>{{ item.file_ext || '图片素材' }}</small></div>
          <span v-if="item.animated" class="meme-motion">GIF</span>
        </div>
        <div class="meme-copy">
          <div class="meme-card-head"><el-tag effect="plain" size="small">群 {{ item.group_id || '全局' }}</el-tag><span>{{ relativeTime(item.created_at) }}</span></div>
          <h3>{{ item.title || '未命名表情包' }}</h3>
          <p>{{ item.summary || '暂无视觉摘要' }}</p>
          <div class="meme-tags"><span v-for="tag in [...item.emotion_tags, ...item.scene_tags, ...item.keywords].slice(0, 5)" :key="tag">{{ tag }}</span></div>
          <footer><span>发送 {{ item.send_count }}</span><span>哑弹 {{ dudRate(item) }}%</span><span>置信度 {{ item.confidence.toFixed(2) }}</span></footer>
        </div>
      </article>
    </div>
    <el-empty v-else-if="!loading" description="当前群还没有收集到表情包" />
  </section>
</template>
