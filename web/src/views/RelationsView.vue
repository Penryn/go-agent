<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { Search } from '@element-plus/icons-vue'
import { relativeTime } from '@/lib/format'
import { getRelationships } from '@/lib/api'
import { useDashboardStore } from '@/stores/dashboard'

const store = useDashboardStore()
const { selectedGroup, token } = storeToRefs(store)
const query = ref('')
const rows = ref<Awaited<ReturnType<typeof getRelationships>>['items']>([])
const total = ref(0)
const page = ref(1)
const loading = ref(false)
const loadError = ref('')
async function load(nextPage = page.value) {
  loading.value = true
  page.value = nextPage
  loadError.value = ''
  try {
    const result = await getRelationships(selectedGroup.value, query.value, page.value, token.value)
    rows.value = result.items
    total.value = result.total
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : String(error)
  } finally {
    loading.value = false
  }
}
onMounted(load)
watch(selectedGroup, () => load(1))
watch(query, () => {
  page.value = 1
  load(1)
})
</script>

<template>
  <section class="view-wrapper">
    <h2>关系管理</h2>
    <el-input v-model="query" placeholder="搜索成员名称..." :prefix-icon="Search" clearable @clear="load(1)" />
    <el-alert v-if="loadError" type="error" :title="loadError" :closable="false" style="margin-top: 1em;" />
    <el-table :data="rows" stripe style="width: 100%; margin-top: 1em;" v-loading="loading">
      <el-table-column label="成员" width="180" prop="name" />
      <el-table-column label="亲密度" width="100" sortable prop="affinity"><template #default="{ row }">{{ row.affinity.toFixed(2) }}</template></el-table-column>
      <el-table-column label="熟悉度" width="100" sortable prop="familiarity"><template #default="{ row }">{{ row.familiarity.toFixed(2) }}</template></el-table-column>
      <el-table-column label="玩笑容忍" width="110" prop="tease_tolerance"><template #default="{ row }">{{ row.tease_tolerance.toFixed(2) }}</template></el-table-column>
      <el-table-column label="信任" width="90" sortable prop="trust"><template #default="{ row }">{{ row.trust.toFixed(2) }}</template></el-table-column>
      <el-table-column label="摩擦" width="90" sortable prop="friction"><template #default="{ row }">{{ row.friction.toFixed(2) }}</template></el-table-column>
      <el-table-column label="互动" width="100" sortable prop="message_count"><template #default="{ row }">{{ row.message_count }} 次</template></el-table-column>
      <el-table-column label="最近互动" width="130"><template #default="{ row }">{{ relativeTime(row.last_interact_at) }}</template></el-table-column>
    </el-table>
    <el-alert type="info" :closable="false" style="margin-top: 1em;">
      <template #title>
        💡 提示：点击行可以查看关系事件历史和投影变化（功能即将上线）
      </template>
    </el-alert>
    <el-pagination v-if="total > 50" class="relation-pagination" layout="prev, pager, next" :current-page="page" :page-size="50" :total="total" @current-change="load" />
  </section>
</template>

<style scoped>
.view-wrapper {
  padding: 1em;
}

.relation-pagination {
  margin-top: 1em;
  justify-content: center;
}
</style>
