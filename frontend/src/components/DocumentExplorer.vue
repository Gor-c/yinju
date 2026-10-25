<template>
  <div class="doc-explorer" :class="{ dense }">
    <div class="de-toolbar">
      <el-input
        v-model="keyword"
        class="de-search"
        clearable
        placeholder="搜索文件名…"
        @keyup.enter="resetAndLoad"
        @clear="resetAndLoad"
      >
        <template #prefix>
          <svg class="de-search-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
            <circle cx="11" cy="11" r="7" />
            <path d="m16.5 16.5 4 4" />
          </svg>
        </template>
      </el-input>
      <el-radio-group v-model="stateFilter" size="small" @change="resetAndLoad">
        <el-radio-button value="">全部</el-radio-button>
        <el-radio-button value="indexed">可用</el-radio-button>
        <el-radio-button value="failed">需处理</el-radio-button>
      </el-radio-group>
      <span class="de-summary">
        {{ hasFilter ? `筛选出 ${total} 份` : `共 ${total} 份` }}
        <span v-if="failed"> · {{ failed }} 份需处理</span>
      </span>
      <el-button v-if="hasFilter" link type="primary" size="small" @click="clearFilters">清空筛选</el-button>
      <el-button link size="small" :loading="loading" @click="load">刷新</el-button>
    </div>

    <div v-loading="loading" class="de-list">
      <div v-if="!loading && !items.length" class="de-empty">
        <b>{{ hasFilter ? '没有找到匹配的文档' : emptyTitle }}</b>
        <p>{{ hasFilter ? '试试更换文件名或状态筛选条件。' : emptyHint }}</p>
        <el-button v-if="hasFilter" @click="clearFilters">清空筛选</el-button>
        <el-button v-else-if="!unitId" type="primary" @click="$router.push('/upload')">上传文档</el-button>
      </div>

      <article
        v-for="row in items"
        :key="`${row.state}-${row.doc_id || row.job_id}-${row.filename}`"
        class="de-card"
        :class="{ failed: row.state !== 'indexed' }"
      >
        <span class="de-type" :class="{ failed: row.state !== 'indexed' }">{{ fileExtension(row.filename) }}</span>
        <div class="de-main">
          <div class="de-title">
            <h3 :title="row.filename">{{ row.filename }}</h3>
            <el-tag size="small" :type="row.state === 'indexed' ? 'success' : 'danger'" effect="light">
              {{ row.state === 'indexed' ? '已索引' : (row.phase === 'parse' ? '解析失败' : '索引失败') }}
            </el-tag>
          </div>
          <div class="de-meta">
            <span>{{ row.doc_type || '未分类' }}</span>
            <span v-if="row.state === 'indexed'">{{ row.chunk_count }} 个文本块</span>
            <span :title="fullTime(row.indexed_at)">{{ formatTime(row.indexed_at) }}</span>
            <!-- 只有全局语义才关心「这份文档被几个库引用」 -->
            <span v-if="scope === 'global' && row.state === 'indexed'">被 {{ row.ref_count }} 个知识库引用</span>
          </div>
          <div v-if="scope === 'global' && row.state === 'indexed'" class="de-refs">
            <span class="de-ref-label">所属知识库</span>
            <template v-if="row.units.length">
              <el-tag v-for="u in row.units" :key="u.unit_id" size="small" effect="plain">{{ u.name }}</el-tag>
            </template>
            <span v-else class="de-orphan">未关联任何知识库</span>
          </div>
        </div>
        <div class="de-actions">
          <template v-if="row.state === 'indexed'">
            <el-button size="small" :disabled="!contextUnit(row)" @click="onPreview(row)">预览</el-button>
            <el-button size="small" :disabled="!contextUnit(row)" @click="openChunks(row)">索引片段</el-button>
            <el-button size="small" :disabled="!contextUnit(row)" @click="openGraph(row)">图谱</el-button>
            <!-- 删除语义由 scope 决定，绝不混用：unit 只解绑，global 才是彻底删除 -->
            <el-button size="small" type="danger" plain @click="remove(row)">
              {{ scope === 'unit' ? '从本库移除' : '删除' }}
            </el-button>
          </template>
          <template v-else>
            <el-button size="small" type="primary" :disabled="busy" @click="retry(row)">重新索引</el-button>
            <el-button size="small" @click="openLatestJob(row)">查看任务</el-button>
          </template>
        </div>
      </article>
    </div>

    <div v-if="total > 0" class="de-pagination">
      <el-pagination
        v-model:current-page="page"
        v-model:page-size="pageSize"
        :total="total"
        :page-sizes="[20, 50, 100]"
        layout="total, sizes, prev, pager, next"
        background
        @current-change="load"
        @size-change="resetAndLoad"
      />
    </div>

    <el-drawer v-model="chunkDrawer" :title="`索引片段 · ${chunkDoc?.filename || ''}`" size="560px">
      <div v-if="chunkDoc" class="de-chunk-meta">
        <div><span>文档 ID</span><b>{{ chunkDoc.doc_id }}</b></div>
        <div><span>类型</span><b>{{ chunkDoc.doc_type || '未分类' }}</b></div>
        <div><span>文本块</span><b>{{ chunkTotal }}</b></div>
        <div><span>索引时间</span><b>{{ fullTime(chunkDoc.indexed_at) }}</b></div>
      </div>
      <div v-if="chunkError" class="de-notice">{{ chunkError }}</div>
      <div v-loading="chunkLoading" class="de-chunk-list">
        <div v-for="c in chunkItems" :key="c.index" class="de-chunk">
          <div class="de-chunk-head"><b>文本块 #{{ c.index }}</b></div>
          <p class="de-chunk-text" :class="{ collapsed: !c.expanded && c.content.length > 360, expanded: c.expanded }">{{ c.content }}</p>
          <button v-if="c.content.length > 360" class="de-chunk-toggle" type="button" @click="c.expanded = !c.expanded">
            {{ c.expanded ? '收起原文' : '展开全文' }}
          </button>
        </div>
        <div v-if="!chunkLoading && !chunkItems.length" class="de-muted" style="padding: 24px 0">
          暂无索引片段（可能是文档刚被清空或向量尚未写入）。
        </div>
      </div>
      <div class="de-chunk-foot">
        <el-button size="small" :disabled="chunkOffset === 0 || chunkLoading" @click="fetchChunks(Math.max(0, chunkOffset - chunkPageSize))">
          上一页
        </el-button>
        <el-button size="small" :disabled="!chunkHasMore || chunkLoading" @click="fetchChunks(chunkOffset + chunkPageSize)">
          下一页
        </el-button>
      </div>
    </el-drawer>

    <el-dialog v-model="previewDialog" :title="previewDoc?.filename || '文档预览'" width="820px" top="6vh">
      <iframe v-if="previewSrc" :src="previewSrc" class="de-preview-frame" />
      <div v-else class="de-muted">该文档没有可直接预览的源文件。</div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import axios from 'axios'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useRouter } from 'vue-router'

interface DocUnit {
  unit_id: string
  name: string
}

interface DocItem {
  state: 'indexed' | 'failed'
  doc_id?: string
  job_id?: string
  filename: string
  doc_type: string
  chunk_count: number
  indexed_at: string
  phase?: string
  ref_count: number
  units: DocUnit[]
}

interface ChunkItem {
  index: number
  content: string
  expanded?: boolean
}

const props = withDefaults(
  defineProps<{
    /** 限定某个知识库；不传表示全局视图 */
    unitId?: string
    /** 决定删除语义：unit 只解除绑定，global 彻底删除 */
    scope?: 'unit' | 'global'
    dense?: boolean
    /**
     * builtin：组件内用 iframe 预览源文件；
     * emit：把预览交回父组件（知识库详情页的预览带全文检索与高亮，
     * 组件里做不到，硬接管会导致功能回退）。
     */
    previewMode?: 'builtin' | 'emit'
    emptyTitle?: string
    emptyHint?: string
  }>(),
  {
    unitId: '',
    scope: 'global',
    dense: false,
    previewMode: 'builtin',
    emptyTitle: '文档库还是空的',
    emptyHint: '上传第一份文档，完成解析后即可检索。',
  },
)

const emit = defineEmits<{ (e: 'changed'): void; (e: 'preview', row: DocItem): void }>()
const router = useRouter()

const items = ref<DocItem[]>([])
const total = ref(0)
const failed = ref(0)
const keyword = ref('')
const stateFilter = ref('')
const page = ref(1)
const pageSize = ref(20)
const loading = ref(false)
const busy = ref(false)

const hasFilter = computed(() => !!keyword.value.trim() || !!stateFilter.value)

const chunkDrawer = ref(false)
const chunkLoading = ref(false)
const chunkError = ref('')
const chunkDoc = ref<DocItem | null>(null)
const chunkItems = ref<ChunkItem[]>([])
const chunkOffset = ref(0)
const chunkTotal = ref(0)
const chunkHasMore = ref(false)
const chunkPageSize = 10

const previewDialog = ref(false)
const previewDoc = ref<DocItem | null>(null)

// 单库视图下当前库就是上下文；全局视图下退回第一个引用库（文档是全局实体）。
// 注意：文档库页「按知识库筛选」时也传了 unitId，但 scope 仍是 global，
// 所以这里要用 scope 判断「谁是需要上下文的那一个」，而不是看 unitId 是否为空。
function contextUnit(row: DocItem): string {
  if (props.scope === 'unit') return props.unitId || ''
  return row.units[0]?.unit_id || props.unitId || ''
}

const previewSrc = computed(() => {
  const unit = previewDoc.value ? contextUnit(previewDoc.value) : ''
  if (!unit || !previewDoc.value) return ''
  return `/api/v1/units/${encodeURIComponent(unit)}/documents/${encodeURIComponent(previewDoc.value.filename)}/file`
})

function fileExtension(filename: string) {
  const ext = (filename.split('.').pop() || 'FILE').toUpperCase()
  return ext.length > 5 ? ext.slice(0, 4) : ext
}

function formatTime(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function fullTime(value?: string) {
  return value ? new Date(value).toLocaleString() : ''
}

async function load() {
  loading.value = true
  try {
    const { data } = await axios.get('/api/v1/documents', {
      params: {
        q: keyword.value.trim(),
        unit_id: props.unitId || '',
        state: stateFilter.value,
        limit: pageSize.value,
        offset: (page.value - 1) * pageSize.value,
      },
    })
    items.value = data?.items || []
    total.value = data?.total || 0
    failed.value = data?.failed || 0
    if (page.value > 1 && !items.value.length && total.value > 0) {
      page.value = 1
      return load()
    }
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '加载文档列表失败')
  } finally {
    loading.value = false
  }
}

function resetAndLoad() {
  page.value = 1
  return load()
}

async function clearFilters() {
  keyword.value = ''
  stateFilter.value = ''
  await resetAndLoad()
}

async function remove(row: DocItem) {
  // 两种语义必须分开提示：说错一句就可能让人删掉别的库还在用的文档。
  const message =
    props.scope === 'unit'
      ? `将「${row.filename}」从当前知识库移除。文档本身仍保留在「文档库」中（未关联任何知识库），可重新绑定或在那里彻底删除。`
      : `将彻底删除「${row.filename}」：解除全部知识库关联、清理已索引向量与源文件。此操作不可撤销。`
  try {
    await ElMessageBox.confirm(message, props.scope === 'unit' ? '从本库移除' : '删除文档', {
      confirmButtonText: props.scope === 'unit' ? '移除' : '删除',
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return
  }
  busy.value = true
  try {
    if (props.scope === 'unit') {
      // detach_only 是关键：默认行为会在「没有其他库引用」时连带清理向量与源文件，
      // 而详情页只该管绑定关系，不能顺手销毁全局资产。
      await axios.delete(
        `/api/v1/units/${encodeURIComponent(contextUnit(row))}/documents/${encodeURIComponent(row.filename)}`,
        { params: { detach_only: 1 } },
      )
      ElMessage.success('已从本库移除，文档仍保留在文档库')
    } else {
      await axios.delete(`/api/v1/documents/${encodeURIComponent(row.doc_id || '')}`)
      ElMessage.success('文档已删除')
    }
    await load()
    emit('changed')
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '删除失败')
  } finally {
    busy.value = false
  }
}

async function retry(row: DocItem) {
  busy.value = true
  try {
    await axios.post(`/api/v1/jobs/${encodeURIComponent(row.job_id || '')}/retry`)
    ElMessage.success('已重新排队，稍后刷新查看进度')
    await load()
    emit('changed')
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '重新索引失败')
  } finally {
    busy.value = false
  }
}

function openLatestJob(row: DocItem) {
  if (row.job_id) router.push({ path: `/units/${encodeURIComponent(contextUnit(row))}`, query: { tab: 'documents' } })
}

function onPreview(row: DocItem) {
  if (props.previewMode === 'emit') {
    emit('preview', row)
    return
  }
  previewDoc.value = row
  previewDialog.value = true
}

function openGraph(row: DocItem) {
  router.push({ path: `/units/${encodeURIComponent(contextUnit(row))}`, query: { tab: 'graph', doc: row.doc_id || '' } })
}

function openChunks(row: DocItem) {
  chunkDoc.value = row
  chunkItems.value = []
  chunkOffset.value = 0
  chunkHasMore.value = false
  chunkError.value = ''
  chunkDrawer.value = true
  fetchChunks(0)
}

async function fetchChunks(offset: number) {
  if (!chunkDoc.value) return
  const unit = contextUnit(chunkDoc.value)
  if (!unit) return
  chunkLoading.value = true
  try {
    const base = `/api/v1/units/${encodeURIComponent(unit)}/documents/${encodeURIComponent(chunkDoc.value.filename)}/chunks`
    const { data } = await axios.get(base, { params: { offset, limit: chunkPageSize } })
    const list = (data?.items || data || []).map((c: any) => ({ ...c, expanded: false }))
    chunkItems.value = Array.isArray(list) ? list : []
    chunkOffset.value = data?.offset ?? offset
    chunkTotal.value = data?.total ?? chunkItems.value.length
    chunkHasMore.value = chunkItems.value.length >= chunkPageSize
    if (!chunkTotal.value) chunkTotal.value = chunkItems.value.length
  } catch (e: any) {
    chunkError.value = e.response?.data?.error || '加载索引片段失败'
  } finally {
    chunkLoading.value = false
  }
}

onMounted(load)

// 文档库页用同一个组件做「按知识库筛选」：筛选值从父组件传进来，变化即重查。
watch(
  () => props.unitId,
  () => resetAndLoad(),
)

defineExpose({ reload: load })
</script>

<style scoped>
.doc-explorer {
  display: flex;
  flex-direction: column;
  gap: calc(12px * var(--fs-scale));
}

.de-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

.de-search {
  max-width: 280px;
}

.de-search-icon {
  width: 14px;
  height: 14px;
  color: var(--text-muted);
}

.de-summary {
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-secondary);
}

.de-list {
  display: flex;
  flex-direction: column;
  gap: calc(10px * var(--fs-scale));
  min-height: 100px;
}

.de-empty {
  padding: calc(40px * var(--fs-scale)) 20px;
  text-align: center;
  color: var(--text-secondary);
}

.de-empty b {
  display: block;
  margin-bottom: 8px;
  font-size: calc(14px * var(--fs-scale));
  font-weight: 500;
  color: var(--text);
}

.de-empty p {
  margin: 0 0 14px;
  font-size: calc(13px * var(--fs-scale));
}

.de-card {
  display: flex;
  gap: 12px;
  padding: calc(12px * var(--fs-scale)) calc(14px * var(--fs-scale));
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg, 12px);
}

.dense .de-card {
  padding: calc(10px * var(--fs-scale)) calc(12px * var(--fs-scale));
}

.de-card.failed {
  border-color: var(--danger);
}

.de-type {
  flex: 0 0 auto;
  align-self: flex-start;
  padding: 2px 6px;
  font-size: calc(10.5px * var(--fs-scale));
  font-weight: 500;
  color: var(--accent);
  background: var(--accent-soft);
  border-radius: 4px;
}

.de-type.failed {
  color: var(--danger);
  background: transparent;
  border: 1px solid var(--danger);
}

.de-main {
  flex: 1;
  min-width: 0;
}

.de-title {
  display: flex;
  align-items: center;
  gap: 8px;
}

.de-title h3 {
  margin: 0;
  font-size: calc(13.5px * var(--fs-scale));
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.de-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 4px;
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-muted);
}

.de-refs {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  margin-top: 6px;
}

.de-ref-label {
  font-size: calc(11.5px * var(--fs-scale));
  color: var(--text-muted);
}

.de-orphan {
  font-size: calc(11.5px * var(--fs-scale));
  color: var(--warning);
}

.de-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  gap: 6px;
}

.de-pagination {
  display: flex;
  justify-content: flex-end;
}

.de-chunk-meta {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px 16px;
  margin-bottom: 12px;
  font-size: calc(12.5px * var(--fs-scale));
}

.de-chunk-meta span {
  display: block;
  color: var(--text-muted);
}

.de-chunk-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.de-chunk {
  padding: 10px 12px;
  background: var(--bg-subtle);
  border-radius: 8px;
}

.de-chunk-head {
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-secondary);
  margin-bottom: 6px;
}

.de-chunk-text {
  margin: 0;
  font-size: calc(12.5px * var(--fs-scale));
  line-height: 1.75;
  white-space: pre-wrap;
  word-break: break-word;
}

.de-chunk-text.collapsed {
  display: -webkit-box;
  -webkit-line-clamp: 5;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.de-chunk-toggle {
  margin-top: 6px;
  padding: 0;
  font-size: calc(12px * var(--fs-scale));
  color: var(--accent);
  background: none;
  border: none;
  cursor: pointer;
}

.de-chunk-foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 12px;
}

.de-preview-frame {
  width: 100%;
  height: 70vh;
  border: 1px solid var(--border);
  border-radius: 8px;
}

.de-notice {
  padding: 8px 10px;
  margin-bottom: 10px;
  font-size: calc(12.5px * var(--fs-scale));
  color: var(--danger);
  background: var(--bg-subtle);
  border-radius: 6px;
}

.de-muted {
  color: var(--text-muted);
  font-size: calc(13px * var(--fs-scale));
}
</style>
