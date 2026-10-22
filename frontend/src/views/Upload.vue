<template>
  <div class="page upload-page">
    <header class="page-head upload-page-head">
      <div>
        <div class="eyebrow">Knowledge Ingestion</div>
        <h1>文档入库</h1>
        <p class="subtitle">选择知识库和文件，系统会自动解析、切块并建立向量索引。</p>
      </div>
      <div class="upload-head-flow" aria-label="入库流程">
        <span><i>1</i>上传</span>
        <b>→</b>
        <span><i>2</i>解析</span>
        <b>→</b>
        <span><i>3</i>索引</span>
      </div>
    </header>

    <div v-if="unitsError" class="notice error">{{ unitsError }}</div>

    <div class="upload-layout">
      <div class="upload-main-column">
        <section class="card upload-form-card">
          <div class="upload-card-heading">
            <div>
              <div class="card-title">上传新文档</div>
              <p>填写归档信息并选择文件，提交后可以离开当前页面。</p>
            </div>
            <span class="upload-format-count">7 种格式</span>
          </div>
          <el-form label-position="top">
            <el-form-item label="所属项目" required>
              <template #label>
                <span class="upload-field-label"><i>1</i>归属知识库</span>
              </template>
              <el-select
                v-model="unit"
                placeholder="选择已有项目，或直接输入名称新建"
                style="width: 100%"
                filterable
                allow-create
                default-first-option
                clearable
              >
                <el-option v-for="u in units" :key="u.unit_id" :label="u.name" :value="u.unit_id" />
              </el-select>
              <div class="muted" style="margin-top: 6px">输入不存在的名称会自动创建项目，无需先建档。</div>
            </el-form-item>

            <el-form-item label="文档类型" required>
              <template #label>
                <span class="upload-field-label"><i>2</i>文档类型</span>
              </template>
              <el-select v-model="docType" style="width: 100%">
                <el-option v-for="x in types" :key="x" :label="x" :value="x" />
              </el-select>
            </el-form-item>

            <el-form-item label="选择文件" required>
              <template #label>
                <span class="upload-field-label"><i>3</i>选择文件</span>
              </template>
              <div class="upload-zone" style="width: 100%">
                <el-upload
                  ref="uploader"
                  drag
                  :show-file-list="false"
                  :auto-upload="false"
                  :limit="1"
                  :disabled="busy"
                  :on-change="fileChanged"
                  :on-remove="clearFile"
                  :on-exceed="onExceed"
                  accept=".txt,.md,.markdown,.json,.html,.htm,.docx,.pdf,.csv"
                >
                  <div class="upload-zone-content">
                    <span class="upload-zone-icon">
                      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
                        <path d="M12 16V5m0 0L8 9m4-4 4 4" />
                        <path d="M5 15v3a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-3" />
                      </svg>
                    </span>
                    <div>
                      <strong>拖拽文件到这里</strong>
                      <span>或点击浏览本地文件</span>
                    </div>
                    <em>单个文件最大 {{ maxMB }} MB</em>
                  </div>
                </el-upload>
              </div>
            </el-form-item>

            <div v-if="file" class="selected-file">
              <span class="selected-file-icon">{{ fileExtension }}</span>
              <div class="selected-file-main">
                <b>{{ file.name }}</b>
                <span>{{ formatFileSize(file.size) }} · 准备上传</span>
              </div>
              <button type="button" :disabled="busy" @click="removeSelectedFile">移除</button>
            </div>

            <div v-if="fileError" class="notice error" style="margin-bottom: 14px">{{ fileError }}</div>

            <div class="upload-submit-bar">
              <div>
                <b>{{ uploadReady ? '信息已准备完成' : '还差一步即可开始' }}</b>
                <span>{{ uploadReady ? `${unitLabel(unit)} · ${docType} · ${formatFileSize(file?.size || 0)}` : '请确认知识库、文档类型和上传文件' }}</span>
              </div>
              <el-button class="upload-submit-btn" type="primary" size="large" :disabled="!uploadReady" :loading="busy" @click="submit">
                {{ busy ? '正在处理…' : '开始入库' }}
                <svg v-if="!busy" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M5 12h14m-5-5 5 5-5 5" />
                </svg>
              </el-button>
            </div>
          </el-form>
        </section>

        <section v-if="job" class="card upload-progress-card">
          <div class="upload-card-heading compact">
            <div>
              <div class="card-title">当前任务</div>
              <p>{{ job.filename || '正在处理本次上传' }}</p>
            </div>
            <el-tag size="small" :type="statusType" effect="light">{{ statusText }}</el-tag>
          </div>
          <div class="job-phases">
            <span class="job-phase" :class="jobStageState(job.phase, job.status, 'upload')">上传</span>
            <span class="job-phase-sep">→</span>
            <span class="job-phase" :class="jobStageState(job.phase, job.status, 'parse')">解析</span>
            <span class="job-phase-sep">→</span>
            <span class="job-phase" :class="jobStageState(job.phase, job.status, 'index')">索引</span>
          </div>
          <el-progress
            :percentage="job.progress || 0"
            :status="job.status === 'failed' ? 'exception' : job.status === 'done' ? 'success' : ''"
            :stroke-width="6"
          />
          <p class="upload-job-message">
            {{ job.message }}
            <span v-if="job.chunks"> · {{ job.chunks }} 个文本块</span>
          </p>
          <el-button
            v-if="job.status === 'failed' && job.job_id"
            link
            type="primary"
            :loading="busy"
            @click="retryIndex(job.job_id)"
          >重新索引</el-button>
          <el-button v-if="job.status === 'failed'" link type="primary" @click="resetJob">重新上传</el-button>
        </section>

        <section class="card upload-history-card">
          <div class="upload-card-heading compact">
            <div>
              <div class="card-title">最近入库任务</div>
              <p>查看解析、索引进度，失败任务可直接重试。</p>
            </div>
            <div style="display: flex; gap: 8px; align-items: center">
              <el-tag v-if="activeCount" size="small" type="warning" effect="light">{{ activeCount }} 个进行中</el-tag>
              <el-button link type="primary" size="small" @click="loadRecent">刷新</el-button>
            </div>
          </div>
          <div v-if="!recent.length" class="muted">暂无入库记录。</div>
          <div v-for="j in recent" :key="j.job_id" class="recent-row">
            <div class="recent-main">
              <b>{{ j.filename }}</b>
              <span class="muted">{{ j.doc_type }} · {{ unitLabel(j.unit_id || '') }}</span>
            </div>
            <div class="recent-side">
              <el-tag size="small" :type="tagType(j.status)" effect="light">{{ jobPhaseLabel(j.phase, j.status) }}</el-tag>
              <span class="muted">
                {{ j.progress }}%<template v-if="j.chunks"> · {{ j.chunks }} 块</template>
              </span>
              <el-button
                v-if="j.status === 'failed'"
                link
                type="primary"
                size="small"
                :disabled="busy"
                @click="retryIndex(j.job_id || '')"
              >重新索引</el-button>
              <button v-if="j.job_id" class="recent-open" type="button" @click="openJob(j.job_id)">查看</button>
            </div>
            <div v-if="j.message" class="muted recent-msg">{{ j.message }}</div>
          </div>
        </section>
      </div>

      <aside class="upload-side-column">
        <section class="card upload-guide-card">
          <div class="card-title">入库会经历什么</div>
          <p class="upload-guide-intro">提交后任务在后台执行，可以放心离开页面。</p>
          <ol class="upload-flow-list">
            <li>
              <span>1</span>
              <div><b>保存文件</b><p>原件安全保存，等待后台处理。</p></div>
            </li>
            <li>
              <span>2</span>
              <div><b>提取与解析</b><p>读取正文，PDF 和 DOCX 自动抽取文本。</p></div>
            </li>
            <li>
              <span>3</span>
              <div><b>切块与索引</b><p>生成文本块和向量，完成后即可问答检索。</p></div>
            </li>
          </ol>
        </section>

        <section class="card upload-guide-card">
          <div class="card-title">上传前确认</div>
          <div class="upload-format-tags">
            <span v-for="ext in allowedExtensions.slice(0, 7)" :key="ext">{{ ext.toUpperCase() }}</span>
          </div>
          <ul class="upload-check-list">
            <li>单个文件不超过 {{ maxMB }} MB</li>
            <li>优先上传可复制文字的 PDF，扫描件需要 OCR</li>
            <li>同名文件重新上传会覆盖旧版本</li>
            <li>1800 字切块、240 字重叠</li>
          </ul>
        </section>
      </aside>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import axios from 'axios'
import { genFileId } from 'element-plus'
import { jobPhaseLabel, jobStageState } from '../jobPhase'

interface Unit {
  unit_id: string
  name: string
}
interface Job {
  job_id?: string
  unit_id?: string
  doc_type?: string
  filename?: string
  status: string
  phase?: string
  progress: number
  message: string
  chunks?: number
  created_at?: string
}

// 与后端 MAX_UPLOAD_MB 保持一致（后端最终以环境变量为准）。
const maxMB = 50
const allowedExtensions = ['txt', 'md', 'markdown', 'json', 'html', 'htm', 'csv', 'docx', 'pdf']
const types = ['制度', '预案', '检测报告', '台账', '其他']

const route = useRoute()
const units = ref<Unit[]>([])
const unit = ref('')
const docType = ref('制度')
const file = ref<File | null>(null)
const fileError = ref('')
const unitsError = ref('')
const job = ref<Job | null>(null)
const busy = ref(false)
const uploader = ref<any>(null)
const recent = ref<Job[]>([])
let timer: ReturnType<typeof setTimeout> | undefined
let recentTimer: ReturnType<typeof setInterval> | undefined

const statusText = computed(() =>
  job.value ? jobPhaseLabel(job.value.phase, job.value.status) : '—',
)
const statusType = computed(() => {
  switch (job.value?.status) {
    case 'done': return 'success'
    case 'failed': return 'danger'
    case 'running': return 'warning'
    default: return 'info'
  }
})

const activeCount = computed(
  () => recent.value.filter((j) => j.status === 'queued' || j.status === 'running').length,
)
const uploadReady = computed(() => !!file.value && !!unit.value.trim() && !busy.value)
const fileExtension = computed(() => {
  const name = file.value?.name || ''
  return (name.split('.').pop() || 'FILE').toUpperCase()
})

function formatFileSize(bytes: number) {
  if (!bytes) return '0 KB'
  if (bytes < 1024 * 1024) return `${Math.max(1, Math.round(bytes / 1024))} KB`
  return `${(bytes / 1024 / 1024).toFixed(bytes < 10 * 1024 * 1024 ? 1 : 0)} MB`
}

function unitLabel(id: string) {
  const match = units.value.find((item) => item.unit_id === id || item.name === id)
  return match?.name || id || '未指定知识库'
}

function tagType(status: string) {
  switch (status) {
    case 'done': return 'success'
    case 'failed': return 'danger'
    case 'running': return 'warning'
    default: return 'info'
  }
}

async function loadRecent() {
  try {
    const { data } = await axios.get('/api/v1/jobs', { params: { limit: 10 } })
    recent.value = data?.items || []
  } catch {
    // 列表接口依赖数据库，失败时静默保留上一次结果。
  }
}

// 从文档库「查看任务」进来时直接定位该任务；失败状态也能看到完整原因并重试。
async function openJob(jobId: string) {
  try {
    const { data } = await axios.get(`/api/v1/jobs/${encodeURIComponent(jobId)}`)
    job.value = data
    localStorage.setItem('fa-last-job', jobId)
    const active = data.status === 'queued' || data.status === 'running'
    busy.value = active
    if (active) schedulePoll(jobId)
  } catch (e: any) {
    job.value = { status: 'failed', progress: 0, message: errorMessage(e) }
    busy.value = false
  }
}

// 切换页面回来后恢复上次提交的任务，避免误以为任务被丢弃。
async function resumeLastJob() {
  const saved = localStorage.getItem('fa-last-job')
  if (!saved) return
  try {
    const { data } = await axios.get(`/api/v1/jobs/${encodeURIComponent(saved)}`)
    job.value = data
    const active = data.status === 'queued' || data.status === 'running'
    busy.value = active
    if (active) schedulePoll(saved)
  } catch {
    localStorage.removeItem('fa-last-job')
  }
}

async function loadUnits() {
  try {
    units.value = (await axios.get('/api/v1/units')).data
    unitsError.value = ''
  } catch (e: any) {
    unitsError.value = e.response?.status === 503
      ? '数据库未配置，无法读取项目。'
      : e.response?.data?.error || '读取项目列表失败。'
  }
}

onMounted(async () => {
  await loadUnits()
  const queryUnit = route.query.unit
  if (queryUnit) unit.value = String(queryUnit)
  await loadRecent()
  const queryJob = String(route.query.job || '')
  if (queryJob) await openJob(queryJob)
  else await resumeLastJob()
  // 有任务在跑时保持列表刷新，离开页面后由顶部状态条接管提示。
  recentTimer = setInterval(loadRecent, 5000)
})
onUnmounted(() => {
  clearTimeout(timer)
  clearInterval(recentTimer)
})

function clearFile() {
  file.value = null
}

function removeSelectedFile() {
  if (busy.value) return
  clearFile()
  fileError.value = ''
  uploader.value?.clearFiles()
}

function onExceed(files: File[]) {
  uploader.value?.clearFiles()
  const next = files[0] as any
  if (!next) return
  // Element Plus requires a uid when replacing the selected file.
  next.uid = genFileId()
  uploader.value?.handleStart(next)
  file.value = next as File
  fileError.value = ''
}

function fileChanged(f: any) {
  const name: string = f.name || ''
  const ext = (name.split('.').pop() || '').toLowerCase()
  fileError.value = ''
  if (!allowedExtensions.includes(ext)) {
    fileError.value = `不支持的文件类型：${ext ? '.' + ext : '未知'}`
    clearFile()
    uploader.value?.clearFiles()
    return
  }
  if (f.size && f.size > maxMB * 1024 * 1024) {
    fileError.value = `文件超过 ${maxMB} MB 上限`
    clearFile()
    uploader.value?.clearFiles()
    return
  }
  file.value = f.raw
}

function errorMessage(e: any) {
  return e.response?.data?.error || e.message || '请求失败'
}

async function submit() {
  if (!file.value || !unit.value.trim() || busy.value) return
  busy.value = true
  fileError.value = ''
  job.value = { status: 'queued', phase: 'upload', progress: 0, message: '正在上传文件…' }
  const value = unit.value.trim()
  const existing = units.value.find((u) => u.unit_id === value || u.name === value)
  const data = new FormData()
  data.append('file', file.value)
  // Existing selection sends unit_id; a typed name is resolved/created server-side.
  if (existing) data.append('unit_id', existing.unit_id)
  else data.append('unit_name', value)
  data.append('doc_type', docType.value)
  try {
    const res = await axios.post('/api/v1/upload', data, {
      onUploadProgress: (event: any) => {
        if (!event.total || !job.value) return
        const percent = Math.min(100, Math.round((event.loaded / event.total) * 100))
        job.value = {
          ...job.value,
          progress: percent,
          message: percent < 100 ? `正在上传文件… ${percent}%` : '文件上传完成，等待后台入库…',
        }
      },
    })
    job.value = res.data
    if (res.data?.job_id) localStorage.setItem('fa-last-job', res.data.job_id)
    schedulePoll(res.data.job_id)
    await loadRecent()
  } catch (e: any) {
    // 解析失败时后端仍保留源文件并记录失败任务，带上 job_id 后可直接重试。
    const data = e.response?.data || {}
    job.value = {
      status: 'failed',
      progress: 0,
      message: errorMessage(e),
      ...(data.job_id ? { job_id: data.job_id, filename: data.filename } : {}),
    }
    busy.value = false
  }
}

// 重新索引：复用已保存的源文件，修好解析环境或模型恢复后不必重新上传。
async function retryIndex(jobId: string) {
  if (!jobId || busy.value) return
  try {
    await axios.post(`/api/v1/jobs/${encodeURIComponent(jobId)}/retry`)
    job.value = { job_id: jobId, status: 'queued', progress: 0, message: '已重新排队，等待索引' }
    localStorage.setItem('fa-last-job', jobId)
    busy.value = true
    schedulePoll(jobId)
    await loadRecent()
  } catch (e: any) {
    if (job.value) job.value = { ...job.value, status: 'failed', message: errorMessage(e) }
    else fileError.value = errorMessage(e)
  }
}

function schedulePoll(jobId: string) {
  clearTimeout(timer)
  timer = setTimeout(async () => {
    try {
      const { data } = await axios.get(`/api/v1/jobs/${encodeURIComponent(jobId)}`)
      job.value = data
      if (data.status === 'done' || data.status === 'failed') {
        busy.value = false
        if (data.status === 'done') await loadUnits()
        await loadRecent()
        return
      }
      schedulePoll(jobId)
    } catch (e: any) {
      job.value = { ...(job.value as Job), status: 'failed', message: errorMessage(e) }
      busy.value = false
    }
  }, 1500)
}

function resetJob() {
  clearTimeout(timer)
  job.value = null
  file.value = null
  fileError.value = ''
  uploader.value?.clearFiles()
}
</script>

<style scoped>
.upload-page {
  max-width: 1180px;
}

.upload-page-head {
  align-items: flex-end;
}

.upload-head-flow {
  display: flex;
  flex: none;
  align-items: center;
  gap: 7px;
  padding: 8px 11px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--surface);
  color: var(--text-muted);
  font-size: 11px;
}

.upload-head-flow span {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.upload-head-flow i {
  display: grid;
  width: 17px;
  height: 17px;
  place-items: center;
  border-radius: 50%;
  background: var(--bg-subtle);
  color: var(--text-secondary);
  font-size: 9px;
  font-style: normal;
  font-weight: 700;
}

.upload-head-flow b {
  color: var(--border-strong);
  font-weight: 400;
}

.upload-layout {
  grid-template-columns: minmax(0, 1fr) 320px;
  gap: 18px;
}

.upload-main-column,
.upload-side-column {
  display: grid;
  gap: 16px;
}

.upload-side-column {
  position: sticky;
  top: 18px;
  align-self: start;
}

.upload-form-card {
  overflow: hidden;
  padding: 0;
}

.upload-card-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding: 20px 22px;
  border-bottom: 1px solid var(--border);
}

.upload-card-heading.compact {
  padding: 0 0 14px;
  border-bottom: 1px solid var(--border);
}

.upload-card-heading .card-title {
  margin: 0;
  font-size: 15px;
}

.upload-card-heading p,
.upload-guide-intro {
  margin: 5px 0 0;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.6;
}

.upload-format-count {
  flex: none;
  padding: 4px 8px;
  border-radius: 999px;
  background: var(--bg-subtle);
  color: var(--text-muted);
  font-size: 11px;
}

.upload-form-card :deep(.el-form) {
  padding: 20px 22px 0;
}

.upload-form-card :deep(.el-form-item) {
  margin-bottom: 20px;
}

.upload-form-card :deep(.el-form-item__label) {
  height: auto;
  padding-bottom: 8px;
}

.upload-field-label {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 600;
}

.upload-field-label i {
  display: grid;
  width: 18px;
  height: 18px;
  place-items: center;
  border-radius: 6px;
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 9px;
  font-style: normal;
  font-weight: 700;
}

.upload-zone :deep(.el-upload-dragger) {
  padding: 30px 24px;
  border-color: color-mix(in srgb, var(--accent) 24%, var(--border-strong));
  background: color-mix(in srgb, var(--accent) 3%, var(--bg-subtle));
}

.upload-zone-content {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
}

.upload-zone-icon {
  display: grid;
  width: 44px;
  height: 44px;
  place-items: center;
  border: 1px solid color-mix(in srgb, var(--accent) 16%, var(--border));
  border-radius: 13px;
  background: var(--surface);
  color: var(--accent);
  box-shadow: var(--shadow-sm);
}

.upload-zone-icon svg {
  width: 22px;
  height: 22px;
}

.upload-zone-content strong,
.upload-zone-content span {
  display: block;
}

.upload-zone-content strong {
  color: var(--text);
  font-size: 14px;
  font-weight: 620;
}

.upload-zone-content span {
  margin-top: 3px;
  color: var(--text-muted);
  font-size: 12px;
}

.upload-zone-content em {
  color: var(--text-muted);
  font-size: 10px;
  font-style: normal;
}

.selected-file {
  display: flex;
  align-items: center;
  gap: 11px;
  margin: -3px 0 18px;
  padding: 11px 12px;
  border: 1px solid var(--border);
  border-radius: 11px;
  background: var(--bg-subtle);
}

.selected-file-icon {
  display: grid;
  width: 38px;
  height: 38px;
  flex: none;
  place-items: center;
  border-radius: 9px;
  background: var(--surface);
  color: var(--accent);
  font-size: 9px;
  font-weight: 750;
  letter-spacing: 0.04em;
}

.selected-file-main {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-direction: column;
  gap: 3px;
}

.selected-file-main b {
  overflow: hidden;
  color: var(--text);
  font-size: 12px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.selected-file-main span {
  color: var(--text-muted);
  font-size: 11px;
}

.selected-file button {
  padding: 4px 7px;
  border: none;
  border-radius: 7px;
  background: transparent;
  color: var(--text-muted);
  font-size: 11px;
  cursor: pointer;
}

.selected-file button:hover {
  background: var(--bg-hover);
  color: var(--danger);
}

.upload-submit-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  margin: 2px -22px 0;
  padding: 16px 22px;
  border-top: 1px solid var(--border);
  background: var(--bg-subtle);
}

.upload-submit-bar > div {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 3px;
}

.upload-submit-bar b {
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 600;
}

.upload-submit-bar span {
  overflow: hidden;
  color: var(--text-muted);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.upload-submit-btn {
  flex: none;
  min-width: 122px;
}

.upload-submit-btn svg {
  width: 15px;
  height: 15px;
  margin-left: 5px;
}

.upload-progress-card,
.upload-history-card {
  padding: 18px 20px;
}

.upload-job-message {
  margin: 10px 0 0;
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.6;
}

.upload-guide-card {
  padding: 20px;
}

.upload-guide-card .card-title {
  margin-bottom: 6px;
  font-size: 14px;
}

.upload-flow-list {
  display: grid;
  gap: 16px;
  margin: 18px 0 0;
  padding: 0;
  list-style: none;
}

.upload-flow-list li {
  display: grid;
  grid-template-columns: 24px minmax(0, 1fr);
  gap: 10px;
  position: relative;
}

.upload-flow-list li:not(:last-child)::after {
  position: absolute;
  top: 27px;
  bottom: -12px;
  left: 11px;
  width: 1px;
  background: var(--border);
  content: '';
}

.upload-flow-list li > span {
  position: relative;
  z-index: 1;
  display: grid;
  width: 23px;
  height: 23px;
  place-items: center;
  border: 1px solid var(--border);
  border-radius: 50%;
  background: var(--surface);
  color: var(--text-secondary);
  font-size: 10px;
  font-weight: 700;
}

.upload-flow-list b {
  display: block;
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 600;
}

.upload-flow-list p {
  margin: 3px 0 0;
  color: var(--text-muted);
  font-size: 11px;
  line-height: 1.55;
}

.upload-format-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 14px;
}

.upload-format-tags span {
  padding: 4px 7px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg-subtle);
  color: var(--text-secondary);
  font-size: 9px;
  font-weight: 650;
  letter-spacing: 0.03em;
}

.upload-check-list {
  display: grid;
  gap: 8px;
  margin: 14px 0 0;
  padding: 0;
  list-style: none;
}

.upload-check-list li {
  position: relative;
  padding-left: 15px;
  color: var(--text-muted);
  font-size: 11px;
  line-height: 1.6;
}

.upload-check-list li::before {
  position: absolute;
  top: 7px;
  left: 1px;
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--accent);
  content: '';
}

.recent-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 12px;
  padding: 10px 0;
  border-top: 1px solid var(--border, rgba(128, 128, 128, 0.16));
}
.recent-row:first-of-type {
  border-top: none;
}
.recent-main {
  flex: 1 1 220px;
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.recent-main b {
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.recent-main .muted {
  font-size: 12px;
}
.recent-side {
  display: flex;
  align-items: center;
  gap: 8px;
  white-space: nowrap;
}
.recent-open {
  padding: 3px 7px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: transparent;
  color: var(--text-secondary);
  font-size: 11px;
  cursor: pointer;
}
.recent-open:hover {
  border-color: var(--border-strong);
  background: var(--bg-subtle);
}
.recent-msg {
  flex: 1 1 100%;
  font-size: 12px;
}

@media (max-width: 1080px) {
  .upload-page-head {
    align-items: flex-start;
    flex-direction: column;
  }

  .upload-layout {
    grid-template-columns: 1fr;
  }

  .upload-side-column {
    position: static;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 620px) {
  .upload-head-flow {
    display: none;
  }

  .upload-side-column {
    grid-template-columns: 1fr;
  }

  .upload-card-heading,
  .upload-form-card :deep(.el-form),
  .upload-submit-bar {
    padding-right: 16px;
    padding-left: 16px;
  }

  .upload-submit-bar {
    align-items: stretch;
    flex-direction: column;
    margin-right: -16px;
    margin-left: -16px;
  }

  .upload-submit-btn {
    width: 100%;
  }

  .upload-zone :deep(.el-upload-dragger) {
    padding: 25px 14px;
  }

  .recent-side {
    width: 100%;
    flex-wrap: wrap;
    justify-content: flex-end;
  }
}
</style>
