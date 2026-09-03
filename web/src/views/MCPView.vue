<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { Plus, Delete, Refresh } from '@element-plus/icons-vue'
import { storeToRefs } from 'pinia'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getMCPConfig, updateMCPConfig } from '@/lib/api'
import type { MCPServerConfig, MCPToolInfo } from '@/types'
import { useDashboardStore } from '@/stores/dashboard'

const store = useDashboardStore()
const { token } = storeToRefs(store)
const servers = ref<MCPServerConfig[]>([])
const tools = ref<MCPToolInfo[]>([])
const loading = ref(false)
const saving = ref(false)
const savedFingerprint = ref('')
const dirty = computed(() => JSON.stringify(servers.value) !== savedFingerprint.value)

function blankServer(): MCPServerConfig {
  return { name: '', enabled: true, required: false, transport: 'stdio', command: '', args: [], url: '', tools: [], timeout: '15s' }
}

async function load(force = false) {
  if (force && dirty.value) {
    try {
      await ElMessageBox.confirm('当前有未保存的 MCP 修改，刷新会丢失这些修改。', '确认刷新', { type: 'warning', confirmButtonText: '刷新', cancelButtonText: '取消' })
    } catch {
      return
    }
  }
  loading.value = true
  try {
    const result = await getMCPConfig(token.value)
    servers.value = result.servers
    tools.value = result.tools || []
    savedFingerprint.value = JSON.stringify(servers.value)
  } catch (error) {
    ElMessage.error(`读取 MCP 配置失败：${error instanceof Error ? error.message : String(error)}`)
  } finally {
    loading.value = false
  }
}

function validate() {
  const seen = new Set<string>()
  for (const server of servers.value) {
    if (!server.enabled) continue
    const name = server.name.trim()
    if (!name) return '启用的服务必须填写名称'
    if (seen.has(name)) return `服务名称重复：${name}`
    seen.add(name)
    if (server.transport === 'stdio' && !server.command.trim()) return `${name} 必须填写命令`
    if (server.transport === 'http' && !server.url.trim()) return `${name} 必须填写 URL`
  }
  return ''
}

function remove(index: number) {
  servers.value.splice(index, 1)
}

async function save() {
  const validationError = validate()
  if (validationError) {
    ElMessage.error(validationError)
    return
  }
  saving.value = true
  try {
    const result = await updateMCPConfig(servers.value, token.value)
    servers.value = result.servers
    tools.value = result.tools || []
    savedFingerprint.value = JSON.stringify(servers.value)
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
function beforeUnload(event: BeforeUnloadEvent) {
  if (!dirty.value) return
  event.preventDefault()
  event.returnValue = ''
}
onMounted(() => window.addEventListener('beforeunload', beforeUnload))
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))
onBeforeRouteLeave(async () => {
  if (!dirty.value) return true
  try {
    await ElMessageBox.confirm('当前有未保存的 MCP 修改，离开页面会丢失这些修改。', '确认离开', { type: 'warning', confirmButtonText: '离开', cancelButtonText: '留下' })
    return true
  } catch {
    return false
  }
})
</script>

<template>
  <section class="glass-panel page-panel mcp-page">
    <div class="page-panel-head">
      <div><span>TOOL CONNECTIONS</span><h2>MCP 工具</h2><p>配置只影响 MCP；数据库、模型和 QQ 连接仍由启动配置管理。</p></div>
      <div class="mcp-actions"><el-button :icon="Refresh" :loading="loading" @click="load(true)">刷新</el-button><el-button type="primary" :disabled="!dirty" :loading="saving" @click="confirmSave">应用配置</el-button></div>
    </div>

    <div class="mcp-toolbar"><span>{{ servers.length }} 个服务</span><el-button link type="primary" :icon="Plus" @click="servers.push(blankServer())">添加服务</el-button></div>
    <div v-if="tools.length" class="mcp-tool-list"><div class="mcp-tool-list-head"><strong>已注册工具</strong><span>{{ tools.length }} 个实际可调用</span></div><div class="mcp-tool-grid"><article v-for="tool in tools" :key="tool.name" class="mcp-tool"><code>{{ tool.name }}</code><p>{{ tool.description || '暂无描述' }}</p></article></div></div>
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
