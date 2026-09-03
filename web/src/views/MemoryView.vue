<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { Search } from '@element-plus/icons-vue'
import { relativeTime } from '@/lib/format'
import { getMemories } from '@/lib/api'
import type { MemoryRecord } from '@/types'
import { useDashboardStore } from '@/stores/dashboard'

const store = useDashboardStore()
const router = useRouter()
const { selectedGroup, token } = storeToRefs(store)
const query = ref('')
const type = ref('')
const status = ref('active')
const memories = ref<MemoryRecord[]>([])
const loading = ref(false)
const types = computed(() => [...new Set(memories.value.map((item) => item.type))])
const filteredMemories = computed(() => memories.value.filter((item) => !type.value || item.type === type.value))
async function load() { loading.value = true; try { memories.value = await getMemories(selectedGroup.value, status.value, query.value, token.value) } finally { loading.value = false } }
function openSource(eventID: string) { void router.push({ name: 'activity', query: { event_id: eventID } }) }
watch([selectedGroup, status, query], load)
onMounted(load)
</script>

<template>
  <section class="glass-panel page-panel">
    <div class="page-panel-head"><div><span>MEMORY ARCHIVE</span><h2>长期记忆</h2><p>{{ status === 'expired' ? '已过期记忆' : status === 'all' ? '全部记忆' : '当前群聊可见的有效记忆' }}，共 {{ filteredMemories.length }} 条</p></div><div class="filters"><el-input v-model="query" :prefix-icon="Search" clearable placeholder="搜索内容或作用域" /><el-select v-model="status" placeholder="记忆状态"><el-option label="有效" value="active" /><el-option label="已过期" value="expired" /><el-option label="全部" value="all" /></el-select><el-select v-model="type" clearable placeholder="全部类型"><el-option v-for="item in types" :key="item" :label="item" :value="item" /></el-select></div></div>
    <div v-loading="loading" v-if="filteredMemories.length" class="memory-grid">
      <article v-for="item in filteredMemories" :key="item.id" class="memory-card">
        <div class="memory-meta"><el-tag effect="plain" round>{{ item.type }}</el-tag><span>{{ relativeTime(item.created_at) }}</span></div>
        <h3>{{ item.subject || item.scope }}</h3><p>{{ item.content }}</p>
        <footer><span>{{ item.scope }}</span><span>重要度 {{ item.importance.toFixed(2) }}</span><span>置信度 {{ item.confidence.toFixed(2) }}</span><span v-if="item.expires_at">{{ new Date(item.expires_at).getTime() > Date.now() ? '过期时间 ' + relativeTime(item.expires_at) : '已过期' }}</span><el-button v-if="item.source_event_id" link type="primary" @click="openSource(item.source_event_id)">查看来源</el-button></footer>
      </article>
    </div>
    <el-empty v-else description="没有符合条件的记忆" />
  </section>
</template>
