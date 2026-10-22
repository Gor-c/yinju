// 入库任务分为三步：上传（入队）→ 解析 → 索引。
// 后端 index_jobs.phase 记录当前阶段，失败时保留出错阶段，前端据此显示
// 「解析失败」还是「索引失败」。

export type JobStage = 'upload' | 'parse' | 'index'
export type JobStageState = 'done' | 'active' | 'failed' | 'idle'

const STAGE_INDEX: Record<JobStage, number> = { upload: 0, parse: 1, index: 2 }
const PHASE_ORDER: Record<string, number> = { pending: 0, parse: 1, index: 2, done: 3 }

export function jobStageState(phase: string | undefined, status: string, stage: JobStage): JobStageState {
  const current = PHASE_ORDER[phase || 'pending'] ?? 0
  const index = STAGE_INDEX[stage]

  if (status === 'failed') {
    if (index < current) return 'done'
    if (index === current) return 'failed'
    return 'idle'
  }
  if (status === 'done') return 'done'
  // 排队中：文件已收下，解析尚未开始。
  if (index === 0) return 'done'
  if (index < current) return 'done'
  if (index === current && current > 0) return 'active'
  return 'idle'
}

export function jobPhaseLabel(phase: string | undefined, status: string): string {
  if (status === 'done') return '已完成'
  if (status === 'failed') {
    if (phase === 'parse') return '解析失败'
    if (phase === 'index') return '索引失败'
    return '失败'
  }
  switch (phase) {
    case 'parse':
      return '解析中'
    case 'index':
      return '索引中'
    default:
      return '排队中'
  }
}
