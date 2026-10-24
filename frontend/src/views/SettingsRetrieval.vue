<template>
  <div class="settings-section">
    <header class="page-head">
      <div>
        <div class="eyebrow">Retrieval &amp; Memory</div>
        <h1>检索与记忆</h1>
        <p class="subtitle">改动立即生效并逐条记入留痕；切块尺寸变更需要重建索引才会生效。</p>
      </div>
      <div class="head-actions">
        <el-button @click="load" :loading="loading">刷新</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </div>
    </header>

    <div v-if="message" class="notice" :class="messageType">{{ message }}</div>

    <section class="panel rk-panel">
      <div class="rk-panel-head">
        <b>召回与阈值</b>
        <span class="muted">三个通道条数 + 两道闸门的阈值</span>
      </div>
      <div class="rk-grid">
        <label v-for="f in retrievalFields" :key="f.key" class="rk-field">
          <span class="rk-label">{{ f.label }}</span>
          <el-input-number
            :model-value="form[f.key]"
            :min="limit(f.key).min"
            :max="limit(f.key).max"
            :step="limit(f.key).step"
            size="small"
            controls-position="right"
            @update:model-value="(v: number | undefined) => (form[f.key] = v ?? 0)"
          />
          <small>{{ f.hint }}</small>
        </label>
      </div>
    </section>

    <!-- 调完就地验证：改一个阈值马上能看到它把哪一层掐掉了，
         不用跳去 Agent 问一句再回来看留痕。 -->
    <section class="panel rk-panel">
      <div class="rk-panel-head">
        <b>用当前参数试一次</b>
        <span class="muted">保存过的参数才会生效；结果同时写入下方检索留痕</span>
      </div>

      <div class="rk-try-bar">
        <el-select v-model="tryUnit" size="small" placeholder="选择知识库" class="rk-try-unit">
          <el-option v-for="u in units" :key="u.unit_id" :label="u.name" :value="u.unit_id" />
        </el-select>
        <el-input
          v-model="tryQuery"
          size="small"
          placeholder="输入一个真实会问的问题，回车检索"
          class="rk-try-input"
          @keyup.enter="runTry"
        />
        <el-button size="small" type="primary" :loading="trying" :disabled="!tryQuery.trim() || !tryUnit" @click="runTry">
          检索
        </el-button>
      </div>

      <div v-if="tryError" class="notice error rk-try-error">{{ tryError }}</div>

      <div v-if="tryResult" class="rk-try-result">
        <div class="rk-steps">
          <div class="rk-step"><span>向量</span><b>{{ tryResult.stats.vector_hits }}</b></div>
          <span class="rk-step-sep">→</span>
          <div class="rk-step"><span>关键词</span><b>{{ tryResult.stats.keyword_hits }}</b></div>
          <span class="rk-step-sep">→</span>
          <div class="rk-step"><span>融合</span><b>{{ tryResult.stats.fused }}</b></div>
          <span class="rk-step-sep">→</span>
          <div class="rk-step" :class="{ 'rk-step-zero': tryResult.stats.reranked === 0 }">
            <span>精排</span><b>{{ tryResult.stats.reranked }}</b>
          </div>
          <span class="rk-step-sep">→</span>
          <div class="rk-step" :class="{ 'rk-step-zero': tryResult.count === 0 }">
            <span>留下</span><b>{{ tryResult.count }}</b>
          </div>
          <span class="rk-try-time">{{ tryResult.duration_ms }}ms</span>
        </div>

        <p class="rk-verdict" :class="{ warn: tryResult.count === 0 }">{{ verdict }}</p>

        <ul v-if="tryResult.matches.length" class="rk-hits">
          <li v-for="(m, i) in tryResult.matches.slice(0, 3)" :key="i">
            <span class="rk-hit-score">{{ (m.score || 0).toFixed(3) }}</span>
            <span class="rk-hit-file">{{ m.filename }}</span>
            <span class="rk-hit-text">{{ m.content.slice(0, 90) }}</span>
          </li>
        </ul>
      </div>
    </section>

    <section class="panel rk-panel">
      <div class="rk-panel-head">
        <b>切块</b>
        <span class="muted">改了只影响新入库的文档，旧文档要重建索引</span>
      </div>
      <div class="rk-grid">
        <label v-for="f in chunkFields" :key="f.key" class="rk-field">
          <span class="rk-label">{{ f.label }}</span>
          <el-input-number
            :model-value="form[f.key]"
            :min="limit(f.key).min"
            :max="limit(f.key).max"
            :step="limit(f.key).step"
            size="small"
            controls-position="right"
            @update:model-value="(v: number | undefined) => (form[f.key] = v ?? 0)"
          />
          <small>{{ f.hint }}</small>
        </label>
      </div>
    </section>

    <section class="panel rk-panel">
      <div class="rk-panel-head">
        <b>记忆窗口</b>
        <span class="muted">只影响会话上下文回放，不改写已存消息</span>
      </div>
      <label class="rk-switch">
        <el-switch v-model="summaryEnabled" size="small" />
        <span class="rk-switch-copy">
          <b>生成历史摘要与约束记忆</b>
          <small>关闭后只回放最近若干条原文，更早的内容会直接丢失；摘要本身不可作为引用依据。</small>
        </span>
      </label>
      <div class="rk-grid">
        <label v-for="f in memoryFields" :key="f.key" class="rk-field">
          <span class="rk-label">{{ f.label }}</span>
          <el-input-number
            :model-value="form[f.key]"
            :min="limit(f.key).min"
            :max="limit(f.key).max"
            :step="limit(f.key).step"
            size="small"
            controls-position="right"
            @update:model-value="(v: number | undefined) => (form[f.key] = v ?? 0)"
          />
          <small>{{ f.hint }}</small>
        </label>
      </div>
    </section>

    <section class="panel rk-panel">
      <div class="rk-panel-head">
        <b>变更留痕</b>
        <span class="muted">谁在什么时候把哪个参数改成了多少</span>
      </div>
      <table v-if="changes.length" class="rk-table">
        <thead>
          <tr><th>时间</th><th>参数</th><th>原值</th><th>新值</th></tr>
        </thead>
        <tbody>
          <tr v-for="(c, i) in changes" :key="i">
            <td class="muted">{{ shortTime(c.changed_at) }}</td>
            <td>{{ keyLabels[c.key] || c.key }}</td>
            <td class="muted">{{ c.old }}</td>
            <td>{{ c.new }}</td>
          </tr>
        </tbody>
      </table>
      <p v-else class="muted rk-empty">还没有改动记录。改完参数保存一次，这里就会留痕。</p>
    </section>

    <section class="panel rk-panel">
      <div class="rk-panel-head">
        <b>检索留痕</b>
        <span class="muted">每次检索的分层计数：哪一层丢掉的内容，看这里</span>
      </div>
      <table v-if="traces.length" class="rk-table rk-table-wide">
        <thead>
          <tr>
            <th>时间</th><th>问题</th><th>向量</th><th>关键词</th><th>融合</th><th>重排</th><th>留下</th><th>耗时</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(t, i) in traces" :key="i" :class="{ 'rk-refused': t.no_evidence }">
            <td class="muted">{{ shortTime(t.created_at) }}</td>
            <td class="rk-query">{{ t.query }}</td>
            <td>{{ t.vector_hits }}</td>
            <td>{{ t.keyword_hits }}</td>
            <td>{{ t.fused }}</td>
            <td :class="{ 'rk-zero': t.reranked === 0 }">{{ t.reranked }}</td>
            <td :class="{ 'rk-zero': t.kept === 0 }">{{ t.kept }}</td>
            <td class="muted">{{ t.duration_ms }}ms</td>
          </tr>
        </tbody>
      </table>
      <p v-else class="muted rk-empty">还没有检索记录。去 Agent 里问一个问题，这里就会出现。</p>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import axios from 'axios'
import { ElMessage } from 'element-plus'

type Limits = Record<string, { min: number; max: number; step: number }>

const loading = ref(false)
const saving = ref(false)
const message = ref('')
// 摘要开关是布尔值，不放进数字表单，避免类型混用。
const summaryEnabled = ref(true)
const messageType = ref<'success' | 'error'>('success')
const limits = ref<Limits>({})
const changes = ref<any[]>([])
const traces = ref<any[]>([])

const form = reactive<Record<string, number>>({
  vector_k: 30,
  keyword_k: 30,
  fusion_k: 20,
  max_distance: 0.55,
  min_overlap: 0.28,
  min_score: 0.1,
  chunk_size: 1800,
  chunk_overlap: 240,
  memory_messages: 20,
  memory_runes: 4000,
})

const retrievalFields = [
  { key: 'vector_k', label: '向量召回条数', hint: '每次从向量库取多少候选' },
  { key: 'keyword_k', label: '关键词召回条数', hint: 'trigram 通道取多少候选' },
  { key: 'fusion_k', label: '融合后条数', hint: 'RRF 之后送进精排的数量' },
  { key: 'max_distance', label: '距离兜底上限', hint: '精排不可用时按向量距离过滤' },
  { key: 'min_overlap', label: '覆盖率闸门', hint: '低于此值判为无依据' },
  { key: 'min_score', label: '精排分数下限', hint: '低于此值直接丢弃' },
]

const chunkFields = [
  { key: 'chunk_size', label: '切块尺寸（字符）', hint: '条款型文档建议 800–1200' },
  { key: 'chunk_overlap', label: '相邻重叠（字符）', hint: '避免条款被切开后语义断裂' },
]

const memoryFields = [
  { key: 'memory_messages', label: '回放消息条数', hint: '0 表示不记忆，每次只带当前这一问' },
  { key: 'memory_runes', label: '单条截断字数', hint: '超长回答会按此截断后再回放' },
]

// 留痕里存的是字段名，直接显示 memory_messages 对用户没有意义，
// 这里映射成页面上同一个中文标签，两处叫法保持一致。
const keyLabels: Record<string, string> = {
  vector_k: '向量召回条数',
  keyword_k: '关键词召回条数',
  fusion_k: '融合后条数',
  max_distance: '距离兜底上限',
  min_overlap: '覆盖率闸门',
  min_score: '精排分数下限',
  chunk_size: '切块尺寸',
  chunk_overlap: '相邻重叠',
  memory_messages: '回放消息条数',
  memory_runes: '单条截断字数',
  outline_enabled: '大纲抽取',
}

const limit = (key: string) => limits.value[key] || { min: 0, max: 9999, step: 1 }

// ---------------- 用当前参数试一次 ----------------
interface TryStats {
  vector_hits: number
  keyword_hits: number
  fused: number
  reranked: number
  degraded?: string[]
  doc_count?: number
}

interface TryMatch {
  filename: string
  chunk_index: number
  score: number
  distance: number
  content: string
}

interface TryResult {
  count: number
  no_evidence: boolean
  stats: TryStats
  matches: TryMatch[]
  duration_ms: number
}

const units = ref<Array<{ unit_id: string; name: string; document_count?: number }>>([])
const tryUnit = ref('')
const tryQuery = ref('')
const trying = ref(false)
const tryError = ref('')
const tryResult = ref<TryResult | null>(null)

// 结论按「哪一层把它掐掉了」来说，而不是只报一个 0 ——
// 0 命中至少有三四种完全不同的原因，只说 0 等于没说。
const verdict = computed(() => {
  const r = tryResult.value
  if (!r) return ''
  const s = r.stats
  if (r.count > 0) {
    const top = r.matches[0]?.score || 0
    return `命中 ${r.count} 条，最高分 ${top.toFixed(3)}。这个问法在当前参数下可用。`
  }
  if (s.vector_hits === 0 && s.keyword_hits === 0) {
    return '两路都没有召回：这个库里可能没有相关内容，或问题用词与原文差得太远。'
  }
  if (s.reranked === 0) {
    return `召回到 ${s.fused} 条候选，但精排全部低于 ${form.min_score}。想放宽就把「精排分数下限」调低。`
  }
  return `精排留下了 ${s.reranked} 条，但被覆盖率闸门（${form.min_overlap}）全部丢弃。想放宽就调低它。`
})

async function loadUnits() {
  try {
    const { data } = await axios.get('/api/v1/units')
    units.value = data || []
    const first = units.value.find((u) => (u.document_count || 0) > 0) || units.value[0]
    if (!tryUnit.value && first) tryUnit.value = first.unit_id
  } catch {
    units.value = []
  }
}

async function runTry() {
  const query = tryQuery.value.trim()
  if (!query || !tryUnit.value) return
  trying.value = true
  tryError.value = ''
  try {
    const { data } = await axios.post('/api/v1/retrieve', {
      unit_id: tryUnit.value,
      query,
      top_k: 5,
    })
    tryResult.value = data
  } catch (e: any) {
    tryResult.value = null
    tryError.value = e.response?.data?.error || '检索失败，确认服务与向量库可用。'
  } finally {
    trying.value = false
  }
}

const shortTime = (v: string) =>
  v ? new Date(v).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '—'

async function load() {
  loading.value = true
  try {
    const { data } = await axios.get('/api/v1/settings/retrieval')
    const values = data.values || {}
    Object.assign(form, values)
    summaryEnabled.value = values.memory_summary !== false
    limits.value = data.limits || {}
    changes.value = data.changes || []
    const t = await axios.get('/api/v1/retrieval/traces', { params: { limit: 20 } })
    traces.value = t.data.traces || []
    message.value = ''
  } catch (e: any) {
    messageType.value = 'error'
    message.value = e.response?.data?.error || '读取参数失败'
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    const { data } = await axios.put('/api/v1/settings/retrieval', {
      ...form,
      memory_summary: summaryEnabled.value,
    })
    const changed: string[] = data.changed || []
    messageType.value = 'success'
    message.value = changed.length ? `已保存，改动 ${changed.join('、')} 立即生效` : '没有参数变化'
    ElMessage.success(message.value)
    await load()
  } catch (e: any) {
    messageType.value = 'error'
    message.value = e.response?.data?.error || '保存失败'
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  load()
  loadUnits()
})
</script>

<style scoped>
.rk-panel {
  padding: 18px 20px;
  margin-bottom: 14px;
}

.rk-panel-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin-bottom: 14px;
}

.rk-panel-head b {
  font-size: calc(14px * var(--fs-scale));
  font-weight: 500;
}

.rk-panel-head .muted {
  font-size: calc(12px * var(--fs-scale));
}

.rk-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 14px;
}

.rk-field {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.rk-label {
  font-size: calc(13px * var(--fs-scale));
}

.rk-field small {
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.rk-switch {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 8px 12px;
  margin-bottom: 14px;
  border: 1px solid var(--border);
  border-radius: 9px;
  cursor: pointer;
}

.rk-switch-copy {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.rk-switch-copy b {
  font-size: calc(12.5px * var(--fs-scale));
  font-weight: 500;
}

.rk-switch-copy small {
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
  line-height: 1.6;
}

.rk-try-bar {
  display: flex;
  align-items: center;
  gap: 8px;
}

.rk-try-unit {
  flex: 0 0 auto;
  width: 200px;
}

.rk-try-input {
  flex: 1;
}

.rk-try-error {
  margin-top: 10px;
}

.rk-try-result {
  margin-top: 14px;
}

.rk-steps {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.rk-step {
  display: flex;
  flex-direction: column;
  align-items: center;
  min-width: 54px;
  padding: 6px 10px;
  border: 1px solid var(--border);
  border-radius: 9px;
}

.rk-step span {
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.rk-step b {
  font-size: calc(16px * var(--fs-scale));
  font-weight: 600;
}

.rk-step-zero b {
  color: var(--warning, #b45309);
}

.rk-step-sep {
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.rk-try-time {
  margin-left: auto;
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.rk-verdict {
  margin: 12px 0 0;
  font-size: calc(12.5px * var(--fs-scale));
  line-height: 1.7;
  color: var(--text-secondary);
}

.rk-verdict.warn {
  color: var(--warning, #b45309);
}

.rk-hits {
  margin: 10px 0 0;
  padding: 0;
  list-style: none;
}

.rk-hits li {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 6px 0;
  border-top: 1px dashed var(--border);
  font-size: calc(12px * var(--fs-scale));
}

.rk-hit-score {
  flex: 0 0 auto;
  font-variant-numeric: tabular-nums;
  font-weight: 600;
  color: var(--accent);
}

.rk-hit-file {
  flex: 0 0 auto;
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.rk-hit-text {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-muted);
}

.rk-table {
  width: 100%;
  border-collapse: collapse;
  font-size: calc(12px * var(--fs-scale));
}

.rk-table th {
  text-align: left;
  font-weight: 500;
  color: var(--text-muted);
  padding: 6px 8px;
  border-bottom: 1px solid var(--border);
}

.rk-table td {
  padding: 6px 8px;
  border-bottom: 1px solid var(--border);
}

.rk-query {
  max-width: 280px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.rk-zero {
  color: var(--warning, #b45309);
  font-weight: 500;
}

.rk-refused td {
  background: var(--surface-2, rgba(0, 0, 0, 0.02));
}

.rk-empty {
  font-size: calc(12px * var(--fs-scale));
  margin: 4px 0 0;
}
</style>
