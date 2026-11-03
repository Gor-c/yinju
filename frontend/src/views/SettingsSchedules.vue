<template>
  <div class="settings-section">
    <header class="page-head">
      <div>
        <div class="eyebrow">Automation</div>
        <h1>定时同步</h1>
        <p class="subtitle">
          按间隔自动拉取语雀 / GitHub 的变更，只处理新增与修改过的文档；远端删除的条目先标记，由你确认后再清理。
        </p>
      </div>
      <div class="head-actions">
        <el-button :loading="loading" @click="load">刷新</el-button>
      </div>
    </header>

    <div v-if="message" class="notice" :class="messageType">{{ message }}</div>

    <section class="panel sc-summary">
      <div class="sc-stat">
        <b>{{ sources.length }}</b>
        <span>导入来源</span>
      </div>
      <div class="sc-stat">
        <b>{{ enabledCount }}</b>
        <span>已开启定时</span>
      </div>
      <div class="sc-stat">
        <b>{{ missingTotal }}</b>
        <span>待清理条目</span>
      </div>
      <div class="sc-stat">
        <b>{{ totalItems }}</b>
        <span>已同步文档</span>
      </div>
    </section>

    <section class="panel sc-panel">
      <div class="sc-panel-head">
        <b>同步任务</b>
        <span class="muted">定时同步依赖已保存的 Token；未保存时只能手动同步</span>
      </div>

      <div v-if="loading && !sources.length" class="muted sc-empty">加载中…</div>

      <div v-else-if="!sources.length" class="sc-empty">
        <p class="muted">还没有导入来源。</p>
        <el-button size="small" @click="$router.push('/upload')">去上传页添加来源</el-button>
      </div>

      <div v-for="src in sources" :key="src.id" class="sc-row">
        <div class="sc-main">
          <div class="sc-title">
            <b>{{ src.name }}</b>
            <el-tag size="small" effect="plain">{{ src.kind === 'yuque' ? '语雀' : 'GitHub' }}</el-tag>
            <el-tag v-if="src.sync_interval_minutes > 0" size="small" type="success" effect="light">定时中</el-tag>
            <el-tag v-if="!src.has_secret" size="small" type="warning" effect="light">未保存 Token</el-tag>
          </div>
          <div class="sc-meta muted">
            {{ unitName(src.unit_id) }} · {{ src.item_count }} 条记录
            <template v-if="src.missing_count"> · <span class="sc-missing">上游已删除 {{ src.missing_count }} 条</span></template>
            <template v-if="src.last_synced_at"> · 上次 {{ formatTime(src.last_synced_at) }}</template>
            <template v-else> · 从未同步</template>
            <template v-if="src.next_sync_at"> · 下次 {{ formatTime(src.next_sync_at) }}</template>
          </div>
          <div v-if="src.last_error" class="sc-error">{{ src.last_error }}</div>
        </div>

        <div class="sc-actions">
          <el-select
            :model-value="src.sync_interval_minutes"
            size="small"
            class="sc-interval"
            :loading="scheduling === src.id"
            @change="(v: number) => changeSchedule(src, Number(v))"
          >
            <el-option v-for="opt in intervalOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
          </el-select>
          <el-button size="small" :loading="syncing === src.id" @click="syncNow(src)">立即同步</el-button>
          <el-button
            v-if="src.missing_count"
            size="small"
            type="warning"
            plain
            :loading="cleaning === src.id"
            @click="cleanup(src)"
          >
            清理 {{ src.missing_count }} 条
          </el-button>
          <el-button size="small" link type="danger" @click="remove(src)">删除</el-button>
        </div>
      </div>
    </section>

    <p class="sc-hint muted">
      调度器每分钟检查一次，执行时把下次时间按间隔顺延；单个来源失败只记录错误，不会影响其它来源。
      语雀来源需要勾选「保存 Token」才能定时同步。
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import axios from 'axios'
import { ElMessage, ElMessageBox } from 'element-plus'

type SourceItem = {
  id: number
  unit_id: string
  kind: 'yuque' | 'github'
  name: string
  has_secret: boolean
  item_count: number
  missing_count: number
  sync_interval_minutes: number
  next_sync_at?: string | null
  last_synced_at?: string | null
  last_error?: string
}

const loading = ref(false)
const message = ref('')
const messageType = ref('')
const sources = ref<SourceItem[]>([])
const units = ref<{ unit_id: string; name: string }[]>([])
const syncing = ref(0)
const cleaning = ref(0)
const scheduling = ref(0)

const intervalOptions = [
  { value: 0, label: '不定时' },
  { value: 30, label: '每 30 分钟' },
  { value: 60, label: '每小时' },
  { value: 180, label: '每 3 小时' },
  { value: 360, label: '每 6 小时' },
  { value: 720, label: '每 12 小时' },
  { value: 1440, label: '每天' },
]

const enabledCount = computed(() => sources.value.filter((s) => s.sync_interval_minutes > 0).length)
const missingTotal = computed(() => sources.value.reduce((sum, s) => sum + (s.missing_count || 0), 0))
const totalItems = computed(() => sources.value.reduce((sum, s) => sum + (s.item_count || 0), 0))

function unitName(id: string) {
  return units.value.find((u) => u.unit_id === id)?.name || id
}

function formatTime(value?: string | null) {
  if (!value) return '—'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleString('zh-CN', { hour12: false })
}

function setMessage(text: string, kind: '' | 'error' | 'success' = '') {
  message.value = text
  messageType.value = kind
}

async function load() {
  loading.value = true
  try {
    const [list, unitRes] = await Promise.all([
      axios.get('/api/v1/import/sources'),
      axios.get('/api/v1/units'),
    ])
    sources.value = list.data?.items || []
    units.value = unitRes.data || []
  } catch (e: any) {
    setMessage(e.response?.data?.error || '读取导入来源失败', 'error')
  } finally {
    loading.value = false
  }
}

async function changeSchedule(src: SourceItem, minutes: number) {
  scheduling.value = src.id
  try {
    const { data } = await axios.put(`/api/v1/import/sources/${src.id}/schedule`, { interval_minutes: minutes })
    Object.assign(src, data.source || {})
    ElMessage.success(minutes > 0 ? '已开启定时同步' : '已关闭定时同步')
    await load()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '设置失败')
    await load()
  } finally {
    scheduling.value = 0
  }
}

async function syncNow(src: SourceItem) {
  syncing.value = src.id
  try {
    const { data } = await axios.post(
      `/api/v1/import/sources/${src.id}/sync`,
      { token: '' },
      { timeout: 180000 },
    )
    setMessage(
      `「${src.name}」已入队 ${data.queued} 篇，未变化 ${data.unchanged} 篇，新增待清理 ${data.missing || 0} 条`,
      'success',
    )
    await load()
  } catch (e: any) {
    setMessage(e.response?.data?.error || '同步失败', 'error')
  } finally {
    syncing.value = 0
  }
}

async function cleanup(src: SourceItem) {
  try {
    await ElMessageBox.confirm(
      `将删除「${src.name}」中 ${src.missing_count} 条远端已删除的文档，并从同步记录中移除。此操作不可撤销。`,
      '清理已删除条目',
      { type: 'warning', confirmButtonText: '清理', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  cleaning.value = src.id
  try {
    const { data } = await axios.post(`/api/v1/import/sources/${src.id}/cleanup`, {}, { timeout: 120000 })
    setMessage(`已清理 ${data.removed} 条${data.failed?.length ? `，失败 ${data.failed.length} 条` : ''}`, 'success')
    await load()
  } catch (e: any) {
    setMessage(e.response?.data?.error || '清理失败', 'error')
  } finally {
    cleaning.value = 0
  }
}

async function remove(src: SourceItem) {
  try {
    await ElMessageBox.confirm(
      `删除来源「${src.name}」？已入库的文档不会被删除，只是不再同步。`,
      '删除来源',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  try {
    await axios.delete(`/api/v1/import/sources/${src.id}`)
    ElMessage.success('已删除')
    await load()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '删除失败')
  }
}

let timer: number | undefined
onMounted(async () => {
  await load()
  // 调度器每分钟跑一次，页面同频刷新即可看到下次执行与错误。
  timer = window.setInterval(() => {
    void load()
  }, 60000)
})
onUnmounted(() => {
  if (timer) window.clearInterval(timer)
})
</script>

<style scoped>
.sc-summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 14px;
  padding: 16px 20px;
  margin-bottom: 14px;
}
.sc-stat {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.sc-stat b {
  font-size: calc(20px * var(--fs-scale));
  font-weight: 550;
}
.sc-stat span {
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-muted);
}
.sc-panel {
  padding: 18px 20px;
  margin-bottom: 12px;
}
.sc-panel-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin-bottom: 14px;
}
.sc-panel-head b {
  font-size: calc(14px * var(--fs-scale));
  font-weight: 500;
}
.sc-panel-head .muted {
  font-size: calc(12px * var(--fs-scale));
}
.sc-row {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 12px 0;
  border-bottom: 1px solid var(--border);
}
.sc-row:last-child {
  border-bottom: none;
}
.sc-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.sc-title {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.sc-title b {
  font-weight: 550;
  font-size: calc(13.5px * var(--fs-scale));
}
.sc-meta {
  font-size: calc(12px * var(--fs-scale));
}
.sc-missing {
  color: var(--warning, #b26a00);
}
.sc-error {
  font-size: calc(12px * var(--fs-scale));
  color: var(--danger, #c0392b);
  word-break: break-word;
}
.sc-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  justify-content: flex-end;
}
.sc-interval {
  width: 130px;
}
.sc-empty {
  padding: 18px 0;
  display: flex;
  align-items: center;
  gap: 12px;
}
.sc-hint {
  font-size: calc(12px * var(--fs-scale));
  line-height: 1.7;
}
@media (max-width: 860px) {
  .sc-row {
    flex-direction: column;
    align-items: stretch;
  }
  .sc-actions {
    justify-content: flex-start;
  }
}
</style>
