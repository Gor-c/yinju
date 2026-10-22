<template>
  <div class="page unit-list-page">
    <header class="page-head unit-list-head">
      <div>
        <div class="eyebrow">Knowledge Bases</div>
        <h1>知识库</h1>
        <p class="subtitle">{{ loading ? '正在加载…' : summary }}</p>
      </div>
      <div class="head-actions">
        <el-button :loading="loading" @click="load">刷新</el-button>
        <el-button @click="$router.push('/upload')">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 16V5m0 0L8 9m4-4 4 4M5 15v3a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-3" /></svg>
          上传文档
        </el-button>
        <el-button type="primary" @click="openCreate">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M12 5v14M5 12h14" /></svg>
          新建知识库
        </el-button>
      </div>
    </header>

    <div v-if="error" class="notice error">{{ error }}</div>

    <section class="unit-overview">
      <div class="unit-overview-copy">
        <span>KNOWLEDGE AT A GLANCE</span>
        <h2>知识资产概览</h2>
        <p>{{ summary }}</p>
        <div class="unit-overview-status">
          <span><i class="ready"></i>{{ readyCount }} 个已有内容</span>
          <span><i :class="{ warn: emptyCount > 0 }"></i>{{ emptyCount }} 个待补充文档</span>
        </div>
      </div>
      <div class="unit-overview-metrics">
        <div>
          <span class="overview-metric-icon docs">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M7 3h7l4 4v14H7zM14 3v5h5" /></svg>
          </span>
          <div><b>{{ totalDocs }}</b><span>文档</span></div>
        </div>
        <div>
          <span class="overview-metric-icon chunks">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"><path d="M5 6h14M5 12h14M5 18h9" /></svg>
          </span>
          <div><b>{{ totalChunks }}</b><span>文本块</span></div>
        </div>
        <div>
          <span class="overview-metric-icon agents">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3 4 7v5c0 4.4 3.4 7.5 8 9 4.6-1.5 8-4.6 8-9V7l-8-4Z" /><path d="m9 12 2 2 4-4" /></svg>
          </span>
          <div><b>{{ configuredCount }}</b><span>自定义 Agent</span></div>
        </div>
      </div>
    </section>

    <div class="panel unit-list-panel">
      <div class="unit-list-toolbar">
        <div class="unit-filter-controls">
          <el-input v-model="keyword" class="search-input unit-search" clearable placeholder="搜索名称、分类、行业或标签">
            <template #prefix>
              <svg class="unit-search-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="11" cy="11" r="7" /><path d="m16.5 16.5 4 4" /></svg>
            </template>
          </el-input>
          <el-select v-model="typeFilter" clearable placeholder="全部分类" class="unit-type-filter">
            <el-option v-for="type in typeOptions" :key="type" :label="type" :value="type" />
          </el-select>
          <el-radio-group v-model="statusFilter" size="small" class="unit-status-filter">
            <el-radio-button value="all">全部</el-radio-button>
            <el-radio-button value="ready">已有内容</el-radio-button>
            <el-radio-button value="empty">待补充</el-radio-button>
          </el-radio-group>
        </div>
        <span class="unit-result-count">{{ keyword || typeFilter || statusFilter !== 'all' ? `${filtered.length} 个匹配结果` : `共 ${units.length} 个知识库` }}</span>
      </div>
      <div class="unit-card-grid" v-loading="loading">
        <article v-for="unit in filtered" :key="unit.unit_id" class="unit-card" :class="{ empty: !unit.document_count }">
          <router-link :to="`/units/${unit.unit_id}`" class="unit-card-main">
            <div class="unit-card-heading">
              <div class="unit-card-identity">
                <span class="unit-card-avatar">{{ unitInitial(unit.name) }}</span>
                <div class="unit-card-title-wrap">
                  <span class="unit-card-category">{{ unit.unit_type || '未分类' }}<template v-if="unit.industry"> · {{ unit.industry }}</template></span>
                  <h2 class="unit-card-title">{{ unit.name }}</h2>
                  <span class="unit-card-id">{{ unit.unit_id }}</span>
                </div>
              </div>
              <span class="unit-card-arrow" aria-hidden="true">↗</span>
            </div>

            <div class="unit-health-row">
              <span class="unit-health" :class="{ empty: !unit.document_count }"><i></i>{{ unit.document_count ? '已就绪' : '待补充文档' }}</span>
              <span>最近活动 {{ relativeTime(unit.last_indexed_at) }}</span>
            </div>

            <div class="unit-card-metrics">
              <div><b>{{ unit.document_count ?? 0 }}</b><span>文档</span></div>
              <div><b>{{ unit.chunk_count ?? 0 }}</b><span>文本块</span></div>
              <div><b>{{ unit.doc_types?.length || 0 }}</b><span>文档类型</span></div>
            </div>

            <div v-if="unit.doc_types?.length" class="unit-card-types">
              <el-tag v-for="t in unit.doc_types.slice(0, 4)" :key="t" size="small" effect="plain" type="info">{{ t }}</el-tag>
              <span v-if="unit.doc_types.length > 4" class="muted">+{{ unit.doc_types.length - 4 }}</span>
            </div>

            <div class="unit-card-config">
              <div class="unit-card-agent">
                <span class="unit-card-label">默认 Agent</span>
                <b>{{ expertName(unit.expert_id) }}</b>
              </div>
              <div class="unit-card-counts">
                <span>{{ unit.skill_ids?.length || 0 }} 项默认技能</span>
                <span>{{ unit.tool_names?.length ? `${unit.tool_names.length} 项指定工具` : '工具自动匹配' }}</span>
              </div>
            </div>
          </router-link>

          <div class="unit-card-bottom">
            <div class="unit-card-tags">
              <el-tag v-for="tag in (unit.tags || []).slice(0, 3)" :key="tag" size="small" effect="plain">{{ tag }}</el-tag>
              <span v-if="!unit.tags?.length" class="muted">暂无标签</span>
              <span v-else-if="unit.tags.length > 3" class="muted">+{{ unit.tags.length - 3 }}</span>
            </div>
            <div class="unit-card-actions">
              <button type="button" class="unit-quick-action primary" @click="chatWithUnit(unit)">去对话</button>
              <button type="button" class="unit-quick-action" @click="uploadToUnit(unit)">{{ unit.document_count ? '上传' : '添加文档' }}</button>
              <button type="button" class="unit-quick-action" @click="openEdit(unit)">编辑</button>
            </div>
          </div>
        </article>

        <div v-if="!loading && !filtered.length" class="unit-empty muted">
          <b>{{ keyword || typeFilter || statusFilter !== 'all' ? '没有匹配的知识库' : '还没有知识库' }}</b>
          <span>{{ keyword || typeFilter || statusFilter !== 'all' ? '调整搜索或筛选条件后再试。' : '新建知识库，或先上传文档。' }}</span>
          <el-button v-if="keyword || typeFilter || statusFilter !== 'all'" @click="clearFilters">清空筛选</el-button>
          <el-button v-else type="primary" @click="openCreate">新建知识库</el-button>
        </div>
      </div>
    </div>

    <el-dialog
      v-model="dialog"
      :title="editing ? '编辑知识库' : '新建知识库'"
      width="min(620px, 94vw)"
      top="5vh"
      class="unit-edit-dialog"
    >
      <p class="unit-dialog-intro muted">维护知识库基本信息，并配置它在 Agent 对话中的默认行为。</p>
      <el-form label-width="92px" class="unit-edit-form" @submit.prevent>
        <el-form-item label="知识库名称" required>
          <el-input v-model="form.name" maxlength="100" show-word-limit placeholder="例如：滨江改造工程" />
        </el-form-item>
        <el-form-item label="项目分类">
          <el-select v-model="form.unit_type" filterable allow-create clearable default-first-option placeholder="选择或输入分类" style="width: 100%">
            <el-option v-for="c in categories" :key="c" :label="c" :value="c" />
          </el-select>
        </el-form-item>
        <el-form-item label="所属行业">
          <el-select v-model="form.industry" filterable allow-create clearable default-first-option placeholder="选择或输入行业" style="width: 100%">
            <el-option v-for="i in industries" :key="i" :label="i" :value="i" />
          </el-select>
        </el-form-item>

        <el-divider content-position="left">对话默认配置</el-divider>
        <el-form-item label="Agent">
          <el-select v-model="form.expert_id" clearable placeholder="使用全局默认" style="width: 100%">
            <el-option v-for="e in experts" :key="e.id" :label="e.name" :value="e.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="技能">
          <el-select v-model="form.skill_ids" multiple collapse-tags clearable placeholder="选择默认技能" style="width: 100%">
            <el-option v-for="s in skills" :key="s.id" :label="s.name" :value="s.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="工具">
          <el-select v-model="form.tool_names" multiple collapse-tags clearable placeholder="留空则按 Agent 与技能自动推导" style="width: 100%">
            <el-option v-for="t in agentTools" :key="t.name" :label="t.label" :value="t.name" />
          </el-select>
        </el-form-item>
        <el-form-item label="标签">
          <el-select
            v-model="form.tags"
            multiple
            filterable
            allow-create
            default-first-option
            clearable
            placeholder="输入后回车添加标签"
            style="width: 100%"
          >
            <el-option v-for="t in tagOptions" :key="t" :label="t" :value="t" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <div class="unit-dialog-footer">
          <span class="muted">这些设置可随时在知识库详情页修改</span>
          <div>
            <el-button @click="dialog = false">取消</el-button>
            <el-button type="primary" :loading="saving" @click="save">保存</el-button>
          </div>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import axios from 'axios'
import { ElMessage } from 'element-plus'
import { useRouter } from 'vue-router'

interface Unit {
  unit_id: string
  name: string
  unit_type: string
  industry: string
  document_count?: number
  chunk_count?: number
  last_indexed_at?: string | null
  doc_types?: string[]
  expert_id?: string
  skill_ids?: string[]
  tool_names?: string[]
  tags?: string[]
}

interface AgentOption {
  id: string
  name: string
}

const agentTools = [
  { name: 'get_unit_profile', label: '知识库信息' },
  { name: 'query_hazards', label: '隐患查询' },
  { name: 'audit_history', label: '审核历史' },
  { name: 'vector_search', label: '知识库检索' },
]

const units = ref<Unit[]>([])
const router = useRouter()

function relativeTime(value?: string | null) {
  if (!value) return '未索引'
  const diff = Date.now() - new Date(value).getTime()
  if (Number.isNaN(diff)) return '—'
  if (diff < 60_000) return '刚刚'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`
  if (diff < 30 * 86_400_000) return `${Math.floor(diff / 86_400_000)} 天前`
  return new Date(value).toLocaleDateString()
}
const experts = ref<AgentOption[]>([])
const skills = ref<AgentOption[]>([])
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const keyword = ref('')
const typeFilter = ref('')
const statusFilter = ref<'all' | 'ready' | 'empty'>('all')
const categories = ref<string[]>([])
const industries = ref<string[]>([])

const dialog = ref(false)
const editing = ref<Unit | null>(null)
const form = ref({
  name: '',
  unit_type: '',
  industry: '',
  expert_id: '',
  skill_ids: [] as string[],
  tool_names: [] as string[],
  tags: [] as string[],
})

const tagOptions = computed(() => [...new Set(units.value.flatMap((u) => u.tags || []))])
const typeOptions = computed(() => [...new Set(units.value.map((u) => u.unit_type).filter(Boolean))].sort())
const totalDocs = computed(() => units.value.reduce((sum, u) => sum + (u.document_count || 0), 0))
const totalChunks = computed(() => units.value.reduce((sum, u) => sum + (u.chunk_count || 0), 0))
const readyCount = computed(() => units.value.filter((u) => (u.document_count || 0) > 0).length)
const emptyCount = computed(() => units.value.length - readyCount.value)
const configuredCount = computed(() => units.value.filter((u) => !!u.expert_id).length)

const filtered = computed(() => {
  const key = keyword.value.trim().toLowerCase()
  return units.value.filter((u) => {
    const matchesKey = !key ||
      u.name.toLowerCase().includes(key) ||
      u.unit_id.toLowerCase().includes(key) ||
      (u.unit_type || '').toLowerCase().includes(key) ||
      (u.industry || '').toLowerCase().includes(key) ||
      (u.tags || []).some((tag) => tag.toLowerCase().includes(key))
    const matchesType = !typeFilter.value || u.unit_type === typeFilter.value
    const matchesStatus =
      statusFilter.value === 'all' ||
      (statusFilter.value === 'ready' && (u.document_count || 0) > 0) ||
      (statusFilter.value === 'empty' && (u.document_count || 0) === 0)
    return matchesKey && matchesType && matchesStatus
  })
})

const summary = computed(() => {
  const docTotal = units.value.reduce((sum, u) => sum + (u.document_count || 0), 0)
  const chunkTotal = units.value.reduce((sum, u) => sum + (u.chunk_count || 0), 0)
  const scope = keyword.value ? `匹配 ${filtered.value.length} 个` : `共 ${units.value.length} 个`
  return `${scope}知识库 · ${docTotal} 份文档 · ${chunkTotal} 个文本块`
})

function expertName(id?: string) {
  if (!id) return '默认'
  return experts.value.find((e) => e.id === id)?.name || id
}

function unitInitial(name: string) {
  return Array.from(name.trim())[0] || '知'
}

function clearFilters() {
  keyword.value = ''
  typeFilter.value = ''
  statusFilter.value = 'all'
}

function chatWithUnit(unit: Unit) {
  router.push({ path: '/agent', query: { unit: unit.unit_id } })
}

function uploadToUnit(unit: Unit) {
  router.push({ path: '/upload', query: { unit: unit.unit_id } })
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    units.value = (await axios.get('/api/v1/units')).data
  } catch (e: any) {
    units.value = []
    error.value = e.response?.status === 503
      ? '数据库未配置，无法读取项目列表。'
      : e.response?.data?.error || '读取项目列表失败，请确认服务可用。'
  } finally {
    loading.value = false
  }
}

async function loadMeta() {
  try {
    const [meta, cfg] = await Promise.all([
      axios.get('/api/v1/units/meta'),
      axios.get('/api/v1/agent/config'),
    ])
    categories.value = meta.data.categories || []
    industries.value = meta.data.industries || []
    experts.value = cfg.data.experts || []
    skills.value = cfg.data.skills || []
  } catch {
    // 建议项加载失败不影响项目管理。
  }
}

function openCreate() {
  editing.value = null
  form.value = { name: '', unit_type: '', industry: '', expert_id: '', skill_ids: [], tool_names: [], tags: [] }
  dialog.value = true
}

function openEdit(row: Unit) {
  editing.value = row
  form.value = {
    name: row.name,
    unit_type: row.unit_type || '',
    industry: row.industry || '',
    expert_id: row.expert_id || '',
    skill_ids: [...(row.skill_ids || [])],
    tool_names: [...(row.tool_names || [])],
    tags: [...(row.tags || [])],
  }
  dialog.value = true
}

async function save() {
  const name = form.value.name.trim()
  if (!name) {
    ElMessage.warning('请填写项目名称')
    return
  }
  saving.value = true
  try {
    const payload = {
      name,
      unit_type: form.value.unit_type || '',
      industry: form.value.industry || '',
      expert_id: form.value.expert_id || '',
      skill_ids: form.value.skill_ids,
      tool_names: form.value.tool_names,
      tags: form.value.tags,
    }
    if (editing.value) {
      await axios.patch(`/api/v1/units/${editing.value.unit_id}`, payload)
      ElMessage.success('项目已更新')
    } else {
      await axios.post('/api/v1/units', payload)
      ElMessage.success('项目已创建')
    }
    dialog.value = false
    await Promise.all([load(), loadMeta()])
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '保存失败，请稍后重试')
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  load()
  loadMeta()
})
</script>

<style scoped>
.unit-list-page {
  max-width: 1220px;
}

.unit-list-head {
  align-items: flex-end;
}

.unit-list-head .head-actions :deep(.el-button svg) {
  width: 14px;
  height: 14px;
  margin-right: 4px;
}

.unit-overview {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 24px;
  position: relative;
  overflow: hidden;
  margin-bottom: 16px;
  padding: 22px 24px;
  border: 1px solid color-mix(in srgb, var(--accent) 16%, var(--border));
  border-radius: 18px;
  background:
    radial-gradient(circle at 88% 20%, color-mix(in srgb, var(--accent) 12%, transparent) 0, transparent 35%),
    linear-gradient(135deg, var(--surface), color-mix(in srgb, var(--accent) 4%, var(--bg-subtle)));
}

.unit-overview-copy {
  max-width: 540px;
}

.unit-overview-copy > span {
  color: var(--accent);
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.12em;
}

.unit-overview-copy h2 {
  margin: 7px 0 5px;
  color: var(--text);
  font-size: 20px;
  font-weight: 650;
  letter-spacing: -0.02em;
}

.unit-overview-copy p {
  margin: 0;
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.65;
}

.unit-overview-status {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
  margin-top: 13px;
  color: var(--text-muted);
  font-size: 11px;
}

.unit-overview-status span {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.unit-overview-status i {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--accent);
}

.unit-overview-status i.warn {
  background: #d97706;
}

.unit-overview-metrics {
  display: grid;
  grid-template-columns: repeat(3, minmax(112px, 1fr));
  gap: 8px;
}

.unit-overview-metrics > div {
  display: flex;
  min-width: 112px;
  align-items: center;
  gap: 10px;
  padding: 12px 13px;
  border: 1px solid color-mix(in srgb, var(--border) 70%, transparent);
  border-radius: 12px;
  background: color-mix(in srgb, var(--surface) 90%, transparent);
  backdrop-filter: blur(4px);
}

.overview-metric-icon {
  display: grid;
  width: 31px;
  height: 31px;
  flex: none;
  place-items: center;
  border-radius: 9px;
  background: var(--bg-subtle);
  color: var(--text-secondary);
}

.overview-metric-icon svg {
  width: 16px;
  height: 16px;
}

.overview-metric-icon.chunks {
  background: color-mix(in srgb, var(--accent) 8%, var(--surface));
  color: var(--accent);
}

.overview-metric-icon.agents {
  background: color-mix(in srgb, #6366f1 8%, var(--surface));
  color: #6366f1;
}

.unit-overview-metrics > div > div {
  display: flex;
  min-width: 0;
  flex-direction: column;
}

.unit-overview-metrics b {
  color: var(--text);
  font-size: 18px;
  font-weight: 680;
  line-height: 1.2;
}

.unit-overview-metrics span:not(.overview-metric-icon) {
  color: var(--text-muted);
  font-size: 10px;
  white-space: nowrap;
}

.unit-list-panel {
  overflow: hidden;
}

.unit-list-toolbar {
  align-items: center;
  gap: 14px;
  padding: 14px 18px;
}

.unit-filter-controls {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.unit-search {
  width: 280px;
}

.unit-search-icon {
  width: 14px;
  height: 14px;
  color: var(--text-muted);
}

.unit-type-filter {
  width: 150px;
}

.unit-result-count {
  flex: none;
  margin-left: auto;
  color: var(--text-muted);
  font-size: 11px;
}

.unit-card-grid {
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 280px), 1fr));
  gap: 14px;
  padding: 14px;
}

.unit-card {
  gap: 0;
  overflow: hidden;
  padding: 0;
  border-radius: 15px;
}

.unit-card.empty {
  border-color: color-mix(in srgb, #d97706 16%, var(--border));
  background: linear-gradient(180deg, color-mix(in srgb, #d97706 2.5%, var(--surface)), var(--surface));
}

.unit-card-main {
  gap: 13px;
  padding: 17px 17px 14px;
}

.unit-card-heading {
  gap: 10px;
}

.unit-card-identity {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  gap: 11px;
}

.unit-card-avatar {
  display: grid;
  width: 38px;
  height: 38px;
  flex: none;
  place-items: center;
  border: 1px solid color-mix(in srgb, var(--accent) 15%, var(--border));
  border-radius: 11px;
  background: linear-gradient(145deg, var(--accent-soft), var(--surface));
  color: var(--accent);
  font-size: 15px;
  font-weight: 700;
}

.unit-card-title-wrap {
  display: flex;
  min-width: 0;
  flex-direction: column;
}

.unit-card-category {
  display: block;
  overflow: hidden;
  margin-bottom: 3px;
  color: var(--text-muted);
  font-size: 9px;
  font-weight: 600;
  letter-spacing: 0.05em;
  text-overflow: ellipsis;
  text-transform: uppercase;
  white-space: nowrap;
}

.unit-card-title {
  -webkit-line-clamp: 1;
  font-size: 14px;
}

.unit-card-id {
  margin-top: 3px;
}

.unit-card-arrow {
  width: 26px;
  height: 26px;
}

.unit-health-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  color: var(--text-muted);
  font-size: 10px;
}

.unit-health {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--text-secondary);
  font-size: 10px;
  font-weight: 600;
}

.unit-health i {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-soft);
}

.unit-health.empty {
  color: #b45309;
}

.unit-health.empty i {
  background: #d97706;
  box-shadow: 0 0 0 3px color-mix(in srgb, #d97706 10%, var(--surface));
}

.unit-card-metrics {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  padding: 10px 0;
  border-top: 1px solid var(--border);
  border-bottom: 1px solid var(--border);
}

.unit-card-metrics div {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 2px;
  padding: 0 10px;
  border-left: 1px solid var(--border);
}

.unit-card-metrics div:first-child {
  padding-left: 0;
  border-left: none;
}

.unit-card-metrics b {
  color: var(--text);
  font-size: 17px;
  font-weight: 650;
  line-height: 1.2;
}

.unit-card-metrics span {
  color: var(--text-muted);
  font-size: 9px;
}

.unit-card-types {
  min-height: 22px;
  gap: 5px;
}

.unit-card-types :deep(.el-tag) {
  height: 21px;
  font-size: 10px;
}

.unit-card-config {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: end;
  padding: 10px 11px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--bg-subtle);
}

.unit-card-agent b {
  font-size: 11px;
}

.unit-card-counts {
  gap: 2px;
  font-size: 9px;
}

.unit-card-bottom {
  gap: 10px;
  padding: 11px 17px;
  border-top: 1px solid var(--border);
  background: color-mix(in srgb, var(--bg-subtle) 70%, var(--surface));
}

.unit-card-tags {
  flex: 1;
}

.unit-card-tags :deep(.el-tag) {
  height: 21px;
  font-size: 9px;
}

.unit-card-actions {
  display: flex;
  flex: none;
  align-items: center;
  gap: 4px;
}

.unit-quick-action {
  padding: 4px 7px;
  border: 1px solid transparent;
  border-radius: 7px;
  background: transparent;
  color: var(--text-muted);
  font-size: 10px;
  cursor: pointer;
  transition: border-color 0.15s ease, background 0.15s ease, color 0.15s ease;
}

.unit-quick-action:hover {
  border-color: var(--border);
  background: var(--surface);
  color: var(--text);
}

.unit-quick-action.primary {
  border-color: color-mix(in srgb, var(--accent) 18%, var(--border));
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 600;
}

.unit-quick-action.primary:hover {
  border-color: color-mix(in srgb, var(--accent) 30%, var(--border));
  background: color-mix(in srgb, var(--accent) 14%, var(--surface));
  color: var(--accent);
}

@media (max-width: 1080px) {
  .unit-list-head {
    align-items: flex-start;
  }

  .unit-overview {
    grid-template-columns: 1fr;
  }

  .unit-overview-metrics {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  .unit-overview-metrics > div {
    min-width: 0;
  }
}

@media (max-width: 720px) {
  .unit-list-head {
    align-items: flex-start;
  }

  .unit-list-head .head-actions {
    width: 100%;
  }

  .unit-list-head .head-actions :deep(.el-button) {
    flex: 1;
  }

  .unit-overview {
    padding: 18px;
  }

  .unit-overview-metrics {
    grid-template-columns: 1fr;
  }

  .unit-list-toolbar {
    align-items: stretch;
    flex-direction: column;
  }

  .unit-search,
  .unit-type-filter {
    width: 100%;
  }

  .unit-result-count {
    margin-left: 0;
  }

  .unit-status-filter {
    width: 100%;
  }

  .unit-status-filter :deep(.el-radio-button) {
    flex: 1;
  }

  .unit-status-filter :deep(.el-radio-button__inner) {
    width: 100%;
  }

  .unit-card-bottom {
    align-items: flex-start;
    flex-direction: column;
  }

  .unit-card-actions {
    width: 100%;
    justify-content: flex-end;
  }
}
</style>
