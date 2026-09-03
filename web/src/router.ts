import { createRouter, createWebHashHistory } from 'vue-router'
import ActivityView from '@/views/ActivityView.vue'
import MemoryView from '@/views/MemoryView.vue'
import OverviewView from '@/views/OverviewView.vue'
import RelationsView from '@/views/RelationsView.vue'
import MonitoringView from '@/views/MonitoringView.vue'
import MCPView from '@/views/MCPView.vue'
import TasksView from '@/views/TasksView.vue'

export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', name: 'overview', component: OverviewView, meta: { title: '实时概览' } },
    { path: '/memory', name: 'memory', component: MemoryView, meta: { title: '长期记忆' } },
    { path: '/relations', name: 'relations', component: RelationsView, meta: { title: '群友关系' } },
    { path: '/activity', name: 'activity', component: ActivityView, meta: { title: '运行记录' } },
    { path: '/tasks', name: 'tasks', component: TasksView, meta: { title: '任务队列' } },
    { path: '/monitoring', name: 'monitoring', component: MonitoringView, meta: { title: '监控指标' } },
    { path: '/mcp', name: 'mcp', component: MCPView, meta: { title: 'MCP 工具' } },
  ],
})
