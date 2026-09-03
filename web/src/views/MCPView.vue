<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Plus, Delete, Refresh } from '@element-plus/icons-vue'
import { storeToRefs } from 'pinia'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getMCPConfig, updateMCPConfig } from '@/lib/api'
import type { MCPServerConfig } from '@/types'
import { useDashboardStore } from '@/stores/dashboard'

const store = useDashboardStore()
const { token } = storeToRefs(store)
const servers = ref<MCPServerConfig[]>([])
const loading = ref(false)
const saving = ref(false)

function blankServer(): MCPServerConfig {
  return { name: '', enabled: true, required: false, transport: 'stdio', command: '', args: [], url: '', tools: [], timeout: '15s' }
}

async function load() {
  loading.value = true
  try {
    servers.value = (await getMCPConfig(token.value)).servers
  } catch (error) {
    ElMessage.error(`读取 MCP 配置失败：${error instanceof Error ? error.message : String(error)}`)
  } finally {
    loading.value = false
  }
}

function remove(index: number) {
  servers.value.splice(index, 1)
}

async function save() {
  saving.value = true
  try {
    const result = await updateMCPConfig(servers.value, token.value)
    servers.value = result.servers
    ElMessage.success('MCP 配置已应用')
  } catch (error) {
    ElMessage.error(`应用失败，旧配置仍在运行：${error instanceof Error ? error.message : String(error)}`)
  } finally {
    saving.value = false
  }
}

async function confirmSave() {
  await ElMessageBox.confirm('保存后会连接新服务并替换当前 MCP 工具；连接失败不会影响旧配置。', '应用 MCP 配置', { type: 'warning', confirmButtonText: '应用', cancelButtonText: '取消' })
  await save()
}

onMounted(load)
</script>

<template>
  <section class="glass-panel page-panel mcp-page">
    <div class="page-panel-head">
      <div><span>TOOL CONNECTIONS</span><h2>MCP 工具</h2><p>配置只影响 MCP；数据库、模型和 QQ 连接仍由启动配置管理。</p></div>
      <div class="mcp-actions"><el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button><el-button type="primary" :loading="saving" @click="confirmSave">应用配置</el-button></div>
    </div>

    <div class="mcp-toolbar"><span>{{ servers.length }} 个服务</span><el-button link type="primary" :icon="Plus" @click="servers.push(blankServer())">添加服务</el-button></div>
    <el-empty v-if="!servers.length && !loading" description="尚未配置 MCP 服务" />
    <div v-else class="mcp-list">
      <article v-for="(server, index) in servers" :key="index" class="mcp-card">
        <div class="mcp-card-head"><div><strong>{{ server.name || '未命名服务' }}</strong><span>{{ server.transport === 'stdio' ? '本地进程' : 'HTTP 服务' }}</span></div><div class="mcp-card-tools"><el-switch v-model="server.enabled" inline-prompt active-text="启用" inactive-text="停用" /><el-button link type="danger" :icon="Delete" aria-label="删除服务" @click="remove(index)" /></div></div>
        <div class="mcp-fields">
          <label>名称<el-input v-model="server.name" placeholder="web-search" /></label>
          <label>传输<el-select v-model="server.transport"><el-option label="stdio" value="stdio" /><el-option label="http" value="http" /></el-select></label>
          <label v-if="server.transport === 'stdio'">命令<el-input v-model="server.command" placeholder="npx" /></label>
          <label v-else>URL<el-input v-model="server.url" placeholder="https://..." /></label>
          <label>超时<el-input v-model="server.timeout" placeholder="15s" /></label>
          <label class="mcp-wide">参数（每行一个）<el-input :model-value="server.args.join('\n')" type="textarea" :rows="2" placeholder="-y&#10;@modelcontextprotocol/server" @update:model-value="server.args = $event.split(/\r?\n/).map((item) => item.trim()).filter(Boolean)" /></label>
          <label class="mcp-wide">工具筛选（每行一个，留空表示全部）<el-input :model-value="server.tools.join('\n')" type="textarea" :rows="2" placeholder="search&#10;fetch" @update:model-value="server.tools = $event.split(/\r?\n/).map((item) => item.trim()).filter(Boolean)" /></label>
        </div>
        <div class="mcp-card-foot"><el-checkbox v-model="server.required">连接失败时阻止启动/应用</el-checkbox><span>工具会以 mcp_{{ server.name || 'server' }}_* 暴露</span></div>
      </article>
    </div>
  </section>
</template>
