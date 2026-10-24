<template>
  <div class="page kb-page">
    <header class="page-head kb-page-head">
      <div class="kb-head-copy">
        <div class="eyebrow">Knowledge Bases</div>
        <h1>知识库</h1>
      </div>
      <div class="head-actions">
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

    <!-- 主从布局：左边选库，右边立刻看到这个库讲了什么、能问什么。
         原来是等权重卡片墙，四个库长得一样，谁也没有重点；现在任何时候
         只有一个「当前库」，信息也就有了落点。 -->
    <div class="kb-layout">
      <aside class="kb-rail">
        <div class="kb-rail-search">
          <el-input v-model="keyword" size="small" clearable placeholder="搜索名称或内容">
            <template #prefix>
              <svg class="kb-rail-search-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="11" cy="11" r="7" /><path d="m16.5 16.5 4 4" /></svg>
            </template>
          </el-input>
        </div>

        <div class="kb-rail-list" v-loading="loading">
          <button
            v-for="u in filtered"
            :key="u.unit_id"
            type="button"
            class="kb-rail-item"
            :class="{ active: u.unit_id === selectedId }"
            @click="selectedId = u.unit_id"
          >
            <span class="kb-rail-initial">{{ unitInitial(u.name) }}</span>
            <span class="kb-rail-body">
              <span class="kb-rail-name">{{ u.name }}</span>
              <span class="kb-rail-meta">
                <template v-if="u.chapter_count">{{ u.chapter_count }} 章节 · </template>{{ u.document_count || 0 }} 份文档
              </span>
              <!-- 库与库的区别在「覆盖什么」，不在「有几份文件」。
                   清单里先露两个章节名，扫一眼就知道这个库讲哪块业务。 -->
              <span v-if="u.chapters?.length" class="kb-rail-topics">{{ u.chapters.slice(0, 2).join(' · ') }}</span>
            </span>
          </button>

          <p v-if="!loading && !filtered.length" class="kb-rail-empty">
            {{ keyword ? '没有匹配的知识库' : '还没有知识库' }}
          </p>
        </div>
      </aside>

      <section v-if="current" class="kb-stage">
        <header class="kb-stage-head">
          <div class="kb-stage-title">
            <h2>{{ current.name }}</h2>
            <div class="kb-stage-meta">
              <span v-if="current.unit_type" class="kb-meta-strong">{{ current.unit_type }}</span>
              <span v-if="current.industry">{{ current.industry }}</span>
              <span>{{ current.document_count || 0 }} 份文档</span>
              <span>{{ current.chapter_count || 0 }} 个章节</span>
              <span>{{ current.chunk_count || 0 }} 个片段</span>
            </div>
          </div>
          <div class="kb-stage-actions">
            <el-button size="small" text @click="openEdit(current)">设置</el-button>
            <el-button size="small" @click="uploadToUnit(current)">上传</el-button>
            <el-button size="small" type="primary" @click="chatWithUnit(current)">开始对话</el-button>
          </div>
        </header>

        <!-- 能问什么：这是整个页面的重点。问题由章节生成、并且已经在
             当前参数下真实检索验证过，点了答得上来才不会伤信任。 -->
        <section class="kb-block kb-block-questions">
          <div class="kb-block-head">
            <h3>能问什么</h3>
            <span class="kb-block-hint">已在本库检索验证</span>
          </div>

          <div v-if="loadingDetail" class="kb-skeleton">
            <span v-for="i in 3" :key="i" class="kb-skeleton-row"></span>
          </div>
          <ol v-else-if="examples.length" class="kb-questions">
            <li v-for="(q, i) in examples" :key="q.question">
              <button type="button" class="kb-question" @click="askQuestion(q.question)">
                <span class="q-index">{{ String(i + 1).padStart(2, '0') }}</span>
                <span class="q-text">{{ q.question }}</span>
                <span class="q-source">{{ i === 0 || q.doc_name !== examples[i - 1].doc_name ? q.doc_name : '' }}</span>
                <span class="q-go" aria-hidden="true">→</span>
              </button>
            </li>
          </ol>
          <div v-else class="kb-block-empty">
            <template v-if="current.document_count">
              章节结构不明显，这个问题暂时生成不出来；内容仍可直接提问。
            </template>
            <template v-else>
              这个库还是空的。先上传文档，再回来看能问什么。
            </template>
          </div>
        </section>

        <section v-if="chapters.length" class="kb-block">
          <div class="kb-block-head">
            <h3>内容框架</h3>
            <router-link class="kb-block-link" :to="`/units/${current.unit_id}/outline`">
              打开完整目录 →
            </router-link>
          </div>
          <div class="kb-chapters">
            <button
              v-for="c in chapters"
              :key="c.doc_id + c.title"
              type="button"
              class="kb-chapter"
              :class="{ major: c.level === 1 }"
              :title="`就「${c.title}」提问`"
              @click="askChapter(c.title)"
            >{{ c.title }}</button>
          </div>
        </section>

        <section class="kb-block">
          <div class="kb-block-head">
            <h3>文档</h3>
            <span class="kb-block-hint">{{ current.document_count || 0 }} 份</span>
          </div>
          <ul v-if="documents.length" class="kb-docs">
            <li v-for="d in documents" :key="d.doc_id">
              <router-link :to="`/units/${current.unit_id}`" class="kb-doc">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M7 3h7l4 4v14H7zM14 3v5h5" /></svg>
                <span class="kb-doc-name">{{ d.filename }}</span>
                <span class="kb-doc-meta">{{ d.doc_type || '文档' }} · {{ d.chunk_count }} 个片段</span>
              </router-link>
            </li>
          </ul>
          <div v-else class="kb-block-empty">
            还没有文档。
            <button type="button" class="kb-inline-btn" @click="uploadToUnit(current)">上传第一份文档</button>
          </div>
        </section>
      </section>

      <section v-else class="kb-stage kb-stage-placeholder">
        <p>{{ loading ? '正在读取知识库…' : '选择左侧的知识库，查看它讲了什么、能问什么。' }}</p>
      </section>
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
import { computed, onMounted, ref, watch } from 'vue'
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
  chapter_count?: number
  chapters?: string[]
  doc_names?: string[]
  last_indexed_at?: string | null
  doc_types?: string[]
  expert_id?: string
  skill_ids?: string[]
  tool_names?: string[]
  tags?: string[]
}

interface Chapter {
  title: string
  level: number
  doc_id: string
}

interface Question {
  question: string
  source?: string
  doc_name?: string
}

interface DocRow {
  doc_id: string
  filename: string
  doc_type: string
  chunk_count: number
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

// 选中态就是「当前重点」。默认落在第一个有内容的库上，
// 而不是某个空库——打开页面先看到内容比先看到空状态有用。
const selectedId = ref('')
const keyword = ref('')
const loading = ref(false)
const loadingDetail = ref(false)
const saving = ref(false)
const error = ref('')
const categories = ref<string[]>([])
const industries = ref<string[]>([])
const experts = ref<AgentOption[]>([])
const skills = ref<AgentOption[]>([])

const examples = ref<Question[]>([])
const chapters = ref<Chapter[]>([])
const documents = ref<DocRow[]>([])

// 切库再切回来不必重新请求：右侧内容在会话期内不会变。
const detailCache = new Map<string, { examples: Question[]; chapters: Chapter[]; documents: DocRow[] }>()

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

const filtered = computed(() => {
  const key = keyword.value.trim().toLowerCase()
  if (!key) return units.value
  return units.value.filter((u) =>
    u.name.toLowerCase().includes(key) ||
    (u.unit_type || '').toLowerCase().includes(key) ||
    (u.industry || '').toLowerCase().includes(key) ||
    (u.chapters || []).some((c) => c.toLowerCase().includes(key)) ||
    (u.doc_names || []).some((d) => d.toLowerCase().includes(key)) ||
    (u.tags || []).some((tag) => tag.toLowerCase().includes(key)),
  )
})

const current = computed(() => units.value.find((u) => u.unit_id === selectedId.value) || null)

function unitInitial(name: string) {
  return Array.from(name.trim())[0] || '知'
}

function chatWithUnit(unit: Unit) {
  router.push({ path: '/agent', query: { unit: unit.unit_id } })
}

// 点问题直接进对话并自动发出，中间不再需要用户复制粘贴。
function askQuestion(question: string) {
  if (!current.value) return
  router.push({ path: '/agent', query: { unit: current.value.unit_id, q: question } })
}

// 章节用「」而不是《》：章节不是文件，套书名号会让模型先否认「没有这份文档」。
function askChapter(title: string) {
  askQuestion(`「${title}」主要讲了什么？`)
}

function uploadToUnit(unit: Unit) {
  router.push({ path: '/upload', query: { unit: unit.unit_id } })
}

async function loadDetail(unit: Unit) {
  const cached = detailCache.get(unit.unit_id)
  if (cached) {
    examples.value = cached.examples
    chapters.value = cached.chapters
    documents.value = cached.documents
    return
  }
  loadingDetail.value = true
  examples.value = []
  chapters.value = []
  documents.value = []
  try {
    const [ex, outline, docs] = await Promise.all([
      axios.get(`/api/v1/units/${unit.unit_id}/examples`, { params: { limit: 4 }, timeout: 40000 }),
      axios.get(`/api/v1/units/${unit.unit_id}/outline`),
      axios.get(`/api/v1/units/${unit.unit_id}/documents`),
    ])
    const allChapters: Chapter[] = (outline.data.documents || [])
      .flatMap((d: any) => (d.entries || []).map((e: any) => ({ ...e, doc_id: d.doc_id })))
    // 这里只是「框架速览」，只放章级标题；条级留给完整目录，混在一起
    // 会让人分不清哪是章、哪是条。没有章级结构时再退回全部条目。
    const majors = allChapters.filter((e) => e.level === 1)
    const chapterList: Chapter[] = (majors.length ? majors : allChapters)
      .sort((a: Chapter, b: Chapter) => a.level - b.level)
      .slice(0, 16)
    examples.value = ex.data.questions || []
    chapters.value = chapterList
    documents.value = (docs.data || []).slice(0, 4)
    detailCache.set(unit.unit_id, {
      examples: examples.value,
      chapters: chapters.value,
      documents: documents.value,
    })
  } catch {
    examples.value = []
    chapters.value = []
    documents.value = []
  } finally {
    loadingDetail.value = false
  }
}

watch(current, (unit) => {
  if (unit) loadDetail(unit)
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    units.value = (await axios.get('/api/v1/units')).data
    const exists = units.value.some((u) => u.unit_id === selectedId.value)
    if (!exists) {
      const first = units.value.find((u) => (u.document_count || 0) > 0) || units.value[0]
      selectedId.value = first?.unit_id || ''
    }
  } catch (e: any) {
    units.value = []
    error.value = e.response?.status === 503
      ? '数据库未配置，无法读取知识库列表。'
      : e.response?.data?.error || '读取知识库列表失败，请确认服务可用。'
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
    // meta 加载失败不影响主体内容。
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
    ElMessage.warning('请填写知识库名称')
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
      ElMessage.success('知识库已更新')
    } else {
      const { data } = await axios.post('/api/v1/units', payload)
      ElMessage.success('知识库已创建')
      if (data?.unit_id) selectedId.value = data.unit_id
    }
    detailCache.clear()
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
.kb-page {
  max-width: 1240px;
}

.kb-page-head {
  align-items: flex-end;
  margin-bottom: 18px;
}

.kb-head-copy h1 {
  margin: 2px 0 0;
}

.kb-page-head .head-actions :deep(.el-button svg) {
  width: 14px;
  height: 14px;
  margin-right: 4px;
}

.kb-layout {
  display: grid;
  grid-template-columns: 250px minmax(0, 1fr);
  gap: 18px;
  align-items: start;
}

/* ---------------- 左栏：库清单 ---------------- */
.kb-rail {
  position: sticky;
  top: 12px;
  border: 1px solid var(--border);
  border-radius: 14px;
  background: var(--surface);
  overflow: hidden;
}

.kb-rail-search {
  padding: 10px 10px 8px;
  border-bottom: 1px solid var(--border);
}

.kb-rail-search-icon {
  width: 13px;
  height: 13px;
  color: var(--text-muted);
}

.kb-rail-list {
  max-height: calc(100vh - 250px);
  overflow-y: auto;
  padding: 6px;
}

.kb-rail-item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 9px 10px;
  margin-bottom: 2px;
  border: 0;
  border-radius: 10px;
  background: transparent;
  text-align: left;
  cursor: pointer;
  transition: background 0.14s ease;
}

.kb-rail-item:hover {
  background: var(--bg-subtle);
}

.kb-rail-item.active {
  background: var(--accent-soft);
}

.kb-rail-initial {
  flex: 0 0 auto;
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  border-radius: 8px;
  background: var(--bg-hover);
  color: var(--text-secondary);
  font-size: calc(12px * var(--fs-scale));
  font-weight: 600;
}

.kb-rail-item.active .kb-rail-initial {
  background: var(--accent);
  color: #fff;
}

.kb-rail-body {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.kb-rail-name {
  font-size: calc(13px * var(--fs-scale));
  font-weight: 500;
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.kb-rail-meta {
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.kb-rail-topics {
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.kb-rail-empty {
  padding: 18px 10px;
  margin: 0;
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-muted);
  text-align: center;
}

/* ---------------- 右栏：当前库的工作台 ---------------- */
.kb-stage {
  min-width: 0;
  border: 1px solid var(--border);
  border-radius: 14px;
  background: var(--surface);
  padding: 20px 22px 22px;
}

.kb-stage-placeholder {
  display: grid;
  place-items: center;
  min-height: 320px;
  color: var(--text-muted);
  font-size: calc(13px * var(--fs-scale));
}

.kb-stage-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--border);
}

.kb-stage-title h2 {
  margin: 0 0 6px;
  font-size: calc(23px * var(--fs-scale));
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--text);
}

.kb-stage-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 14px;
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-muted);
}

.kb-meta-strong {
  color: var(--text-secondary);
}

.kb-stage-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 0 0 auto;
}

.kb-block {
  padding-top: 18px;
}

.kb-block-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin-bottom: 10px;
}

.kb-block-head h3 {
  margin: 0;
  font-size: calc(13px * var(--fs-scale));
  font-weight: 600;
  letter-spacing: 0.02em;
  color: var(--text);
}

.kb-block-hint {
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.kb-block-link {
  margin-left: auto;
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
  text-decoration: none;
}

.kb-block-link:hover {
  color: var(--accent);
}

/* 问题清单是首屏重点：一行一问，序号负责扫读，来源负责可信。 */
.kb-questions {
  margin: 0;
  padding: 0;
  list-style: none;
  border: 1px solid var(--border);
  border-radius: 12px;
  overflow: hidden;
}

.kb-questions li + li {
  border-top: 1px solid var(--border);
}

.kb-question {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 11px 14px;
  border: 0;
  background: transparent;
  text-align: left;
  cursor: pointer;
  transition: background 0.14s ease;
}

.kb-question:hover {
  background: var(--bg-subtle);
}

.q-index {
  flex: 0 0 auto;
  font-variant-numeric: tabular-nums;
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
  letter-spacing: 0.04em;
}

.q-text {
  flex: 1;
  min-width: 0;
  font-size: calc(14px * var(--fs-scale));
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.q-source {
  flex: 0 0 auto;
  max-width: 220px;
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.q-go {
  flex: 0 0 auto;
  color: var(--text-muted);
  opacity: 0;
  transition: opacity 0.14s ease, transform 0.14s ease;
}

.kb-question:hover .q-go {
  opacity: 1;
  transform: translateX(2px);
}

.kb-skeleton {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 4px 0;
}

.kb-skeleton-row {
  display: block;
  height: 38px;
  border-radius: 8px;
  background: linear-gradient(90deg, var(--bg-subtle), var(--bg-hover), var(--bg-subtle));
  background-size: 200% 100%;
  animation: kb-shimmer 1.2s ease-in-out infinite;
}

@keyframes kb-shimmer {
  0% { background-position: 200% 0; }
  100% { background-position: -200% 0; }
}

.kb-chapters {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.kb-chapter {
  max-width: 100%;
  padding: 4px 10px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: transparent;
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-secondary);
  cursor: pointer;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  transition: border-color 0.14s ease, color 0.14s ease;
}

.kb-chapter:hover {
  border-color: var(--accent);
  color: var(--accent);
}

.kb-chapter.major {
  border-color: var(--border-strong);
  color: var(--text);
  font-weight: 500;
}

.kb-docs {
  margin: 0;
  padding: 0;
  list-style: none;
}

.kb-doc {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 2px;
  border-bottom: 1px dashed var(--border);
  text-decoration: none;
}

.kb-docs li:last-child .kb-doc {
  border-bottom: 0;
}

.kb-doc svg {
  flex: 0 0 auto;
  width: 15px;
  height: 15px;
  color: var(--text-muted);
}

.kb-doc-name {
  flex: 1;
  min-width: 0;
  font-size: calc(13px * var(--fs-scale));
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.kb-doc:hover .kb-doc-name {
  color: var(--accent);
}

.kb-doc-meta {
  flex: 0 0 auto;
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.kb-block-empty {
  padding: 14px;
  border: 1px dashed var(--border);
  border-radius: 10px;
  font-size: calc(12px * var(--fs-scale));
  line-height: 1.7;
  color: var(--text-muted);
}

.kb-inline-btn {
  padding: 0;
  border: 0;
  background: none;
  color: var(--accent);
  font-size: calc(12px * var(--fs-scale));
  cursor: pointer;
}

@media (max-width: 980px) {
  .kb-layout {
    grid-template-columns: 1fr;
  }

  .kb-rail {
    position: static;
  }

  .kb-rail-list {
    max-height: 220px;
  }
}
</style>
