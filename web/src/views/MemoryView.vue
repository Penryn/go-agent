<script setup lang="ts">
import { computed, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { Search } from '@element-plus/icons-vue'
import { relativeTime } from '@/lib/format'
import { useDashboardStore } from '@/stores/dashboard'

const { snapshot } = storeToRefs(useDashboardStore())
const query = ref('')
const type = ref('')
const types = computed(() => [...new Set(snapshot.value?.memories.map((item) => item.type) || [])])
const memories = computed(() => (snapshot.value?.memories || []).filter((item) => {
  const matchesType = !type.value || item.type === type.value
  const needle = query.value.trim().toLowerCase()
  return matchesType && (!needle || `${item.subject} ${item.content} ${item.scope}`.toLowerCase().includes(needle))
}))
</script>

<template>
  <section class="glass-panel page-panel">
    <div class="page-panel-head"><div><span>MEMORY ARCHIVE</span><h2>长期记忆</h2><p>当前群聊可见的有效记忆，共 {{ memories.length }} 条</p></div><div class="filters"><el-input v-model="query" :prefix-icon="Search" clearable placeholder="搜索内容或作用域" /><el-select v-model="type" clearable placeholder="全部类型"><el-option v-for="item in types" :key="item" :label="item" :value="item" /></el-select></div></div>
    <div v-if="memories.length" class="memory-grid">
      <article v-for="item in memories" :key="item.id" class="memory-card">
        <div class="memory-meta"><el-tag effect="plain" round>{{ item.type }}</el-tag><span>{{ relativeTime(item.created_at) }}</span></div>
        <h3>{{ item.subject || item.scope }}</h3><p>{{ item.content }}</p>
        <footer><span>{{ item.scope }}</span><span>重要度 {{ item.importance.toFixed(2) }}</span><span>置信度 {{ item.confidence.toFixed(2) }}</span></footer>
      </article>
    </div>
    <el-empty v-else description="没有符合条件的记忆" />
  </section>
</template>
