<script setup lang="ts">
import { computed, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { Search } from '@element-plus/icons-vue'
import { relativeTime } from '@/lib/format'
import { useDashboardStore } from '@/stores/dashboard'

const { snapshot } = storeToRefs(useDashboardStore())
const query = ref('')
const rows = computed(() => (snapshot.value?.relationships || []).filter((item) => `${item.name} ${item.user_id}`.toLowerCase().includes(query.value.trim().toLowerCase())))
</script>

<template>
  <section class="glass-panel page-panel">
    <div class="page-panel-head"><div><span>RELATIONSHIP MAP</span><h2>群友关系</h2><p>好感度是 Bot 的主观关系状态，熟悉度来自实际互动证据</p></div><el-input v-model="query" :prefix-icon="Search" clearable class="search-box" placeholder="搜索昵称或 QQ" /></div>
    <el-table :data="rows" class="relation-table" empty-text="还没有关系数据">
      <el-table-column label="群友" min-width="190"><template #default="{ row }"><div class="table-member"><div>{{ row.name.slice(0, 1) }}</div><span><strong>{{ row.name }}</strong><small>{{ row.user_id }}</small></span></div></template></el-table-column>
      <el-table-column label="好感度" min-width="190" sortable prop="affinity"><template #default="{ row }"><div class="table-progress"><el-progress :percentage="Math.round(row.affinity * 100)" :stroke-width="6" /><span>{{ row.affinity.toFixed(2) }}</span></div></template></el-table-column>
      <el-table-column label="熟悉度" width="110" sortable prop="familiarity"><template #default="{ row }">{{ row.familiarity.toFixed(2) }}</template></el-table-column>
      <el-table-column label="玩笑容忍" width="110" prop="tease_tolerance"><template #default="{ row }">{{ row.tease_tolerance.toFixed(2) }}</template></el-table-column>
      <el-table-column label="互动" width="100" sortable prop="message_count"><template #default="{ row }">{{ row.message_count }} 次</template></el-table-column>
      <el-table-column label="最近互动" width="130"><template #default="{ row }">{{ relativeTime(row.last_interact_at) }}</template></el-table-column>
    </el-table>
  </section>
</template>
