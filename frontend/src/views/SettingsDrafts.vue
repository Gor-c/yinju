<template>
  <div class="page-head">
    <div>
      <h1>知识沉淀</h1>
      <p class="subtitle">
        每次有依据的回答都会留下一条候选；<b>只有采纳后才会进入知识库</b>，未采纳的内容不参与检索。
      </p>
    </div>
    <div class="draft-head-actions">
      <el-button :loading="distilling" @click="distill">整理成笔记</el-button>
      <el-button :loading="loading" @click="load">刷新</el-button>
    </div>
  </div>

  <div class="draft-notice">
    <span>待审核 <b>{{ pending }}</b> 条</span>
    <span class="draft-notice-sep">·</span>
    <span>每小时自动整理一次（积累满 {{ autoMin }} 条才触发，只生成草稿）</span>
    <span class="draft-notice-sep">·</span>
    <span>采纳后走同一套索引管线：切块、嵌入、章节抽取</span>
  </div>

  <el-tabs v-model="tab" class="draft-tabs" @tab-change="load">
    <el-tab-pane label="待审核" name="pending" />
    <el-tab-pane label="已采纳" name="accepted" />
    <el-tab-pane label="已整理合并" name="merged" />
    <el-tab-pane label="已忽略" name="dismissed" />
  </el-tabs>

  <div v-loading="loading" class="draft-list">
    <div v-if="!loading && !items.length" class="draft-empty">
      <b>{{ emptyTitle }}</b>
      <p>{{ emptyHint }}</p>
      <el-button v-if="tab === 'pending'" type="primary" @click="$router.push('/agent')">去提问</el-button>
    </div>

    <article v-for="d in items" :key="d.id" class="draft-card">
      <header class="draft-card-head">
        <el-tag size="small" :type="d.kind === 'note' ? 'warning' : 'info'" effect="light">
          {{ d.kind === 'note' ? '主题笔记' : '问答' }}
        </el-tag>
        <h3>{{ d.question }}</h3>
        <time>{{ formatTime(d.created_at) }}</time>
      </header>

      <p class="draft-answer" :class="{ collapsed: !expanded.has(d.id) }">{{ d.answer }}</p>
      <button v-if="d.answer.length > 220" class="draft-toggle" type="button" @click="toggle(d.id)">
        {{ expanded.has(d.id) ? '收起' : '展开全文' }}
      </button>

      <div v-if="d.sources.length" class="draft-sources">
        <span class="draft-source-label">依据</span>
        <span v-for="s in d.sources.slice(0, 3)" :key="s.filename + '-' + s.chunk_index" class="draft-source">
          {{ s.filename }}<template v-if="s.chunk_index"> · 片段 {{ s.chunk_index }}</template>
        </span>
        <span v-if="d.sources.length > 3" class="draft-source-more">+{{ d.sources.length - 3 }}</span>
      </div>

      <footer class="draft-card-foot">
        <span class="draft-unit">{{ d.unit_ids.length }} 个知识库</span>
        <template v-if="d.status === 'pending'">
          <el-button size="small" type="primary" :loading="acting === d.id" @click="accept(d)">
            采纳入库
          </el-button>
          <el-button size="small" text :disabled="acting === d.id" @click="dismiss(d)">忽略</el-button>
        </template>
        <template v-else-if="d.status === 'accepted'">
          <span class="draft-done">已入库 · 任务 {{ d.job_id }}</span>
        </template>
        <template v-else-if="d.status === 'merged'">
          <span class="draft-done">已合并进主题笔记</span>
        </template>
        <template v-else>
          <span class="draft-done">已忽略</span>
        </template>
      </footer>
    </article>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import axios from 'axios'
import { ElMessage, ElMessageBox } from 'element-plus'

interface DraftSource {
  filename: string
  doc_id?: string
  chunk_index: number
}

interface Draft {
  id: number
  kind: string
  status: string
  conversation_id: string
  unit_ids: string[]
  question: string
  answer: string
  sources: DraftSource[]
  doc_id: string
  job_id: string
  created_at: string
}

const items = ref<Draft[]>([])
const pending = ref(0)
const autoMin = ref(8)
const tab = ref('pending')
const loading = ref(false)
const distilling = ref(false)
const acting = ref(0)
const expanded = ref(new Set<number>())

const emptyTitle = computed(() => (tab.value === 'pending' ? '还没有待审核的沉淀' : '这一组是空的'))
const emptyHint = computed(() =>
  tab.value === 'pending'
    ? '在对话里问几个有资料依据的问题，回答会自动沉淀到这里。'
    : '切换上方的分组看看其他状态。',
)

function formatTime(value: string) {
  if (!value) return '—'
  return new Date(value).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

function toggle(id: number) {
  const next = new Set(expanded.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expanded.value = next
}

async function load() {
  loading.value = true
  try {
    const { data } = await axios.get('/api/v1/kb/drafts', { params: { status: tab.value, limit: 50 } })
    items.value = data?.items || []
    pending.value = data?.pending || 0
    autoMin.value = data?.auto_distill_min || 8
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '加载沉淀列表失败')
  } finally {
    loading.value = false
  }
}

async function accept(d: Draft) {
  try {
    await ElMessageBox.confirm(
      '采纳后这条内容会作为「问答沉淀」文档进入知识库，之后可以被检索和引用。请确认回答与依据无误。',
      '采纳入库',
      { confirmButtonText: '采纳入库', cancelButtonText: '再想想', type: 'info' },
    )
  } catch {
    return
  }
  acting.value = d.id
  try {
    const { data } = await axios.post(`/api/v1/kb/drafts/${d.id}/accept`, { id: d.id })
    ElMessage.success(`已加入索引队列（任务 ${data.job_id}），完成后即可检索`)
    await load()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '采纳失败')
  } finally {
    acting.value = 0
  }
}

async function dismiss(d: Draft) {
  acting.value = d.id
  try {
    await axios.post(`/api/v1/kb/drafts/${d.id}/dismiss`, { id: d.id })
    await load()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '操作失败')
  } finally {
    acting.value = 0
  }
}

async function distill() {
  distilling.value = true
  try {
    const { data } = await axios.post('/api/v1/kb/drafts/distill')
    if (data.merged) ElMessage.success(data.message)
    else ElMessage.info(data.message || '当前没有可整理的内容')
    tab.value = 'pending'
    await load()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '整理失败')
  } finally {
    distilling.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.draft-head-actions {
  display: flex;
  gap: 8px;
}

.draft-notice {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  padding: calc(10px * var(--fs-scale)) calc(14px * var(--fs-scale));
  margin-bottom: calc(16px * var(--fs-scale));
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-secondary);
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-md, 8px);
}

.draft-notice-sep {
  color: var(--text-muted);
}

.draft-tabs {
  margin-bottom: calc(8px * var(--fs-scale));
}

.draft-list {
  display: flex;
  flex-direction: column;
  gap: calc(12px * var(--fs-scale));
  min-height: 120px;
}

.draft-empty {
  padding: calc(48px * var(--fs-scale)) 24px;
  text-align: center;
  color: var(--text-secondary);
}

.draft-empty b {
  display: block;
  font-size: calc(15px * var(--fs-scale));
  font-weight: 500;
  color: var(--text);
  margin-bottom: 8px;
}

.draft-empty p {
  font-size: calc(13px * var(--fs-scale));
  margin-bottom: 16px;
}

.draft-card {
  padding: calc(14px * var(--fs-scale)) calc(16px * var(--fs-scale));
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg, 12px);
}

.draft-card-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin-bottom: calc(8px * var(--fs-scale));
}

.draft-card-head h3 {
  flex: 1;
  margin: 0;
  font-size: calc(14px * var(--fs-scale));
  font-weight: 500;
  line-height: 1.5;
}

.draft-card-head time {
  flex-shrink: 0;
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-muted);
}

.draft-answer {
  margin: 0 0 calc(8px * var(--fs-scale));
  font-size: calc(13px * var(--fs-scale));
  line-height: 1.75;
  color: var(--text-secondary);
  white-space: pre-wrap;
  word-break: break-word;
}

.draft-answer.collapsed {
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.draft-toggle {
  padding: 0;
  margin-bottom: calc(8px * var(--fs-scale));
  font-size: calc(12px * var(--fs-scale));
  color: var(--accent);
  background: none;
  border: none;
  cursor: pointer;
}

.draft-sources {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
  margin-bottom: calc(10px * var(--fs-scale));
}

.draft-source-label {
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-muted);
}

.draft-source {
  padding: 2px 8px;
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-secondary);
  background: var(--bg-subtle);
  border-radius: 4px;
}

.draft-source-more {
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.draft-card-foot {
  display: flex;
  align-items: center;
  gap: 10px;
}

.draft-unit {
  flex: 1;
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-muted);
}

.draft-done {
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-muted);
}
</style>
