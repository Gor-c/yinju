<template>
  <div class="page docs-page">
    <header class="page-head docs-page-head">
      <div>
        <div class="eyebrow">Documents</div>
        <h1>文档库</h1>
        <p class="subtitle">
          {{ loading ? '正在加载…' : '统一浏览所有知识库中的文档，查看索引状态、引用关系和失败任务。' }}
        </p>
      </div>
      <div class="head-actions docs-head-actions">
        <el-button :loading="loading" @click="load">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path d="M20 11a8 8 0 1 0-2.3 5.7M20 4v7h-7" />
          </svg>
          刷新
        </el-button>
        <el-button type="primary" @click="$router.push('/upload')">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 5v14M5 12h14" />
          </svg>
          上传文档
        </el-button>
      </div>
    </header>

    <div v-if="error" class="notice error">{{ error }}</div>

    <div class="docs-stats">
      <div class="docs-stat">
        <span class="docs-stat-icon">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M7 3h7l4 4v14H7zM14 3v5h5" /></svg>
        </span>
        <div><b>{{ total }}</b><span>文档总数</span></div>
      </div>
      <div class="docs-stat">
        <span class="docs-stat-icon">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M5 6h14M5 12h14M5 18h9" /></svg>
        </span>
        <div><b>{{ chunks }}</b><span>已索引文本块</span></div>
      </div>
      <div class="docs-stat" :class="{ warn: failed > 0 }">
        <span class="docs-stat-icon">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9" /><path d="M12 8v5m0 3h.01" /></svg>
        </span>
        <div><b>{{ failed }}</b><span>索引失败</span></div>
      </div>
      <div class="docs-stat" :class="{ warn: orphans > 0 }">
        <span class="docs-stat-icon">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M9 7H5v12h10v-4M14 4h6v6M20 4l-8 8" /></svg>
        </span>
        <div><b>{{ orphans }}</b><span>未关联文档</span></div>
      </div>
    </div>

    <section class="panel docs-panel">
      <div class="docs-panel-heading">
        <div>
          <div class="card-title">全部文档</div>
          <p>
            {{ hasActiveFilter ? `当前显示 ${items.length} 份文档` : `共 ${total} 份文档` }}
            <span v-if="failed"> · {{ failed }} 份需要处理</span>
          </p>
        </div>
        <span class="docs-sort-note">按最近更新排序</span>
      </div>
      <div class="docs-toolbar">
        <el-input
          v-model="keyword"
          class="docs-search"
          clearable
          placeholder="搜索文件名…"
          @keyup.enter="resetAndLoad"
          @clear="resetAndLoad"
        >
          <template #prefix>
            <svg class="docs-search-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
              <circle cx="11" cy="11" r="7" />
              <path d="m16.5 16.5 4 4" />
            </svg>
          </template>
        </el-input>
        <el-select v-model="unitFilter" class="docs-unit-filter" clearable placeholder="全部知识库" @change="resetAndLoad">
          <el-option v-for="u in units" :key="u.unit_id" :label="u.name" :value="u.unit_id" />
        </el-select>
        <el-radio-group v-model="stateFilter" class="docs-state-filter" size="small" @change="resetAndLoad">
          <el-radio-button value="">全部</el-radio-button>
          <el-radio-button value="indexed">可用</el-radio-button>
          <el-radio-button value="failed">需处理</el-radio-button>
        </el-radio-group>
        <el-button v-if="hasActiveFilter" link type="primary" size="small" @click="clearFilters">清空筛选</el-button>
      </div>

      <div v-loading="loading" class="docs-list">
        <div v-if="!loading && !items.length" class="docs-empty">
          <span class="docs-empty-icon">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
              <path d="M7 3h7l4 4v14H7zM14 3v5h5M9.5 13h5M9.5 16.5h3" />
            </svg>
          </span>
          <b>{{ hasActiveFilter ? '没有找到匹配的文档' : '文档库还是空的' }}</b>
          <p>{{ hasActiveFilter ? '试试更换文件名或知识库筛选条件。' : '上传第一份文档，完成解析后即可在 Agent 中检索。' }}</p>
          <el-button v-if="hasActiveFilter" @click="clearFilters">清空筛选</el-button>
          <el-button v-else type="primary" @click="$router.push('/upload')">上传第一份文档</el-button>
        </div>

        <article
          v-for="row in items"
          :key="`${row.state}-${row.doc_id || row.job_id}-${row.filename}`"
          class="doc-card"
          :class="{ failed: row.state !== 'indexed' }"
        >
          <span class="doc-file-type" :class="{ failed: row.state !== 'indexed' }">{{ fileExtension(row.filename) }}</span>
          <div class="doc-card-main">
            <div class="doc-card-title">
              <h3 :title="row.filename">{{ row.filename }}</h3>
              <el-tag size="small" :type="stateType(row)" effect="light">{{ stateLabel(row) }}</el-tag>
            </div>
            <div class="doc-card-meta">
              <span>{{ row.doc_type || '未分类' }}</span>
              <span v-if="row.state === 'indexed'">{{ row.chunk_count }} 个文本块</span>
              <span v-else>{{ row.phase === 'parse' ? '正文解析阶段失败' : '向量索引阶段失败' }}</span>
              <span :title="fullTime(row.indexed_at)">{{ formatTime(row.indexed_at) }}</span>
            </div>
            <div class="doc-card-refs">
              <span class="doc-ref-label">所属知识库</span>
              <template v-if="row.units.length">
                <el-tag v-for="u in row.units" :key="u.unit_id" size="small" effect="plain">{{ u.name }}</el-tag>
              </template>
              <span v-else class="doc-orphan">未关联任何知识库</span>
            </div>
          </div>
          <div class="doc-card-actions">
            <template v-if="row.state === 'indexed'">
              <el-button size="small" :disabled="!row.units.length" @click="openSource(row)">预览</el-button>
              <el-button size="small" :disabled="!row.units.length" @click="openChunks(row)">索引片段</el-button>
              <el-button size="small" :disabled="!row.units.length" @click="openGraph(row)">图谱</el-button>
              <el-button size="small" type="danger" plain @click="remove(row)">删除</el-button>
            </template>
            <template v-else>
              <el-button size="small" type="primary" :disabled="busy" @click="retry(row)">重新索引</el-button>
              <el-button size="small" @click="openLatestJob(row)">查看任务</el-button>
            </template>
          </div>
        </article>
      </div>

      <div v-if="total > 0" class="docs-pagination">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[20, 50, 100]"
          layout="total, sizes, prev, pager, next"
          background
          @current-change="load"
          @size-change="onPageSizeChange"
        />
      </div>
    </section>

    <el-drawer v-model="chunkDrawer" :title="`索引片段 · ${chunkDoc?.filename || ''}`" size="560px">
      <div v-if="chunkDoc" class="chunk-meta">
        <div><span>文档 ID</span><b>{{ chunkDoc.doc_id }}</b></div>
        <div><span>类型</span><b>{{ chunkDoc.doc_type || '未分类' }}</b></div>
        <div><span>文本块</span><b>{{ chunkTotal }}</b></div>
        <div><span>索引时间</span><b>{{ fullTime(chunkDoc.indexed_at) }}</b></div>
        <div><span>引用知识库</span><b>{{ chunkDoc.units.map((u) => u.name).join('、') }}</b></div>
      </div>

      <div v-if="chunkError" class="notice error">{{ chunkError }}</div>
      <div v-loading="chunkLoading" class="chunk-list">
        <div v-for="c in chunkItems" :key="c.index" class="chunk-card">
          <div class="chunk-head">
            <b>文本块 #{{ c.index }}</b>
            <span class="muted">{{ chunkDoc?.doc_type }}</span>
          </div>
          <p class="chunk-text" :class="{ collapsed: !c.expanded && c.content.length > 360, expanded: c.expanded }">{{ c.content }}</p>
          <button
            v-if="c.content.length > 360"
            class="chunk-toggle"
            type="button"
            @click="c.expanded = !c.expanded"
          >
            {{ c.expanded ? '收起原文' : '展开全文' }}
          </button>
        </div>
        <div v-if="!chunkLoading && !chunkItems.length" class="muted" style="padding: 24px 0">
          暂无索引片段（可能是文档刚被清空或向量尚未写入）。
        </div>
      </div>

      <div v-if="chunkItems.length" class="chunk-pager">
        <el-button size="small" :disabled="chunkOffset === 0 || chunkLoading" @click="fetchChunks(Math.max(0, chunkOffset - chunkPageSize))">
          上一页
        </el-button>
        <span class="muted">{{ chunkOffset + 1 }} – {{ chunkOffset + chunkItems.length }} / {{ chunkTotal }}</span>
        <el-button size="small" :disabled="!chunkHasMore || chunkLoading" @click="fetchChunks(chunkOffset + chunkPageSize)">
          下一页
        </el-button>
      </div>
    </el-drawer>

    <el-dialog
      class="docs-preview-dialog"
      v-model="previewDialog"
      :title="previewDoc?.filename || '文档预览'"
      width="min(1180px, 96vw)"
      top="3vh"
      destroy-on-close
    >
      <DocumentPreview
        v-if="previewDialog && previewDoc && previewSrc"
        :src="previewSrc"
        :filename="previewDoc.filename"
      />
      <template #footer>
        <div class="source-footer docs-preview-footer">
          <span v-if="previewDoc">
            {{ previewDoc.doc_type || '未分类' }}
            · {{ previewDoc.units.map((u) => u.name).join('、') }}
          </span>
          <div class="docs-preview-actions">
            <a
              v-if="previewSrc"
              class="el-button el-button--primary el-button--small"
              :href="previewSrc + '?download=1'"
              target="_blank"
              rel="noopener"
            >下载原件</a>
            <el-button size="small" @click="previewDialog = false">关闭</el-button>
          </div>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import axios from 'axios'
import { ElMessage, ElMessageBox } from 'element-plus'
import DocumentPreview from '../components/DocumentPreview.vue'

interface DocUnit {
  unit_id: string
  name: string
}
interface DocItem {
  state: 'indexed' | 'failed'
  doc_id?: string
  job_id?: string
  phase?: string
  filename: string
  doc_type: string
  chunk_count: number
  indexed_at?: string
  ref_count: number
  units: DocUnit[]
}
interface UnitOption {
  unit_id: string
  name: string
}
interface ChunkItem {
  index: number
  content: string
  expanded?: boolean
}

const router = useRouter()
const items = ref<DocItem[]>([])
const units = ref<UnitOption[]>([])
const keyword = ref('')
const unitFilter = ref('')
const stateFilter = ref('')
const total = ref(0)
const failed = ref(0)
const chunks = ref(0)
const orphans = ref(0)
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const previewDialog = ref(false)
const previewDoc = ref<DocItem | null>(null)

// 索引片段抽屉：与知识库详情页同一接口，按文档维度分页读取。
const chunkDrawer = ref(false)
const chunkLoading = ref(false)
const chunkError = ref('')
const chunkDoc = ref<DocItem | null>(null)
const chunkItems = ref<ChunkItem[]>([])
const chunkOffset = ref(0)
const chunkTotal = ref(0)
const chunkHasMore = ref(false)
const chunkPageSize = 10
const previewSrc = computed(() => {
  const unit = previewDoc.value?.units[0]
  if (!unit || !previewDoc.value) return ''
  return `/api/v1/units/${encodeURIComponent(unit.unit_id)}/documents/${encodeURIComponent(previewDoc.value.filename)}/file`
})

// 列表分页：总数与状态筛选都在服务端完成，翻页不会丢掉「需处理」的文档。
const page = ref(1)
const pageSize = ref(20)

const hasActiveFilter = computed(() => !!keyword.value.trim() || !!unitFilter.value || !!stateFilter.value)

function fileExtension(filename: string) {
  const ext = (filename.split('.').pop() || 'FILE').toUpperCase()
  return ext.length > 5 ? ext.slice(0, 4) : ext
}

function formatTime(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const pad = (n: number) => String(n).padStart(2, '0')
  // 列表里用紧凑格式，完整时间靠 title 悬浮查看。
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function fullTime(value?: string) {
  return value ? new Date(value).toLocaleString() : ''
}

function stateLabel(row: DocItem) {
  if (row.state === 'indexed') return '已索引'
  return row.phase === 'parse' ? '解析失败' : '索引失败'
}

function stateType(row: DocItem) {
  return row.state === 'indexed' ? 'success' : 'danger'
}

async function clearFilters() {
  keyword.value = ''
  unitFilter.value = ''
  stateFilter.value = ''
  await resetAndLoad()
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await axios.get('/api/v1/documents', {
      params: {
        q: keyword.value.trim(),
        unit_id: unitFilter.value,
        state: stateFilter.value,
        limit: pageSize.value,
        offset: (page.value - 1) * pageSize.value,
      },
    })
    items.value = res.data?.items || []
    total.value = res.data?.total || 0
    failed.value = res.data?.failed || 0
    chunks.value = res.data?.chunks || 0
    orphans.value = res.data?.orphans || 0
    // 筛选后总数变少，当前页可能超出范围，回退到第一页而不是显示空白。
    if (page.value > 1 && !items.value.length && total.value > 0) {
      page.value = 1
      return load()
    }
  } catch (e: any) {
    error.value = e.response?.data?.error || '加载文档库失败'
  } finally {
    loading.value = false
  }
}

function resetAndLoad() {
  page.value = 1
  return load()
}

function onPageSizeChange() {
  return resetAndLoad()
}

async function loadUnits() {
  try {
    const res = await axios.get('/api/v1/units')
    units.value = res.data || []
  } catch {
    // 知识库筛选是辅助功能，失败时不影响列表。
  }
}

function openSource(row: DocItem) {
  const unit = row.units[0]
  if (!unit) return
  previewDoc.value = row
  previewDialog.value = true
}

function openChunks(row: DocItem) {
  const unit = row.units[0]
  if (!row.doc_id || !unit) return
  chunkDoc.value = row
  chunkItems.value = []
  chunkOffset.value = 0
  chunkTotal.value = row.chunk_count || 0
  chunkError.value = ''
  chunkDrawer.value = true
  fetchChunks(0)
}

async function fetchChunks(offset: number) {
  const row = chunkDoc.value
  const unit = row?.units[0]
  if (!row || !unit) return
  chunkLoading.value = true
  chunkError.value = ''
  try {
    const base = `/api/v1/units/${encodeURIComponent(unit.unit_id)}/documents/${encodeURIComponent(row.filename)}/chunks`
    const { data } = await axios.get(base, { params: { offset, limit: chunkPageSize } })
    chunkItems.value = data?.chunks || []
    chunkOffset.value = data?.offset ?? offset
    chunkHasMore.value = !!data?.has_more
    if (data?.chunk_count) chunkTotal.value = data.chunk_count
  } catch (e: any) {
    chunkError.value = e.response?.data?.error || '读取索引片段失败'
  } finally {
    chunkLoading.value = false
  }
}

// 文档级知识图谱：跳进所属知识库的图谱页，并按 doc_id 过滤。
function openGraph(row: DocItem) {
  const unit = row.units[0]
  if (!unit || !row.doc_id) return
  router.push({ path: `/units/${unit.unit_id}`, query: { tab: 'graph', doc: row.doc_id } })
}

function openLatestJob(row: DocItem) {
  if (!row.job_id) return
  router.push({ path: '/upload', query: { job: row.job_id } })
}

async function retry(row: DocItem) {
  if (!row.job_id || busy.value) return
  busy.value = true
  try {
    await axios.post(`/api/v1/jobs/${encodeURIComponent(row.job_id)}/retry`)
    ElMessage.success('已重新排队')
    await load()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '重新索引失败')
  } finally {
    busy.value = false
  }
}

async function remove(row: DocItem) {
  if (!row.doc_id) return
  const refs = row.ref_count
  const suffix = refs > 1 ? `\n\n它当前被 ${refs} 个知识库引用，删除后这些知识库都将失去它。` : ''
  try {
    await ElMessageBox.confirm(
      `已索引的向量和源文件会一并删除。${suffix}`,
      `确定删除「${row.filename}」？`,
      {
        type: 'warning',
        confirmButtonText: '确认删除',
        cancelButtonText: '取消',
      },
    )
  } catch {
    return
  }
  try {
    await axios.delete(`/api/v1/documents/${encodeURIComponent(row.doc_id)}`)
    ElMessage.success('文档已删除')
    await load()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '删除失败')
  }
}

onMounted(async () => {
  await Promise.all([loadUnits(), load()])
})
</script>

<style scoped>
.docs-page {
  max-width: 1220px;
}

.docs-pagination {
  display: flex;
  justify-content: flex-end;
  padding: calc(14px * var(--fs-scale)) 4px 0;
}

.docs-page-head {
  align-items: flex-end;
}

.docs-head-actions :deep(.el-button svg) {
  width: 14px;
  height: 14px;
  margin-right: 4px;
}

.docs-stat {
  align-items: center;
  gap: 11px;
  padding: 13px 15px;
}

.docs-stat-icon {
  display: grid;
  width: 34px;
  height: 34px;
  flex: none;
  place-items: center;
  border-radius: 10px;
  background: var(--bg-subtle);
  color: var(--text-secondary);
}

.docs-stat-icon svg {
  width: 17px;
  height: 17px;
}

.docs-stat > div {
  display: flex;
  min-width: 0;
  flex-direction: column;
}

.docs-stat.warn .docs-stat-icon {
  background: color-mix(in srgb, #d97706 10%, var(--surface));
  color: #b45309;
}

.docs-panel {
  overflow: hidden;
  padding: 0;
}

.docs-panel-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding: 18px 20px 14px;
}

.docs-panel-heading .card-title {
  margin: 0;
  font-size: calc(15px * var(--fs-scale));
}

.docs-panel-heading p {
  margin: 4px 0 0;
  color: var(--text-muted);
  font-size: calc(11px * var(--fs-scale));
}

.docs-sort-note {
  flex: none;
  padding-top: 2px;
  color: var(--text-muted);
  font-size: calc(11px * var(--fs-scale));
}

.docs-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  padding: 12px 20px;
  border-top: 1px solid var(--border);
  border-bottom: 1px solid var(--border);
  background: var(--bg-subtle);
}

.docs-search {
  width: 280px;
}

.docs-search-icon {
  width: 14px;
  height: 14px;
  color: var(--text-muted);
}

.docs-unit-filter {
  width: 200px;
}

.docs-state-filter :deep(.el-radio-button__inner) {
  padding-right: 12px;
  padding-left: 12px;
}

.docs-list {
  min-height: 180px;
  padding: 0 20px 8px;
}

.doc-card {
  display: grid;
  grid-template-columns: 42px minmax(0, 1fr) auto;
  gap: 14px;
  align-items: center;
  padding: 16px 2px;
  border-bottom: 1px solid var(--border);
}

.doc-card:last-child {
  border-bottom: none;
}

.doc-card.failed {
  margin: 0 -10px;
  padding-right: 12px;
  padding-left: 12px;
  border-radius: 10px;
  background: color-mix(in srgb, var(--danger) 2.5%, var(--surface));
}

.doc-file-type {
  display: grid;
  width: 42px;
  height: 46px;
  place-items: center;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--bg-subtle);
  color: var(--text-secondary);
  font-size: calc(9px * var(--fs-scale));
  font-weight: 750;
  letter-spacing: 0.04em;
}

.doc-file-type.failed {
  border-color: color-mix(in srgb, var(--danger) 20%, var(--border));
  background: color-mix(in srgb, var(--danger) 6%, var(--surface));
  color: var(--danger);
}

.doc-card-main {
  display: grid;
  min-width: 0;
  gap: 6px;
}

.doc-card-title {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
}

.doc-card-title h3 {
  overflow: hidden;
  min-width: 0;
  margin: 0;
  color: var(--text);
  font-size: calc(13px * var(--fs-scale));
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.doc-card-title :deep(.el-tag) {
  flex: none;
}

.doc-card-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 5px 13px;
  color: var(--text-muted);
  font-size: calc(11px * var(--fs-scale));
}

.doc-card-meta span {
  position: relative;
}

.doc-card-meta span:not(:first-child)::before {
  position: absolute;
  left: -8px;
  color: var(--border-strong);
  content: '·';
}

.doc-card-refs {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 5px;
}

.doc-ref-label {
  margin-right: 2px;
  color: var(--text-muted);
  font-size: calc(10px * var(--fs-scale));
}

.doc-card-refs :deep(.el-tag) {
  max-width: 180px;
}

.doc-card-refs :deep(.el-tag__content) {
  overflow: hidden;
  text-overflow: ellipsis;
}

.doc-orphan {
  color: #b45309;
  font-size: calc(11px * var(--fs-scale));
}

.doc-card-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 6px;
}

.doc-card-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}

.docs-empty {
  display: flex;
  min-height: 260px;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  text-align: center;
}

.docs-empty-icon {
  display: grid;
  width: 48px;
  height: 48px;
  margin-bottom: 4px;
  place-items: center;
  border-radius: 14px;
  background: var(--bg-subtle);
  color: var(--text-muted);
}

.docs-empty-icon svg {
  width: 23px;
  height: 23px;
}

.docs-empty b {
  color: var(--text-secondary);
  font-size: calc(14px * var(--fs-scale));
  font-weight: 600;
}

.docs-empty p {
  margin: 0 0 6px;
  color: var(--text-muted);
  font-size: calc(12px * var(--fs-scale));
}

.doc-name {
  font-weight: 500;
  word-break: break-all;
}
.doc-id {
  font-size: calc(11.5px * var(--fs-scale));
  margin-top: 2px;
}
.ref-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.chunk-meta {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px 16px;
  padding: 12px 14px;
  margin-bottom: 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--bg-subtle);
  font-size: calc(12.5px * var(--fs-scale));
}
.chunk-meta div {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}
.chunk-meta span {
  color: var(--text-muted);
}
.chunk-meta b {
  font-weight: 550;
  color: var(--text-secondary);
  word-break: break-all;
}
.chunk-list {
  min-height: 120px;
}
.chunk-card {
  padding: 14px 0;
  border-bottom: 1px solid var(--border);
}
.chunk-card:last-child {
  border-bottom: none;
}
.chunk-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  font-size: calc(12.5px * var(--fs-scale));
}
.chunk-text {
  margin: 0;
  font-size: calc(13px * var(--fs-scale));
  line-height: 1.75;
  color: var(--text-secondary);
  white-space: pre-wrap;
  word-break: break-word;
}
.chunk-text.collapsed {
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 7;
}
.chunk-toggle {
  margin-top: 7px;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--accent);
  font-size: calc(11px * var(--fs-scale));
  cursor: pointer;
}
.chunk-pager {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-top: 14px;
  font-size: calc(12.5px * var(--fs-scale));
}
.docs-preview-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.docs-preview-footer > span {
  overflow: hidden;
  color: var(--text-muted);
  font-size: calc(11px * var(--fs-scale));
  text-overflow: ellipsis;
  white-space: nowrap;
}
.docs-preview-actions {
  display: flex;
  flex: none;
  align-items: center;
  gap: 8px;
}
@media (max-width: 720px) {
  .docs-page-head {
    align-items: flex-start;
  }
  .docs-search,
  .docs-unit-filter {
    width: 100%;
  }
  .docs-sort-note {
    display: none;
  }
  .docs-toolbar {
    padding: 12px 14px;
  }
  .docs-panel-heading,
  .docs-list {
    padding-right: 14px;
    padding-left: 14px;
  }
  .doc-card {
    grid-template-columns: 38px minmax(0, 1fr);
    gap: 12px;
    align-items: start;
  }
  .doc-file-type {
    width: 38px;
    height: 42px;
  }
  .doc-card-actions {
    grid-column: 2;
    justify-content: flex-start;
  }
  .doc-card-meta span:not(:first-child)::before {
    display: none;
  }
  .doc-card-meta {
    gap: 3px 10px;
  }
  .chunk-meta {
    grid-template-columns: 1fr;
  }
  .docs-preview-footer {
    align-items: flex-start;
    flex-direction: column;
  }
  .docs-preview-actions {
    width: 100%;
    justify-content: flex-end;
  }
}
</style>
