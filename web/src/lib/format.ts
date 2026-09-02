export function relativeTime(value?: string) {
  if (!value || value.startsWith('0001-')) return '暂无'
  const seconds = Math.max(0, (Date.now() - new Date(value).getTime()) / 1000)
  if (seconds < 60) return `${Math.floor(seconds)} 秒前`
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`
  return new Date(value).toLocaleDateString('zh-CN')
}

export const moodText = (value?: string) => ({ happy: '开心', steady: '平稳', withdrawn: '低落', aggro: '有点烦' })[value ?? ''] ?? value ?? '平稳'
export const energyText = (value?: string) => ({ high: '充沛', normal: '正常', low: '偏低', tired: '疲惫' })[value ?? ''] ?? value ?? '正常'
export const activityText = (value: string) => ({ message: '消息', decision: '决策', task: '任务' })[value] ?? value
