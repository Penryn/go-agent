<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { Delete, Picture, Refresh, Search } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { deleteMeme, getMemes } from '@/lib/api'
import { relativeTime } from '@/lib/format'
import type { MemeRecord } from '@/types'
import { useDashboardStore } from '@/stores/dashboard'

const store = useDashboardStore()
const { selectedGroup, token } = storeToRefs(store)
const memes = ref<MemeRecord[]>([])
const query = ref('')
const loading = ref(false)
const deleting = ref('')
const total = ref(0)
const page = ref(1)
let searchTimer: number | undefined

const sentCount = computed(() => memes.value.reduce((total, item) => total + item.send_count, 0))
const previewCount = computed(() => memes.value.filter((item) => item.preview_url).length)

async function load(nextPage = page.value) {
  loading.value = true
  page.value = nextPage
  try {
    const result = await getMemes(selectedGroup.value, query.value, page.value, token.value)
    memes.value = result.items
    total.value = result.total
  } catch (error) {
    ElMessage.error(`读取表情包失败：${error instanceof Error ? error.message : String(error)}`)
  } finally {
    loading.value = false
  }
}

function dudRate(item: MemeRecord) {
  return item.send_count ? Math.round(item.dud_count / item.send_count * 100) : 0
}

function previewURL(item: MemeRecord) {
  if (!item.preview_url || !token.value) return item.preview_url
  return `${item.preview_url}?token=${encodeURIComponent(token.value)}`
}

async function remove(item: MemeRecord) {
  try {
    await ElMessageBox.confirm(`删除“${item.title || '未命名表情包'}”后，Bot 将不再检索或发送它。原始群消息不会被删除。`, '删除表情包', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' })
  } catch {
    return
  }
  deleting.value = item.meme_id
  try {
    await deleteMeme(item.meme_id, token.value)
    memes.value = memes.value.filter((candidate) => candidate.meme_id !== item.meme_id)
    ElMessage.success('表情包已删除')
  } catch (error) {
    ElMessage.error(`删除失败：${error instanceof Error ? error.message : String(error)}`)
  } finally {
    deleting.value = ''
  }
}

watch(selectedGroup, () => load(1))
watch(query, () => {
  window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(() => load(1), 250)
})
onMounted(load)
</script>

<template>
  <section class="glass-panel page-panel meme-page">
    <div class="page-panel-head">
      <div><span>REACTION ARCHIVE</span><h2>表情包库</h2><p>只在这里管理 Bot 收集的图片素材；首页与监控页不重复展示明细。</p></div>
      <div class="meme-filters"><el-input v-model="query" :prefix-icon="Search" clearable placeholder="搜索标题、摘要或标签" /><el-button :icon="Refresh" :loading="loading" @click="load()">刷新</el-button></div>
    </div>

    <div class="meme-summary"><span><b>{{ total }}</b> 个素材</span><span><b>{{ previewCount }}</b> 个可预览（本页）</span><span><b>{{ sentCount }}</b> 次发送（本页）</span></div>

    <div v-if="memes.length" v-loading="loading" class="meme-grid">
      <article v-for="item in memes" :key="item.meme_id" class="meme-card">
        <div class="meme-preview">
          <el-image v-if="item.preview_url" :src="previewURL(item)" fit="contain" loading="lazy" :preview-src-list="[previewURL(item)]" preview-teleported hide-on-click-modal />
          <div v-else class="meme-placeholder"><el-icon><Picture /></el-icon><span>暂无可访问预览</span><small>{{ item.file_ext || '图片素材' }}</small></div>
          <span v-if="item.animated" class="meme-motion">GIF</span>
        </div>
        <div class="meme-copy">
          <div class="meme-card-head"><el-tag effect="plain" size="small">群 {{ item.group_id || '全局' }}</el-tag><span>{{ relativeTime(item.created_at) }}</span><el-button class="meme-delete" link type="danger" :icon="Delete" :loading="deleting === item.meme_id" aria-label="删除表情包" @click.stop="remove(item)" /></div>
          <h3>{{ item.title || '未命名表情包' }}</h3>
          <p>{{ item.summary || '暂无视觉摘要' }}</p>
          <div class="meme-tags"><span v-for="tag in [...item.emotion_tags, ...item.scene_tags, ...item.keywords].slice(0, 5)" :key="tag">{{ tag }}</span></div>
          <footer><span>发送 {{ item.send_count }}</span><span>哑弹 {{ dudRate(item) }}%</span><span>置信度 {{ item.confidence.toFixed(2) }}</span></footer>
        </div>
      </article>
    </div>
    <el-empty v-else-if="!loading" description="当前群还没有收集到表情包" />
    <el-pagination v-if="total > 50" class="meme-pagination" layout="prev, pager, next" :current-page="page" :page-size="50" :total="total" @current-change="load" />
  </section>
</template>
