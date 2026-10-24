<template>
  <div class="page tree-page" v-loading="loading">
    <router-link to="/units" class="back-link">← 返回知识库</router-link>

    <header class="page-head tree-head">
      <div>
        <div class="eyebrow">Outline</div>
        <h1>内容目录</h1>
        <p class="subtitle">
          {{ unitName }} · {{ docs.length }} 份文档 · {{ totalChapters }} 个章节 · {{ totalChunks }} 个片段
        </p>
      </div>
      <div class="head-actions">
        <el-input v-model="keyword" class="tree-search" size="small" clearable placeholder="搜索章节或片段内容">
          <template #prefix>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="11" cy="11" r="7" /><path d="m16.5 16.5 4 4" /></svg>
          </template>
        </el-input>
        <el-button size="small" @click="toggleAll">{{ allExpanded ? '全部收起' : '全部展开' }}</el-button>
        <el-button size="small" type="primary" @click="goChat">去对话</el-button>
      </div>
    </header>

    <div v-if="error" class="notice error">{{ error }}</div>

    <div class="tree-layout">
      <!-- 左：四级树。知识库 → 文档 → 章节 → 片段，缩进表达层级。 -->
      <section class="tree-pane">
        <div v-if="!docs.length && !loading" class="tree-empty">
          这个知识库还没有可展示的内容。先上传文档。
        </div>

        <div v-else class="tree-rows">
          <button
            v-for="row in rows"
            :key="row.node.key"
            type="button"
            class="tree-row"
            :class="[row.node.kind, { active: selected?.key === row.node.key }]"
            :style="{ paddingLeft: 10 + row.depth * 16 + 'px' }"
            @click="pick(row.node)"
          >
            <span
              class="tree-caret"
              :class="{ open: row.open, leaf: !row.node.children.length }"
              @click.stop="row.node.children.length && toggle(row.node.key)"
            >
              <svg v-if="row.node.children.length" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
                <path d="m9 6 6 6-6 6" />
              </svg>
            </span>

            <svg v-if="row.node.kind === 'doc'" class="tree-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
              <path d="M7 3h7l4 4v14H7zM14 3v5h5" />
            </svg>
            <span v-else-if="row.node.kind === 'chunk'" class="tree-block">#</span>

            <span class="tree-label" :class="{ major: row.node.level === 1 }">{{ row.node.label }}</span>
            <span v-if="row.node.badge" class="tree-badge">{{ row.node.badge }}</span>
            <span v-if="row.node.chunkIndex !== null && row.node.kind === 'chapter'" class="tree-chunk-no">
              #{{ row.node.chunkIndex }}
            </span>
          </button>
        </div>
      </section>

      <!-- 右：选中节点的原文。点哪里就出哪里，不用再去翻文档。 -->
      <aside class="tree-detail">
        <template v-if="selected">
          <div class="td-crumb">
            <span>{{ selected.docName }}</span>
            <template v-if="selected.kind !== 'doc'">
              <span class="td-sep">›</span>
              <span>{{ selected.kind === 'chunk' ? `第 ${selected.chunkIndex} 块` : '章节' }}</span>
            </template>
          </div>
          <h2 class="td-title">{{ selected.label }}</h2>
          <p class="td-meta">
            <template v-if="selected.kind === 'doc'">
              {{ selected.badge }} · {{ selected.children.length }} 个{{ selected.children[0]?.kind === 'chapter' ? '章节' : '片段' }}
            </template>
            <template v-else>
              索引片段 #{{ selected.chunkIndex }}
              <span v-if="selected.level">· 目录层级 L{{ selected.level }}</span>
            </template>
          </p>

          <div v-if="selected.kind === 'doc'" class="td-actions">
            <p class="td-hint">展开左侧节点，可以看到这份文档被切成了哪些块、每块讲了什么。</p>
            <el-button size="small" @click="$router.push(`/units/${unitId}`)">管理文档</el-button>
          </div>

          <template v-else>
            <div class="td-actions">
              <el-button v-if="selected.kind === 'chapter'" size="small" type="primary" @click="ask(selected.label)">
                在对话中提问
              </el-button>
              <el-button size="small" @click="openDocument">打开文档</el-button>
            </div>

            <div class="td-content" v-loading="contentLoading">
              <p v-if="skippedChars" class="td-skip">
                该片段开头还有 {{ skippedChars }} 字前置内容（目录或封面），已从本章标题处显示。
              </p>
              <p v-if="displayContent">{{ displayContent }}</p>
              <p v-else-if="!contentLoading" class="muted">这一段没有可读文本。</p>
            </div>
          </template>
        </template>

        <div v-else class="tree-detail-empty">
          <p>选择左侧任意章节或片段，这里显示它对应的原文。</p>
        </div>
      </aside>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import axios from 'axios'
import { useRoute, useRouter } from 'vue-router'

interface Entry {
  title: string
  level: number
  chunk_index: number
  doc_id: string
}

interface Chunk {
  index: number
  preview: string
}

interface Doc {
  doc_id: string
  filename: string
  doc_type: string
  chunk_count: number
  entries: Entry[]
  chunks: Chunk[]
}

interface Node {
  key: string
  kind: 'doc' | 'chapter' | 'chunk'
  label: string
  docId: string
  docName: string
  chunkIndex: number | null
  level?: number
  badge?: string
  preview?: string
  children: Node[]
}

interface Row {
  node: Node
  depth: number
  open: boolean
}

const route = useRoute()
const router = useRouter()
const unitId = computed(() => String(route.params.id || ''))

const loading = ref(false)
const error = ref('')
const docs = ref<Doc[]>([])
const totalChapters = ref(0)
const totalChunks = ref(0)
const unitName = ref('知识库')

const keyword = ref('')
const expanded = ref<Set<string>>(new Set())
const selected = ref<Node | null>(null)
const contentText = ref('')
const contentLoading = ref(false)

// 1800 字一块，块内可能既有上一章的结尾也有本章标题。选中章节时
// 从标题处开始显示，否则用户点「总则」看到的是一屏目录页。
const sliced = computed(() => {
  const text = contentText.value
  const title = selected.value?.kind === 'chapter' ? selected.value.label : ''
  if (!text || !title) return { text, skipped: 0 }
  const at = text.indexOf(title)
  if (at <= 0) return { text, skipped: 0 }
  return { text: text.slice(at), skipped: at }
})

const displayContent = computed(() => sliced.value.text)
const skippedChars = computed(() => sliced.value.skipped)

const contentCache = new Map<string, string>()

const nodes = computed<Node[]>(() => buildNodes(docs.value))

const filteredNodes = computed(() => {
  const q = keyword.value.trim()
  if (!q) return nodes.value
  return filterTree(nodes.value, q.toLowerCase())
})

const rows = computed<Row[]>(() => {
  const searching = !!keyword.value.trim()
  const out: Row[] = []
  const walk = (list: Node[], depth: number) => {
    for (const node of list) {
      const open = searching || expanded.value.has(node.key)
      out.push({ node, depth, open })
      if (open && node.children.length) walk(node.children, depth + 1)
    }
  }
  walk(filteredNodes.value, 0)
  return out
})

// 只统计「有子节点」的节点，否则永远达不到全部展开。
const expandableCount = computed(() => {
  const walk = (list: Node[]): number =>
    list.reduce((sum, n) => sum + (n.children.length ? 1 : 0) + walk(n.children), 0)
  return walk(nodes.value)
})

const allExpanded = computed(() => expandableCount.value > 0 && expanded.value.size >= expandableCount.value)

// 层级靠缩进表达；同一份文档内按 level 用栈挂父子，避免出现 1→3 跳级时错挂。
function buildNodes(list: Doc[]): Node[] {
  return list.map((doc) => {
    const docNode: Node = {
      key: `d:${doc.doc_id}`,
      kind: 'doc',
      label: doc.filename,
      docId: doc.doc_id,
      docName: doc.filename,
      chunkIndex: null,
      badge: `${doc.chunk_count} 片段`,
      children: [],
    }
    if (doc.entries.length) {
      const stack: Node[] = []
      for (const entry of doc.entries) {
        const node: Node = {
          key: `c:${doc.doc_id}:${entry.chunk_index}:${entry.title}`,
          kind: 'chapter',
          label: entry.title,
          docId: doc.doc_id,
          docName: doc.filename,
          chunkIndex: entry.chunk_index,
          level: entry.level,
          children: [],
        }
        while (stack.length && (stack[stack.length - 1].level || 0) >= entry.level) stack.pop()
        const parent = stack[stack.length - 1]
        ;(parent ? parent.children : docNode.children).push(node)
        stack.push(node)
      }
    } else {
      // 没有条款结构的文档（表格、扫描件）退化成片段列表，仍能逐块查看。
      docNode.children = doc.chunks.map((chunk) => ({
        key: `k:${doc.doc_id}:${chunk.index}`,
        kind: 'chunk' as const,
        label: chunk.preview || `第 ${chunk.index} 块`,
        docId: doc.doc_id,
        docName: doc.filename,
        chunkIndex: chunk.index,
        preview: chunk.preview,
        children: [],
      }))
    }
    return docNode
  })
}

function filterTree(list: Node[], q: string): Node[] {
  const out: Node[] = []
  for (const node of list) {
    const children = filterTree(node.children, q)
    const hit = node.label.toLowerCase().includes(q) || (node.preview || '').toLowerCase().includes(q)
    if (hit || children.length) {
      out.push({ ...node, children: hit ? node.children : children })
    }
  }
  return out
}

function toggle(key: string) {
  const next = new Set(expanded.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  expanded.value = next
}

function toggleAll() {
  if (allExpanded.value) {
    expanded.value = new Set()
    return
  }
  const next = new Set<string>()
  const walk = (list: Node[]) => {
    for (const n of list) {
      if (n.children.length) next.add(n.key)
      walk(n.children)
    }
  }
  walk(nodes.value)
  expanded.value = next
}

// 点整行只负责选中；展开/收起交给前面的箭头，两件事分开，
// 免得「看一眼内容」顺手把树折起来。
function pick(node: Node) {
  selected.value = node
  if (node.kind === 'doc') {
    contentText.value = ''
    return
  }
  loadContent(node)
}

// 正文按需取：树里只带 140 字预览，避免大库一次传几百 KB。
async function loadContent(node: Node) {
  if (node.chunkIndex === null) return
  const key = `${node.docId}:${node.chunkIndex}`
  const cached = contentCache.get(key)
  if (cached !== undefined) {
    contentText.value = cached
    return
  }
  contentLoading.value = true
  contentText.value = node.preview || ''
  try {
    const { data } = await axios.get(
      `/api/v1/units/${unitId.value}/documents/${encodeURIComponent(node.docName)}/chunks`,
      { params: { offset: node.chunkIndex, limit: 1 } },
    )
    const text = data.chunks?.[0]?.content || ''
    contentCache.set(key, text)
    if (selected.value?.key === node.key) contentText.value = text
  } catch {
    contentText.value = node.preview || ''
  } finally {
    contentLoading.value = false
  }
}

function ask(title: string) {
  router.push({ path: '/agent', query: { unit: unitId.value, q: `「${title}」主要讲了什么？` } })
}

function openDocument() {
  router.push(`/units/${unitId.value}`)
}

function goChat() {
  router.push({ path: '/agent', query: { unit: unitId.value } })
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [tree, units] = await Promise.all([
      axios.get(`/api/v1/units/${unitId.value}/tree`),
      axios.get('/api/v1/units').catch(() => ({ data: [] })),
    ])
    docs.value = tree.data.documents || []
    totalChapters.value = tree.data.total_chapters || 0
    totalChunks.value = tree.data.total_chunks || 0

    const match = (units.data || []).find((u: any) => u.unit_id === unitId.value)
    if (match) unitName.value = match.name

    // 默认展开文档、收起章节：先看到「有几份文件、各自几章」，再按需下钻。
    const next = new Set<string>()
    for (const d of docs.value) next.add(`d:${d.doc_id}`)
    expanded.value = next

    const firstChapter = firstSelectable()
    if (firstChapter) pick(firstChapter)
  } catch (e: any) {
    error.value = e.response?.data?.error || '读取目录失败'
  } finally {
    loading.value = false
  }
}

function firstSelectable(): Node | null {
  for (const doc of nodes.value) {
    if (doc.children.length) return doc.children[0]
    return doc
  }
  return null
}

onMounted(load)
</script>

<style scoped>
.tree-page {
  max-width: 1240px;
}

.tree-head {
  align-items: flex-end;
  margin-bottom: 16px;
}

.tree-search {
  width: 240px;
}

.tree-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 400px;
  gap: 16px;
  align-items: start;
}

.tree-pane {
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface);
  max-height: calc(100vh - 250px);
  overflow-y: auto;
  padding: 8px;
}

.tree-rows {
  display: flex;
  flex-direction: column;
}

.tree-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 6px 10px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  font: inherit;
  text-align: left;
  cursor: pointer;
  min-height: 30px;
}

.tree-row:hover {
  background: var(--bg-subtle);
}

.tree-row.active {
  background: var(--accent-soft);
}

.tree-row.doc {
  margin-top: 4px;
}

.tree-caret {
  flex: 0 0 auto;
  display: grid;
  place-items: center;
  width: 14px;
  height: 14px;
  color: var(--text-muted);
  transition: transform 0.14s ease;
}

.tree-caret.open {
  transform: rotate(90deg);
}

.tree-caret.leaf {
  visibility: hidden;
}

.tree-caret svg {
  width: 12px;
  height: 12px;
}

.tree-icon {
  flex: 0 0 auto;
  width: 14px;
  height: 14px;
  color: var(--text-secondary);
}

.tree-block {
  flex: 0 0 auto;
  width: 14px;
  text-align: center;
  font-size: calc(10px * var(--fs-scale));
  color: var(--text-muted);
}

.tree-label {
  flex: 1;
  min-width: 0;
  font-size: calc(13px * var(--fs-scale));
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.tree-row.doc .tree-label {
  font-weight: 600;
}

.tree-label.major {
  font-weight: 500;
}

.tree-badge {
  flex: 0 0 auto;
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.tree-chunk-no {
  flex: 0 0 auto;
  min-width: 26px;
  font-size: calc(10px * var(--fs-scale));
  font-variant-numeric: tabular-nums;
  color: var(--text-muted);
  text-align: right;
}

.tree-empty {
  padding: 24px;
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-muted);
}

.tree-detail {
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface);
  padding: 18px 20px 20px;
  position: sticky;
  top: 12px;
  max-height: calc(100vh - 250px);
  overflow-y: auto;
}

.td-crumb {
  display: flex;
  gap: 6px;
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.td-sep {
  color: var(--border-strong);
}

.td-title {
  margin: 8px 0 4px;
  font-size: calc(17px * var(--fs-scale));
  font-weight: 600;
  line-height: 1.45;
  color: var(--text);
}

.td-meta {
  margin: 0;
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.td-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 14px;
}

.td-hint {
  width: 100%;
  margin: 0 0 2px;
  font-size: calc(12px * var(--fs-scale));
  line-height: 1.7;
  color: var(--text-muted);
}

.td-skip {
  margin: 0 0 10px;
  padding: 6px 10px;
  border-radius: 6px;
  background: var(--bg-subtle);
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.td-content {
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px solid var(--border);
  font-size: calc(13px * var(--fs-scale));
  line-height: 1.85;
  color: var(--text);
  white-space: pre-wrap;
  word-break: break-word;
}

.tree-detail-empty {
  display: grid;
  place-items: center;
  min-height: 200px;
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-muted);
  text-align: center;
}

@media (max-width: 1000px) {
  .tree-layout {
    grid-template-columns: 1fr;
  }

  .tree-detail {
    position: static;
    max-height: none;
  }
}
</style>
