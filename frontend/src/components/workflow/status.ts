const labels: Record<string, string> = {
  queued: '排队中',
  running: '运行中',
  waiting: '等待处理',
  retrying: '等待重试',
  succeeded: '成功',
  failed: '失败',
  cancelled: '已取消',
  skipped: '已跳过'
}
export const runStatusLabel = (status: string | undefined) =>
  labels[status || ''] || status || '暂无运行'
