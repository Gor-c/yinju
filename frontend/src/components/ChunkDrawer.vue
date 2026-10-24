<script setup lang="ts">
import { ref, watch } from 'vue'
import axios from 'axios'

// 片段抽屉：按 (知识库, 文件名) 读取已入库的 chunk，并可直接定位到某一块。
// 对话里的引用跳转与文档库的「索引片段」共用这一套取数与展示。
const props = defineProps<{
  modelValue: boolean
  unitId?: string
  filename?: string
  docId?: string
  docType?: string
  // 需要高亮的片段序号；会先跳到它所在的那一页。
  focusIndex?: number
}>()

const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()

interface ChunkItem {
  index: number
  content: string
  expanded?: boolean
}

const pageSize = 10
const loading = ref(false)
const error = ref('')
const items = ref<ChunkItem[]>([])
const offset = ref(0)
const total = ref(0)
const hasMore = ref(false)

function initialOffset() {
  const focus = props.focusIndex ?? 0
  if (focus <= 0) return 0
  return Math.floor(focus / pageSize) * pageSize
}

async function load(start: number) {
  if (!props.unitId || !props.filename) return
  loading.value = true
  error.value = ''
  try {
    const base = `/api/v1/units/${encodeURIComponent(props.unitId)}/documents/${encodeURIComponent(props.filename)}/chunks`
    const { data } = await axios.get(base, { params: { offset: start, limit: pageSize } })
    items.value = (data?.chunks || []).map((c: ChunkItem) => ({
      ...c,
      expanded: c.index === props.focusIndex ? true : c.expanded,
    }))
    offset.value = data?.offset ?? start
    hasMore.value = !!data?.has_more
    if (data?.chunk_count) total.value = data.chunk_count
  } catch (e: any) {
    error.value = e.response?.data?.error || '读取索引片段失败'
    items.value = []
  } finally {
    loading.value = false
  }
}

watch(
  () => props.modelValue,
  (open) => {
    if (!open) return
    items.value = []
    total.value = 0
    load(initialOffset())
  },
)

// 同一抽屉内切换引用目标时重新加载。
watch(
  () => props.focusIndex,
  () => {
    if (props.modelValue) load(initialOffset())
  },
)
</script>

<template>
  <el-drawer
    :model-value="modelValue"
    :title="`索引片段 · ${filename || ''}`"
    size="560px"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="chunk-meta">
      <div><span>文档 ID</span><b>{{ docId || '—' }}</b></div>
      <div><span>类型</span><b>{{ docType || '未分类' }}</b></div>
      <div><span>文本块</span><b>{{ total || '—' }}</b></div>
      <div v-if="focusIndex !== undefined && focusIndex >= 0">
        <span>当前引用</span><b>第 {{ focusIndex }} 块</b>
      </div>
    </div>

    <div v-if="error" class="notice error">{{ error }}</div>
    <div v-loading="loading" class="chunk-list">
      <div
        v-for="c in items"
        :key="c.index"
        class="chunk-card"
        :class="{ focused: c.index === focusIndex }"
      >
        <div class="chunk-head">
          <b>文本块 #{{ c.index }}</b>
          <span v-if="c.index === focusIndex" class="chunk-badge">引用位置</span>
        </div>
        <p
          class="chunk-text"
          :class="{ collapsed: !c.expanded && c.content.length > 360, expanded: c.expanded }"
        >
          {{ c.content }}
        </p>
        <button
          v-if="c.content.length > 360"
          class="chunk-toggle"
          type="button"
          @click="c.expanded = !c.expanded"
        >
          {{ c.expanded ? '收起原文' : '展开全文' }}
        </button>
      </div>
      <div v-if="!loading && !items.length" class="muted" style="padding: 24px 0">
        暂无索引片段（可能是文档刚被清空或向量尚未写入）。
      </div>
    </div>

    <div v-if="items.length" class="chunk-pager">
      <el-button size="small" :disabled="offset === 0 || loading" @click="load(Math.max(0, offset - pageSize))">
        上一页
      </el-button>
      <span class="muted">{{ offset + 1 }} – {{ offset + items.length }} / {{ total }}</span>
      <el-button size="small" :disabled="!hasMore || loading" @click="load(offset + pageSize)">
        下一页
      </el-button>
    </div>
  </el-drawer>
</template>

<style scoped>
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
  padding: 14px 12px;
  border-bottom: 1px solid var(--border);
  border-radius: 8px;
}
.chunk-card:last-child {
  border-bottom: none;
}
.chunk-card.focused {
  background: var(--accent-soft);
  border-bottom-color: transparent;
}
.chunk-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  font-size: calc(12.5px * var(--fs-scale));
}
.chunk-badge {
  padding: 1px 7px;
  border-radius: 999px;
  background: var(--accent);
  color: #fff;
  font-size: calc(10.5px * var(--fs-scale));
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
@media (max-width: 720px) {
  .chunk-meta {
    grid-template-columns: 1fr;
  }
}
</style>
