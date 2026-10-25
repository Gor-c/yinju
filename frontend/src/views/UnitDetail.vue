<template>
  <div class="page unit-detail-page">
    <router-link to="/units" class="back-link">← 返回知识库</router-link>

    <header class="page-head">
      <div>
        <div class="eyebrow">Knowledge Base</div>
        <h1>{{ profile?.name || '知识库' }}</h1>
        <p class="subtitle">{{ profile?.unit_type || '知识库' }} · {{ profile?.industry || '未设置行业' }}</p>
        <div v-if="profile?.tags?.length" class="tag-row">
          <el-tag v-for="t in profile.tags" :key="t" size="small" effect="plain">{{ t }}</el-tag>
        </div>
      </div>
      <div class="head-actions">
        <el-button @click="openSettings">设置</el-button>
        <el-button @click="openUpload">上传文档</el-button>
        <el-button type="primary" @click="openAgent">进入 Agent</el-button>
      </div>
    </header>

    <div v-if="error" class="notice error">
      <span>{{ error }}</span>
      <el-button link @click="$router.push('/units')">返回列表</el-button>
    </div>

    <!-- 首屏回答「这个库讲了什么」：章节结构比「2 份文档 / 42 个片段」有信息量得多。 -->
    <section class="unit-framework" v-loading="loading">
      <div class="uf-head">
        <div class="uf-stats">
          <b>{{ outlineTotal }}</b> 个章节
          <span class="uf-sep">·</span>
          <b>{{ profile?.document_count ?? 0 }}</b> 份文档
          <span class="uf-sep">·</span>
          <b>{{ profile?.indexed_chunks ?? 0 }}</b> 个片段
        </div>
        <div class="uf-head-actions">
          <router-link class="uf-link" :to="`/units/${id}/outline`">完整目录 →</router-link>
          <el-button link type="primary" size="small" :loading="extracting" @click="extractOutline">
            {{ outlineTotal ? '重新提取' : '提取内容框架' }}
          </el-button>
        </div>
      </div>
      <div v-if="outlineGroups.length" class="uf-groups">
        <div v-for="g in outlineGroups" :key="g.docId" class="uf-group">
          <div class="uf-group-head">
            <span class="uf-group-name">{{ g.name }}</span>
            <span class="uf-group-count">{{ g.entries.length }} 项</span>
            <button
              v-if="g.entries.length > 8"
              type="button"
              class="uf-group-toggle"
              @click="toggleDoc(g.docId)"
            >{{ expanded[g.docId] ? '收起' : '展开全部' }}</button>
          </div>
          <div class="uf-list">
            <span
              v-for="e in (expanded[g.docId] ? g.entries : g.entries.slice(0, 8))"
              :key="e.doc_id + e.title"
              class="uf-item"
              :class="{ major: e.level === 1 }"
            >{{ e.title }}</span>
            <span v-if="!expanded[g.docId] && g.entries.length > 8" class="uf-more">
              还有 {{ g.entries.length - 8 }} 项
            </span>
          </div>
        </div>
      </div>
      <p v-else class="uf-empty">
        还没有可识别的章节结构。表格、扫描件、或没有条款编号的文档抽不出目录，内容仍可正常检索。
      </p>
    </section>

    <el-tabs v-model="tab" class="tabs unit-detail-tabs">
      <el-tab-pane :label="`文档 ${profile?.document_count ? `(${profile.document_count})` : ''}`" name="documents">
        <div class="panel" style="padding: 18px">
          <div v-if="activeJobs.length" class="tab-toolbar">
            <span class="muted">{{ profile?.indexed_chunks ?? 0 }} 个文本块</span>
            <el-tag size="small" type="warning" effect="light">
              {{ activeJobs.length }} 个任务进行中
            </el-tag>
          </div>

          <div v-for="j in activeJobs" :key="j.job_id" class="job-card">
            <div class="job-head">
              <b>{{ j.filename }}</b>
              <span class="muted">{{ jobPhaseLabel(j.phase, j.status) }} · {{ j.message }}</span>
            </div>
            <div class="job-phases">
              <span class="job-phase" :class="jobStageState(j.phase, j.status, 'upload')">上传</span>
              <span class="job-phase-sep">→</span>
              <span class="job-phase" :class="jobStageState(j.phase, j.status, 'parse')">解析</span>
              <span class="job-phase-sep">→</span>
              <span class="job-phase" :class="jobStageState(j.phase, j.status, 'index')">索引</span>
            </div>
            <el-progress :percentage="j.progress || 0" :stroke-width="6" />
            <div class="muted" style="font-size: calc(12px * var(--fs-scale)); margin-top: 4px">
              {{ j.doc_type }} · {{ j.chunks || 0 }} 块
              <template v-if="j.attempts"> · 第 {{ j.attempts }} 次尝试</template>
            </div>
          </div>

          <div v-if="failedJobs.length" class="job-card">
            <div class="muted" style="margin-bottom: 6px">最近的失败任务</div>
            <div v-for="j in failedJobs" :key="j.job_id" class="failed-row">
              <el-tag size="small" type="danger" effect="light">失败</el-tag>
              <span>{{ j.filename }}</span>
              <el-tooltip placement="top" :show-after="200">
                <template #content>
                  <div class="err-tip">
                    <p>{{ describeIndexError(j.message).hint }}</p>
                    <pre v-if="describeIndexError(j.message).raw">{{ describeIndexError(j.message).raw }}</pre>
                  </div>
                </template>
                <span class="muted failed-reason">{{ describeIndexError(j.message).title }}</span>
              </el-tooltip>
              <el-button link type="primary" size="small" @click="retryIndexJob(j.job_id)">重新解析</el-button>
            </div>
          </div>

          <DocumentExplorer
            scope="unit"
            :unit-id="id"
            dense
            preview-mode="emit"
            empty-title="这个知识库还没有文档"
            empty-hint="上传文档后会自动出现在这里。"
            @preview="openSource"
            @changed="refreshAfterDocChange"
          />
        </div>
      </el-tab-pane>
      <el-tab-pane label="Agent 问答" name="agent">
        <div class="panel" style="padding: 26px">
          <p class="subtitle" style="margin-bottom: 16px">带上该项目档案进入 Agent，检索回答会附上文件名和片段。</p>
          <el-button type="primary" @click="openAgent">打开 Agent →</el-button>
        </div>
      </el-tab-pane>

      <el-tab-pane label="知识图谱" name="graph">
        <div class="panel" style="padding: 18px">
          <div v-if="graphDocID" class="notice info graph-doc-filter">
            <span>仅显示文档「{{ graphDocName || graphDocID }}」相关的实体与关系</span>
            <el-button link type="primary" @click="clearDocFilter">查看整个知识库</el-button>
          </div>
          <div class="graph-header">
            <div class="graph-heading">
              <b>实体关系网络</b>
              <p>从档案中识别组织、人员、制度、风险和关键事件，并展示它们之间的联系。</p>
            </div>
            <div class="graph-header-actions">
              <span v-if="graphStats" class="graph-size-pill">{{ displayNodes.length }} 个可见实体 · {{ displayEdges.length }} 条关系</span>
              <el-button size="small" :loading="kgLoading" @click="runKGExtract">重建实体图谱</el-button>
            </div>
          </div>
          <div class="graph-controls">
            <el-input v-model="graphKeyword" clearable placeholder="搜索实体名称或描述" class="graph-search">
              <template #prefix>
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="11" cy="11" r="7" /><path d="m16.5 16.5 4 4" /></svg>
              </template>
            </el-input>
            <el-select v-model="graphKindFilter" clearable placeholder="全部实体类型" class="graph-kind-filter">
              <el-option v-for="item in graphKindOptions" :key="item.kind" :label="`${item.kind}（${item.count}）`" :value="item.kind" />
            </el-select>
            <div class="graph-view-toggles">
              <el-switch v-model="highConfidenceOnly" size="small" active-text="仅多来源关系" />
              <el-switch v-model="hideLeaves" size="small" active-text="隐藏边缘节点" />
            </div>
          </div>
          <div v-if="kgJob" class="kg-status">
            <span class="muted">
              状态：{{ kgStatusText }} · 片段 {{ kgJob.chunks_done }}/{{ kgJob.chunks_total }} · 实体 {{ kgJob.entities }} · 关系 {{ kgJob.relations }}
            </span>
            <span v-if="kgJob.error" class="muted">{{ kgJob.error }}</span>
          </div>
          <div v-if="graphError" class="notice error">{{ graphError }}</div>
          <div v-if="graphNodes.length && !graphError" class="graph-summary">
            <div><span>可见实体</span><b>{{ displayNodes.length }}</b></div>
            <div><span>可见关系</span><b>{{ displayEdges.length }}</b></div>
            <div><span>多来源确认</span><b>{{ multiSourceRelations }}</b></div>
            <div><span>主要类型</span><b>{{ topGraphKind }}</b></div>
          </div>
          <div v-if="graphNodes.length && !graphError" class="graph-insights">
            <section>
              <div class="graph-insight-head"><b>核心实体</b><span>按关联数量排序</span></div>
              <button v-for="item in topGraphEntities" :key="item.node.id" type="button" @click="focusGraphEntity(item.node)">
                <i :style="{ background: nodeColor(item.node) }"></i>
                <span>{{ item.node.label }}</span>
                <em>{{ item.degree }} 个关联</em>
              </button>
            </section>
            <section>
              <div class="graph-insight-head"><b>关键关系</b><span>优先展示多来源证据</span></div>
              <button v-for="item in topGraphRelations" :key="`${item.source}-${item.target}-${item.label}`" type="button" @click="focusGraphRelation(item)">
                <span>{{ item.sourceLabel }} → {{ item.targetLabel }}</span>
                <em>{{ item.label || '相关' }}<template v-if="(item.weight || 1) > 1"> ×{{ item.weight }}</template></em>
              </button>
            </section>
          </div>
          <div v-if="!graphError" class="graph-legend">
            <span v-for="item in legendItems" :key="item.label" class="graph-legend-item">
              <i :style="{ background: item.color }"></i>{{ item.label }}
            </span>
          </div>
          <div v-show="!graphError" class="graph-stage">
            <div ref="graphEl" v-loading="graphLoading" class="graph-canvas-2d">
              <canvas ref="graphCanvas" class="graph-canvas-surface"></canvas>
              <div v-if="!graphNodes.length && !graphLoading" class="graph-empty muted">
                还没有实体数据，点击右上角「重建实体图谱」开始抽取。
              </div>
              <div v-else-if="!displayNodes.length && !graphLoading" class="graph-empty muted">
                当前筛选条件下没有匹配的实体。
              </div>
              <div v-if="displayNodes.length" class="graph-canvas-hint">点击查看详情 · 空白处拖动平移 · 滚轮缩放</div>
              <button v-if="displayNodes.length" type="button" class="graph-fit-btn" title="适配画布" @click="fitGraph2D()">适配</button>
            </div>

            <aside class="graph-side-panel">
              <template v-if="selectedNode">
                <div class="graph-side-eyebrow">ENTITY DETAIL</div>
                <div class="graph-side-title">
                  <span :style="{ background: nodeColor(selectedNode) }"></span>
                  <h3>{{ selectedNode.label }}</h3>
                </div>
                <dl class="graph-properties">
                  <div><dt>类型</dt><dd>{{ selectedNode.kind || graphTypeLabel(selectedNode.type) }}</dd></div>
                  <div><dt>关联</dt><dd>{{ neighborRelations.length }} 个实体</dd></div>
                  <div><dt>实体 ID</dt><dd :title="selectedNode.id">{{ selectedNode.id.replace('entity:', '') }}</dd></div>
                </dl>
                <p class="graph-description">{{ selectedNode.detail || '暂无描述' }}</p>
                <div class="graph-side-subtitle">关联实体</div>
                <div v-if="neighborRelations.length" class="graph-neighbors">
                  <button
                    v-for="item in neighborRelations"
                    :key="item.id + item.relation"
                    type="button"
                    @click="selectNode(graphNodes.find((n) => n.id === item.id))"
                  >
                    <span class="dot" :style="{ background: item.color }"></span>
                    <b>{{ item.label }}</b>
                    <span class="graph-rel">{{ item.relation }}</span>
                    <span v-if="item.weight > 1" class="muted">×{{ item.weight }}</span>
                  </button>
                </div>
                <p v-else class="muted">没有可见的关联，可能被过滤条件隐藏了。</p>
                <el-button class="graph-side-close" link @click="clearSelection">清除选择</el-button>
              </template>

              <template v-else-if="selectedLink">
                <div class="graph-side-eyebrow">RELATION DETAIL</div>
                <div class="graph-side-title">
                  <span :style="{ background: selectedLinkColor }"></span>
                  <h3>{{ selectedLink.label || '关系' }}</h3>
                </div>
                <dl class="graph-properties">
                  <div><dt>状态</dt><dd>{{ (selectedLink.weight || 1) >= 2 ? '多来源确认' : '待确认' }}</dd></div>
                  <div><dt>来源片段</dt><dd>{{ selectedLink.weight || 1 }} 个</dd></div>
                </dl>
                <p class="graph-description">{{ selectedLink.evidence || '该关系由单个片段抽取，建议核对原文后再采信。' }}</p>
                <el-button class="graph-side-close" link @click="selectedLink = null">清除选择</el-button>
              </template>

              <template v-else>
                <div class="graph-side-eyebrow">GRAPH EXPLORER</div>
                <h3>选择一个实体</h3>
                <p class="graph-description">点击图中的节点查看描述和关联关系；点击核心实体或关键关系也可以直接定位。</p>
                <div class="graph-guide-list">
                  <span><i class="dot-node"></i>节点大小表示关联数量</span>
                  <span><i class="dot-line"></i>浅色节点表示关联较少</span>
                  <span><i class="dot-focus"></i>选中后高亮邻居子图</span>
                </div>
              </template>
            </aside>
          </div>
          <p class="muted" style="margin: 8px 0 0">
            实体与关系由本地配置的模型从档案片段抽取，仅存储在本项目数据库中。
          </p>
        </div>
      </el-tab-pane>

      <el-tab-pane label="设置" name="settings">
        <div class="panel settings-panel">
          <el-tabs v-model="settingsTab">
            <el-tab-pane label="基础信息" name="basic">
              <el-form label-width="96px" class="settings-form">
                <el-form-item label="项目名称" required>
                  <el-input v-model="settingsForm.name" maxlength="100" show-word-limit />
                </el-form-item>
                <el-form-item label="项目分类">
                  <el-select
                    v-model="settingsForm.unit_type"
                    filterable
                    allow-create
                    clearable
                    default-first-option
                    placeholder="选择或输入项目分类"
                    style="width: 100%"
                  >
                    <el-option v-for="c in categories" :key="c" :label="c" :value="c" />
                  </el-select>
                </el-form-item>
                <el-form-item label="所属行业">
                  <el-select
                    v-model="settingsForm.industry"
                    filterable
                    allow-create
                    clearable
                    default-first-option
                    placeholder="选择或输入行业"
                    style="width: 100%"
                  >
                    <el-option v-for="i in industries" :key="i" :label="i" :value="i" />
                  </el-select>
                </el-form-item>
                <el-form-item label="标签">
                  <el-select
                    v-model="settingsForm.tags"
                    multiple
                    filterable
                    allow-create
                    default-first-option
                    clearable
                    placeholder="输入后回车添加标签"
                    style="width: 100%"
                  />
                </el-form-item>
              </el-form>
            </el-tab-pane>

            <el-tab-pane label="对话配置" name="dialog">
              <el-form label-width="110px" class="settings-form">
                <el-form-item label="项目 Agent">
                  <el-select v-model="settingsForm.expert_id" clearable placeholder="使用全局默认" style="width: 100%">
                    <el-option v-for="e in experts" :key="e.id" :label="e.name" :value="e.id" />
                  </el-select>
                </el-form-item>
                <el-form-item label="项目技能">
                  <el-select
                    v-model="settingsForm.skill_ids"
                    multiple
                    collapse-tags
                    clearable
                    placeholder="选择后对话时自动带出对应工具"
                    style="width: 100%"
                  >
                    <el-option v-for="s in skills" :key="s.id" :label="s.name" :value="s.id" />
                  </el-select>
                </el-form-item>
                <el-form-item label="工具">
                  <el-select
                    v-model="settingsForm.tool_names"
                    multiple
                    collapse-tags
                    clearable
                    placeholder="留空则按 Agent 与技能自动推导"
                    style="width: 100%"
                  >
                    <el-option v-for="t in agentTools" :key="t.name" :label="t.label" :value="t.name" />
                  </el-select>
                </el-form-item>
                <el-form-item label="生效工具">
                  <div class="tool-chips">
                    <el-tag v-for="t in previewTools" :key="t" size="small" effect="plain">{{ toolLabel(t) }}</el-tag>
                    <span v-if="!previewTools.length" class="muted">全部只读工具</span>
                  </div>
                </el-form-item>
              </el-form>
              <p class="muted" style="margin: 0 0 0 110px">对话页选择该项目后会自动套用这里的 Agent、技能与工具。</p>
            </el-tab-pane>
          </el-tabs>

          <div class="settings-actions">
            <el-button @click="fillSettings">重置</el-button>
            <el-button type="primary" :loading="settingsSaving" @click="saveSettings">保存设置</el-button>
          </div>
        </div>
      </el-tab-pane>
    </el-tabs>


    <el-dialog v-model="sourceDialog" :title="sourceTitle" width="min(1200px, 94vw)" top="3vh" class="source-dialog">
      <div class="source-viewer">
        <el-tabs v-model="sourceTab" class="source-tabs">
          <el-tab-pane label="原文预览" name="preview">
            <DocumentPreview v-if="sourceDoc" :src="sourceFileURL" :filename="sourceDoc.filename" />
          </el-tab-pane>
          <el-tab-pane label="提取文本" name="text">
            <div v-loading="sourceLoading">
              <div v-if="sourceError" class="notice error">{{ sourceError }}</div>
              <template v-else>
                <div class="source-toolbar">
                  <span class="muted">索引用的纯文本 · {{ sourceContent.length.toLocaleString() }} 字</span>
                  <div class="source-actions">
                    <el-input v-model="sourceQuery" placeholder="在文档中查找" clearable size="small" class="source-search" @input="sourceMatchIndex = 0" />
                    <el-button v-if="sourceQuery" size="small" :disabled="!sourceMatchCount" @click="stepSourceMatch(-1)">上一个</el-button>
                    <el-button v-if="sourceQuery" size="small" :disabled="!sourceMatchCount" @click="stepSourceMatch(1)">下一个</el-button>
                    <span v-if="sourceQuery" class="muted source-match-count">{{ sourceMatchCount ? sourceMatchIndex + 1 : 0 }} / {{ sourceMatchCount }}</span>
                    <el-button size="small" @click="copySource">复制文本</el-button>
                  </div>
                </div>
                <div ref="sourceTextEl" class="source-content" v-html="sourceRendered" />
              </template>
            </div>
          </el-tab-pane>
        </el-tabs>
      </div>
      <template #footer>
        <div class="source-footer">
          <span class="muted">预览基于上传的原件渲染；不支持的格式可直接下载</span>
          <el-button size="small" @click="sourceDialog = false">关闭</el-button>
          <a class="el-button el-button--primary el-button--small" :href="sourceDownloadURL" target="_blank" rel="noopener">下载原件</a>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import axios from 'axios'
import { ElMessage } from 'element-plus'
import { forceCenter, forceCollide, forceLink, forceManyBody, forceSimulation, forceX, forceY } from 'd3-force-3d'
import DocumentPreview from '../components/DocumentPreview.vue'
import DocumentExplorer from '../components/DocumentExplorer.vue'
import { jobPhaseLabel, jobStageState } from '../jobPhase'
import { describeIndexError } from '../utils/indexError'

interface UnitProfile {
  unit_id: string
  name: string
  unit_type: string
  industry: string
  expert_id?: string
  skill_ids?: string[]
  tool_names?: string[]
  tags?: string[]
  tools?: string[]
  document_count?: number
  doc_types?: Record<string, number>
  last_indexed_at?: string | null
  audit_count: number
  open_hazards: number
  indexed_chunks: number
}
interface AgentOption {
  id: string
  name: string
  tools?: string[]
}
interface SkillOption {
  id: string
  name: string
  tools?: string[]
}
interface GraphNode {
  id: string
  label: string
  type: string
  weight?: number
  kind?: string
  detail?: string
}
interface GraphEdge {
  source: string
  target: string
  type: string
  label?: string
  weight?: number
  evidence?: string
}
interface IndexDoc {
  doc_id: string
  filename: string
  doc_type: string
  chunk_count: number
  job_id: string
  indexed_at?: string
}
interface JobItem {
  job_id: string
  filename: string
  doc_type: string
  status: string
  phase?: string
  progress: number
  message: string
  chunks?: number
  attempts?: number
}

const route = useRoute()
const router = useRouter()
const id = computed(() => String(route.params.id || ''))

// 内容框架：章节来自 /units/:id/outline，后端在索引完成时已自动抽取。
const outlineEntries = ref<Array<{ title: string; level: number; doc_id: string; doc_name?: string; chunk_index: number }>>([])
const outlineTotal = ref(0)
const extracting = ref(false)
// 每份文档单独成组：章节脱离文件之后，就没法判断「这段话出自哪一册」。
const expanded = ref<Record<string, boolean>>({})

const outlineGroups = computed(() => {
  const map = new Map<string, { docId: string; name: string; entries: typeof outlineEntries.value }>()
  for (const e of outlineEntries.value) {
    const key = e.doc_id || e.doc_name || 'unknown'
    let group = map.get(key)
    if (!group) {
      group = { docId: key, name: e.doc_name || '未命名文档', entries: [] }
      map.set(key, group)
    }
    group.entries.push(e)
  }
  return [...map.values()]
})

function toggleDoc(docId: string) {
  expanded.value = { ...expanded.value, [docId]: !expanded.value[docId] }
}

async function loadOutline() {
  try {
    const { data } = await axios.get(`/api/v1/units/${id.value}/outline`)
    outlineTotal.value = data.total || 0
    outlineEntries.value = (data.documents || []).flatMap((d: any) => d.entries || [])
  } catch {
    outlineEntries.value = []
    outlineTotal.value = 0
  }
}

async function extractOutline() {
  extracting.value = true
  try {
    await axios.post(`/api/v1/units/${id.value}/outline/extract`)
    await loadOutline()
  } finally {
    extracting.value = false
  }
}
const profile = ref<UnitProfile | null>(null)
const loading = ref(false)
const error = ref('')
// 从文档库跳进来时可以直接落到图谱页，并按文档过滤。
const graphDocID = ref(String(route.query.doc || ''))
const graphDocName = ref('')
const tab = ref(route.query.tab === 'graph' ? 'graph' : 'documents')
const settingsTab = ref('basic')
const settingsSaving = ref(false)
const graphNodes = ref<GraphNode[]>([])
const graphEdges = ref<GraphEdge[]>([])
const graphStats = ref<{ nodes: number; edges: number } | null>(null)
const graphLoading = ref(false)
const graphError = ref('')
const selectedNode = ref<GraphNode | null>(null)
const kgJob = ref<any>(null)
const kgLoading = ref(false)
let kgTimer: ReturnType<typeof setTimeout> | undefined

const kgStatusText = computed(() => {
  switch (kgJob.value?.status) {
    case 'running': return '构建中'
    case 'done': return '已完成'
    case 'failed': return '失败'
    default: return '未构建'
  }
})

function graphTypeLabel(type: string) {
  const labels: Record<string, string> = {
    unit: '项目', tag: '标签', category: '分类', industry: '行业',
    doc_type: '文档类型', document: '文档', entity: '实体',
  }
  return labels[type] || type
}
const experts = ref<AgentOption[]>([])
const skills = ref<SkillOption[]>([])
const categories = ref<string[]>([])
const industries = ref<string[]>([])
const settingsForm = ref({
  name: '',
  unit_type: '',
  industry: '',
  expert_id: '',
  skill_ids: [] as string[],
  tool_names: [] as string[],
  tags: [] as string[],
})

const toolLabels: Record<string, string> = {
  get_unit_profile: '项目画像',
  query_hazards: '查询记录清单',
  audit_history: '读取历史记录',
  vector_search: '档案检索',
}
const toolLabel = (name: string) => toolLabels[name] || name
const agentTools = Object.entries(toolLabels).map(([name, label]) => ({ name, label }))

// 与后端 effectiveTools 保持一致：显式工具优先，否则技能声明、Agent 作为上限。
const previewTools = computed(() => {
  if (settingsForm.value.tool_names.length) return [...settingsForm.value.tool_names]
  const wanted = settingsForm.value.skill_ids
  const skillTools = new Set<string>()
  for (const s of skills.value) {
    if (wanted.length && !wanted.includes(s.id)) continue
    for (const t of s.tools || []) skillTools.add(t)
  }
  const expertTools = new Set(
    experts.value.find((e) => e.id === settingsForm.value.expert_id)?.tools || [],
  )
  if (!skillTools.size) return [...expertTools]
  if (!expertTools.size) return [...skillTools]
  const both = [...skillTools].filter((t) => expertTools.has(t))
  return both.length ? both : [...skillTools]
})

const unitJobs = ref<JobItem[]>([])
const docsLoading = ref(false)


const sourceDialog = ref(false)
const sourceLoading = ref(false)
const sourceError = ref('')
const sourceContent = ref('')
const sourceDoc = ref<IndexDoc | null>(null)
const sourceTitle = computed(() => (sourceDoc.value ? `${sourceDoc.value.filename} · 源文档内容` : '源文档内容'))
const sourceQuery = ref('')
const sourceMatchIndex = ref(0)
const sourceTextEl = ref<HTMLElement | null>(null)
const sourceTab = ref<'preview' | 'text'>('preview')
const previewableExtensions = ['.pdf', '.docx', '.xlsx', '.xls', '.csv']
const sourcePreviewable = computed(() => previewableExtensions.some((ext) => sourceDoc.value?.filename.toLowerCase().endsWith(ext)))
const sourceFileURL = computed(() => sourceDoc.value ? `/api/v1/units/${encodeURIComponent(id.value)}/documents/${encodeURIComponent(sourceDoc.value.filename)}/file` : '#')
const sourceDownloadURL = computed(() => (sourceDoc.value ? `${sourceFileURL.value}?download=1` : '#'))
const sourceMatchCount = computed(() => {
  if (!sourceQuery.value.trim()) return 0
  return sourceContent.value.toLocaleLowerCase().split(sourceQuery.value.trim().toLocaleLowerCase()).length - 1
})
const sourceRendered = computed(() => {
  const text = sourceContent.value || '源文档没有可显示的文本内容。'
  const query = sourceQuery.value.trim()
  if (!query) return escapeSourceHTML(text)
  const escapedQuery = query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  let match = 0
  return escapeSourceHTML(text).replace(new RegExp(escapedQuery, 'gi'), (found) => {
    const active = match++ === sourceMatchIndex.value
    return `<mark class="source-match${active ? ' active' : ''}">${found}</mark>`
  })
})

function escapeSourceHTML(value: string) {
  return value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/\"/g, '&quot;').replace(/'/g, '&#39;')
}

function stepSourceMatch(direction: number) {
  if (!sourceMatchCount.value) return
  sourceMatchIndex.value = (sourceMatchIndex.value + direction + sourceMatchCount.value) % sourceMatchCount.value
  nextTick(() => sourceTextEl.value?.querySelectorAll('mark.source-match')[sourceMatchIndex.value]?.scrollIntoView({ block: 'center' }))
}

async function copySource() {
  try {
    await navigator.clipboard.writeText(sourceContent.value)
    ElMessage.success('文档文本已复制')
  } catch {
    ElMessage.error('复制失败，请检查浏览器剪贴板权限')
  }
}

// 组件 emit 的 DocItem 与 IndexDoc 字段兼容，但 doc_id 可能为空，这里放宽为 any。
async function openSource(row: any) {
  sourceDoc.value = row
  sourceContent.value = ''
  sourceQuery.value = ''
  sourceMatchIndex.value = 0
  sourceError.value = ''
  sourceDialog.value = true
  sourceTab.value = sourcePreviewable.value ? 'preview' : 'text'
  sourceLoading.value = true
  try {
    const url = `/api/v1/units/${encodeURIComponent(id.value)}/documents/${encodeURIComponent(row.filename)}/source`
    const { data } = await axios.get(url, { responseType: 'text' })
    sourceContent.value = typeof data === 'string' ? data : String(data ?? '')
  } catch (e: any) {
    let detail = e.response?.data
    if (typeof detail === 'string') {
      try { detail = JSON.parse(detail) } catch { /* response may be a plain-text proxy error */ }
    }
    const reason = detail?.error || e.message
    sourceError.value = reason
      ? `读取源文档失败（HTTP ${e.response?.status ?? '网络错误'}）：${reason}`
      : '读取源文档失败，请稍后重试。'
  } finally {
    sourceLoading.value = false
  }
}



const activeJobs = computed(() =>
  unitJobs.value.filter((j) => j.status === 'queued' || j.status === 'running'),
)
const failedJobs = computed(() => unitJobs.value.filter((j) => j.status === 'failed').slice(0, 3))

// 文档索引进度：进行中任务 + 已入库文档，由用户手动刷新（不再轮询）。
async function loadDocuments() {
  if (!id.value) return
  docsLoading.value = true
  const hadActive = activeJobs.value.length > 0
  try {
    // 文档列表由 DocumentExplorer 自己取（带搜索/分页/状态筛选），
    // 这里只负责「任务进度」这块，避免同一份数据在页面里取两遍。
    const jobs = await axios.get('/api/v1/jobs', { params: { unit_id: id.value, limit: 20 } })
    unitJobs.value = jobs.data?.items || []
    // 任务刚结束时刷新画像里的文本块计数。
    if (hadActive && activeJobs.value.length === 0) await refreshProfile()
  } finally {
    docsLoading.value = false
  }
}

// 重新解析失败任务：源文件已保留，修好解析环境（如 OCR 扫描件）后可直接重跑。
async function retryIndexJob(jobId: string) {
  if (!jobId) return
  try {
    await axios.post(`/api/v1/jobs/${encodeURIComponent(jobId)}/retry`)
    ElMessage.success('已重新排队，点「刷新」查看进度')
    await loadDocuments()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '重新解析失败')
  }
}


// 文档被移除或重新索引后，刷新画像统计与任务列表。
async function refreshAfterDocChange() {
  await Promise.all([refreshProfile(), loadDocuments()])
}

async function refreshProfile() {
  try {
    const { data } = await axios.get(`/api/v1/units/${encodeURIComponent(id.value)}/profile`)
    profile.value = data
  } catch {
    // 画像刷新失败不影响索引列表展示。
  }
}


async function load() {
  if (!id.value) return
  loading.value = true
  error.value = ''
  profile.value = null
  try {
    const { data } = await axios.get(`/api/v1/units/${encodeURIComponent(id.value)}/profile`)
    profile.value = data
  } catch (e: any) {
    error.value = e?.response?.status === 404
      ? `知识库 ${id.value} 不存在。`
      : e?.response?.data?.error || '读取知识库信息失败。'
  } finally {
    loading.value = false
  }
}

async function loadOptions() {
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
    // 选项加载失败不影响画像展示。
  }
}

// 从画像回填表单，供切换面板、保存后重置使用。
function fillSettings() {
  const p = profile.value
  settingsForm.value = {
    name: p?.name || '',
    unit_type: p?.unit_type || '',
    industry: p?.industry || '',
    expert_id: p?.expert_id || '',
    skill_ids: [...(p?.skill_ids || [])],
    tool_names: [...(p?.tool_names || [])],
    tags: [...(p?.tags || [])],
  }
}

function openSettings() {
  fillSettings()
  tab.value = 'settings'
}

async function saveSettings() {
  if (!id.value) return
  const name = settingsForm.value.name.trim()
  if (!name) {
    ElMessage.warning('请填写项目名称')
    return
  }
  settingsSaving.value = true
  try {
    await axios.patch(`/api/v1/units/${encodeURIComponent(id.value)}`, {
      name,
      unit_type: settingsForm.value.unit_type || '',
      industry: settingsForm.value.industry || '',
      expert_id: settingsForm.value.expert_id || '',
      skill_ids: settingsForm.value.skill_ids,
      tool_names: settingsForm.value.tool_names,
      tags: settingsForm.value.tags,
    })
    ElMessage.success('项目设置已保存')
    await refreshProfile()
    await loadOptions()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '保存失败，请稍后重试')
  } finally {
    settingsSaving.value = false
  }
}

const graphEl = ref<HTMLDivElement | null>(null)
const graphCanvas = ref<HTMLCanvasElement | null>(null)
const highConfidenceOnly = ref(false)
const hideLeaves = ref(true)
const graphKeyword = ref('')
const graphKindFilter = ref('')
const selectedLink = ref<GraphEdge | null>(null)

interface GraphPoint extends GraphNode {
  x: number
  y: number
  vx?: number
  vy?: number
  degree: number
}

let graphSimulation: any = null
let graphPoints: GraphPoint[] = []
let graphCanvasLinks: any[] = []
let graphCanvasWidth = 0
let graphCanvasHeight = 0
let graphView = { x: 0, y: 0, scale: 1 }
let graphHoveredID = ''
let graphDragNode: GraphPoint | null = null
let graphPointerDown: { x: number; y: number; moved: boolean } | null = null
let graphPanOrigin: { x: number; y: number; viewX: number; viewY: number } | null = null
let graphResizeObserver: ResizeObserver | null = null

const nodeID = (value: any) => (typeof value === 'string' ? value : value?.id || '')

const graphBaseEdges = computed(() => {
  let edges = graphEdges.value
  if (highConfidenceOnly.value) {
    edges = edges.filter((edge) => (edge.weight || 1) >= 2)
  }
  if (!hideLeaves.value) return edges
  // 去掉只连一条边的叶子关系，图会清爽很多。
  const degree = new Map<string, number>()
  edges.forEach((edge) => {
    degree.set(edge.source, (degree.get(edge.source) || 0) + 1)
    degree.set(edge.target, (degree.get(edge.target) || 0) + 1)
  })
  return edges.filter((edge) => (degree.get(edge.source) || 0) > 1 || (degree.get(edge.target) || 0) > 1)
})

const displayNodes = computed(() => {
  let nodes = graphNodes.value
  if (highConfidenceOnly.value || hideLeaves.value) {
    const ids = new Set<string>()
    graphBaseEdges.value.forEach((edge) => {
      ids.add(edge.source)
      ids.add(edge.target)
    })
    nodes = graphNodes.value.filter((node) => ids.has(node.id))
  }
  const keyword = graphKeyword.value.trim().toLowerCase()
  if (!keyword && !graphKindFilter.value) return nodes
  const nodeMap = new Map(graphNodes.value.map((node) => [node.id, node]))
  const seeds = nodes.filter((node) => {
    const matchesKind = !graphKindFilter.value || (node.kind || graphTypeLabel(node.type)) === graphKindFilter.value
    const matchesKeyword = !keyword ||
      node.label.toLowerCase().includes(keyword) ||
      (node.detail || '').toLowerCase().includes(keyword)
    return matchesKind && matchesKeyword
  })
  const ids = new Set<string>()
  seeds.forEach((node) => {
    ids.add(node.id)
    graphBaseEdges.value.forEach((edge) => {
      if (edge.source === node.id) ids.add(edge.target)
      if (edge.target === node.id) ids.add(edge.source)
    })
  })
  return Array.from(ids).map((id) => nodeMap.get(id)).filter((node): node is GraphNode => !!node)
})

const displayEdges = computed(() => {
  const ids = new Set(displayNodes.value.map((node) => node.id))
  return graphBaseEdges.value.filter((edge) => ids.has(edge.source) && ids.has(edge.target))
})

interface NeighborItem {
  id: string
  label: string
  color: string
  relation: string
  weight: number
  evidence?: string
}

const neighborRelations = computed<NeighborItem[]>(() => {
  const current = selectedNode.value
  if (!current) return []
  const items: NeighborItem[] = []
  displayEdges.value.forEach((edge) => {
    let otherId = ''
    if (edge.source === current.id) otherId = edge.target
    else if (edge.target === current.id) otherId = edge.source
    if (!otherId) return
    const other = displayNodes.value.find((node) => node.id === otherId)
    if (!other) return
    items.push({
      id: other.id,
      label: other.label,
      color: nodeColor(other),
      relation: edge.label || '相关',
      weight: edge.weight || 1,
      evidence: edge.evidence,
    })
  })
  return items.sort((a, b) => b.weight - a.weight)
})

// 节点大小按连接数（degree）计算，比出现次数更能反映关联密度。
const degreeMap = computed(() => {
  const map = new Map<string, number>()
  graphEdges.value.forEach((edge) => {
    map.set(edge.source, (map.get(edge.source) || 0) + 1)
    map.set(edge.target, (map.get(edge.target) || 0) + 1)
  })
  return map
})

const graphKindOptions = computed(() => {
  const counts = new Map<string, number>()
  graphNodes.value.forEach((node) => {
    const kind = node.kind || graphTypeLabel(node.type)
    counts.set(kind, (counts.get(kind) || 0) + 1)
  })
  return Array.from(counts, ([kind, count]) => ({ kind, count })).sort((a, b) => b.count - a.count)
})

const topGraphKind = computed(() => graphKindOptions.value[0]
  ? `${graphKindOptions.value[0].kind} ${graphKindOptions.value[0].count}`
  : '—')

const multiSourceRelations = computed(() => graphEdges.value.filter((edge) => (edge.weight || 1) >= 2).length)
const selectedLinkColor = computed(() => selectedLink.value ? '#8b5cf6' : 'var(--border-strong)')

const topGraphEntities = computed(() =>
  [...graphNodes.value]
    .sort((a, b) => (degreeMap.value.get(b.id) || 0) - (degreeMap.value.get(a.id) || 0))
    .slice(0, 6)
    .map((node) => ({ node, degree: degreeMap.value.get(node.id) || 0 }))
    .filter((item) => item.degree > 0),
)

const topGraphRelations = computed(() => {
  const labels = new Map(graphNodes.value.map((node) => [node.id, node.label]))
  return [...graphEdges.value]
    .sort((a, b) => (b.weight || 1) - (a.weight || 1))
    .slice(0, 6)
    .map((edge) => ({
      ...edge,
      sourceLabel: labels.get(edge.source) || edge.source.replace('entity:', ''),
      targetLabel: labels.get(edge.target) || edge.target.replace('entity:', ''),
    }))
})

const rankedGraphNodes = computed(() => [...displayNodes.value].sort(
    (a, b) => (degreeMap.value.get(b.id) || 0) - (degreeMap.value.get(a.id) || 0),
  ))
const coreNodeIDs = computed(() => new Set(rankedGraphNodes.value.slice(0, 30).map((node) => node.id)))
const labelNodeIDs = computed(() => new Set(rankedGraphNodes.value.slice(0, 18).map((node) => node.id)))

function isDarkTheme() {
  return document.documentElement.classList.contains('dark')
}

function selectNode(node?: GraphNode | null) {
  if (!node) return
  selectedNode.value = node
  selectedLink.value = null
  drawGraph2D()
}

function clearSelection() {
  selectedNode.value = null
  selectedLink.value = null
  drawGraph2D()
}

function focusGraphEntity(node: GraphNode) {
  graphKeyword.value = ''
  graphKindFilter.value = ''
  nextTick(() => selectNode(node))
}

function focusGraphRelation(edge: GraphEdge) {
  graphKeyword.value = ''
  graphKindFilter.value = ''
  selectedNode.value = null
  nextTick(() => {
    selectedLink.value = edge
    drawGraph2D()
  })
}

function graphRadius(node: GraphNode) {
  return Math.min(18, 4.5 + Math.sqrt(degreeMap.value.get(node.id) || 1) * 1.8)
}

function roundedRect(ctx: CanvasRenderingContext2D, x: number, y: number, width: number, height: number, radius: number) {
  const r = Math.min(radius, width / 2, height / 2)
  ctx.beginPath()
  ctx.moveTo(x + r, y)
  ctx.arcTo(x + width, y, x + width, y + height, r)
  ctx.arcTo(x + width, y + height, x, y + height, r)
  ctx.arcTo(x, y + height, x, y, r)
  ctx.arcTo(x, y, x + width, y, r)
  ctx.closePath()
}

function drawGraph2D() {
  const canvas = graphCanvas.value
  const ctx = canvas?.getContext('2d')
  if (!canvas || !ctx) return
  const dpr = window.devicePixelRatio || 1
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
  ctx.clearRect(0, 0, graphCanvasWidth, graphCanvasHeight)

  const selectedID = selectedNode.value?.id || ''
  const neighborIDs = new Set<string>()
  if (selectedID) {
    neighborIDs.add(selectedID)
    displayEdges.value.forEach((edge) => {
      if (edge.source === selectedID) neighborIDs.add(edge.target)
      if (edge.target === selectedID) neighborIDs.add(edge.source)
    })
  }
  const isActive = (node: GraphPoint) =>
    selectedID ? neighborIDs.has(node.id) : coreNodeIDs.value.has(node.id)

  ctx.save()
  ctx.translate(graphView.x, graphView.y)
  ctx.scale(graphView.scale, graphView.scale)

  graphCanvasLinks.forEach((link: any) => {
    const source = nodeID(link.source)
    const target = nodeID(link.target)
    const pointA = graphPoints.find((node) => node.id === source)
    const pointB = graphPoints.find((node) => node.id === target)
    if (!pointA || !pointB) return
    const focused = selectedID ? source === selectedID || target === selectedID : false
    const coreLink = coreNodeIDs.value.has(source) && coreNodeIDs.value.has(target)
    ctx.beginPath()
    ctx.moveTo(pointA.x, pointA.y)
    ctx.lineTo(pointB.x, pointB.y)
    ctx.strokeStyle = focused
      ? 'rgba(139,92,246,0.8)'
      : selectedID
        ? 'rgba(148,163,184,0.08)'
        : coreLink
          ? 'rgba(139,92,246,0.28)'
          : 'rgba(148,163,184,0.14)'
    ctx.lineWidth = (focused ? 1.8 : coreLink ? 1 : 0.7) / graphView.scale
    ctx.stroke()
  })

  graphPoints.forEach((node) => {
    const active = isActive(node)
    const radius = graphRadius(node)
    const selected = node.id === selectedID
    const hovered = node.id === graphHoveredID
    ctx.beginPath()
    ctx.arc(node.x, node.y, radius, 0, Math.PI * 2)
    ctx.fillStyle = active ? nodeColor(node) : isDarkTheme() ? 'rgba(148,163,184,0.22)' : 'rgba(148,163,184,0.3)'
    ctx.globalAlpha = active ? 1 : 0.55
    ctx.fill()
    if (selected || hovered) {
      ctx.globalAlpha = 1
      ctx.lineWidth = (selected ? 2.5 : 1.5) / graphView.scale
      ctx.strokeStyle = selected ? '#7c3aed' : 'rgba(255,255,255,0.95)'
      ctx.stroke()
    }
    ctx.globalAlpha = 1

    const shouldLabel = selected || neighborIDs.has(node.id) || (!selectedID && labelNodeIDs.value.has(node.id))
    if (!shouldLabel) return
    const text = node.label.length > 18 ? `${node.label.slice(0, 18)}…` : node.label
    ctx.font = '600 11px "PingFang SC", "Microsoft YaHei", sans-serif'
    const textWidth = ctx.measureText(text).width
    const labelHeight = 20
    const labelX = node.x + radius + 6
    const labelY = node.y - labelHeight / 2
    roundedRect(ctx, labelX, labelY, textWidth + 12, labelHeight, 5)
    ctx.fillStyle = isDarkTheme() ? 'rgba(30,41,59,0.92)' : 'rgba(255,255,255,0.95)'
    ctx.fill()
    ctx.strokeStyle = selected ? '#8b5cf6' : isDarkTheme() ? 'rgba(148,163,184,0.28)' : 'rgba(148,163,184,0.38)'
    ctx.lineWidth = 0.8 / graphView.scale
    ctx.stroke()
    ctx.fillStyle = isDarkTheme() ? '#e2e8f0' : '#334155'
    ctx.textBaseline = 'middle'
    ctx.fillText(text, labelX + 6, node.y)
  })
  ctx.restore()
}

function fitGraph2D(redraw = true) {
  if (!graphCanvasWidth || !graphCanvasHeight || !graphPoints.length) return
  const focusPoints = graphPoints.filter((node) => coreNodeIDs.value.has(node.id))
  const fitPoints = focusPoints.length ? focusPoints : graphPoints
  const xs = fitPoints.map((node) => node.x)
  const ys = fitPoints.map((node) => node.y)
  const minX = Math.min(...xs)
  const maxX = Math.max(...xs)
  const minY = Math.min(...ys)
  const maxY = Math.max(...ys)
  const spanX = Math.max(maxX - minX, 160)
  const spanY = Math.max(maxY - minY, 120)
  const scale = Math.min((graphCanvasWidth - 80) / spanX, (graphCanvasHeight - 70) / spanY, 1.65)
  graphView = {
    scale,
    x: graphCanvasWidth / 2 - ((minX + maxX) / 2) * scale,
    y: graphCanvasHeight / 2 - ((minY + maxY) / 2) * scale,
  }
  if (redraw) drawGraph2D()
}

function resizeGraph2D() {
  const host = graphEl.value
  const canvas = graphCanvas.value
  if (!host || !canvas) return
  graphCanvasWidth = host.clientWidth
  graphCanvasHeight = host.clientHeight
  const dpr = window.devicePixelRatio || 1
  canvas.width = Math.max(1, Math.floor(graphCanvasWidth * dpr))
  canvas.height = Math.max(1, Math.floor(graphCanvasHeight * dpr))
  canvas.style.width = `${graphCanvasWidth}px`
  canvas.style.height = `${graphCanvasHeight}px`
  drawGraph2D()
}

function updateGraph2D() {
  graphSimulation?.stop()
  const width = graphCanvasWidth || graphEl.value?.clientWidth || 760
  const height = graphCanvasHeight || graphEl.value?.clientHeight || 600
  const previous = new Map(graphPoints.map((node) => [node.id, node]))
  graphPoints = displayNodes.value.map((node, index) => {
    const old = previous.get(node.id)
    const angle = (index / Math.max(1, displayNodes.value.length)) * Math.PI * 2
    const ring = 80 + Math.sqrt(index + 1) * 14
    return {
      ...node,
      x: old?.x || width / 2 + Math.cos(angle) * ring,
      y: old?.y || height / 2 + Math.sin(angle) * ring,
      degree: degreeMap.value.get(node.id) || 0,
    }
  })
  graphCanvasLinks = displayEdges.value.map((edge) => ({ ...edge }))
  if (!graphPoints.length) {
    drawGraph2D()
    return
  }
  graphSimulation = forceSimulation(graphPoints, 2)
    .force('link', forceLink(graphCanvasLinks).id((node: any) => node.id).distance((link: any) => 38 + Math.max(0, 4 - (link.weight || 1)) * 12).strength(0.55))
    .force('charge', forceManyBody().strength((node: any) => -70 - (node.degree || 0) * 10).distanceMax(320))
    .force('center', forceCenter(width / 2, height / 2))
    .force('collide', forceCollide((node: any) => graphRadius(node) + 8).strength(0.85))
    .force('x', forceX(width / 2).strength(0.018))
    .force('y', forceY(height / 2).strength(0.018))
    .alphaDecay(0.035)
    .velocityDecay(0.38)
    .on('tick', drawGraph2D)
    .on('end', () => {
      fitGraph2D()
    })
}

function ensureGraph2D() {
  const host = graphEl.value
  const canvas = graphCanvas.value
  if (!host || !canvas) return
  if (!canvas.dataset.graphBound) {
    canvas.dataset.graphBound = '1'
    canvas.addEventListener('pointerdown', onGraphPointerDown)
    canvas.addEventListener('pointermove', onGraphPointerMove)
    canvas.addEventListener('pointerup', onGraphPointerUp)
    canvas.addEventListener('pointercancel', onGraphPointerUp)
    canvas.addEventListener('pointerleave', onGraphPointerLeave)
    canvas.addEventListener('wheel', onGraphWheel, { passive: false })
    canvas.style.cursor = 'grab'
  }
  if (!graphResizeObserver && typeof ResizeObserver !== 'undefined') {
    graphResizeObserver = new ResizeObserver(() => resizeGraph2D())
    graphResizeObserver.observe(host)
  }
  resizeGraph2D()
  updateGraph2D()
}

function graphWorldPoint(event: PointerEvent | WheelEvent) {
  const rect = graphCanvas.value?.getBoundingClientRect()
  if (!rect) return { x: 0, y: 0 }
  return {
    x: (event.clientX - rect.left - graphView.x) / graphView.scale,
    y: (event.clientY - rect.top - graphView.y) / graphView.scale,
  }
}

function nodeAt(event: PointerEvent) {
  const point = graphWorldPoint(event)
  return [...graphPoints].reverse().find((node) => {
    const dx = node.x - point.x
    const dy = node.y - point.y
    return Math.hypot(dx, dy) <= graphRadius(node) + 5 / graphView.scale
  }) || null
}

function onGraphPointerDown(event: PointerEvent) {
  const node = nodeAt(event)
  graphPointerDown = { x: event.clientX, y: event.clientY, moved: false }
  if (node) {
    graphDragNode = node
    node.vx = 0
    node.vy = 0
    graphSimulation?.alphaTarget(0.12).restart()
  } else {
    graphPanOrigin = {
      x: event.clientX,
      y: event.clientY,
      viewX: graphView.x,
      viewY: graphView.y,
    }
  }
  if (graphCanvas.value) graphCanvas.value.style.cursor = 'grabbing'
  graphCanvas.value?.setPointerCapture(event.pointerId)
}

function onGraphPointerMove(event: PointerEvent) {
  if (graphPointerDown && Math.hypot(event.clientX - graphPointerDown.x, event.clientY - graphPointerDown.y) > 3) {
    graphPointerDown.moved = true
  }
  const point = graphWorldPoint(event)
  if (graphDragNode) {
    graphDragNode.x = point.x
    graphDragNode.y = point.y
    drawGraph2D()
    return
  }
  if (graphPanOrigin && graphPointerDown) {
    graphView.x = graphPanOrigin.viewX + event.clientX - graphPanOrigin.x
    graphView.y = graphPanOrigin.viewY + event.clientY - graphPanOrigin.y
    drawGraph2D()
    return
  }
  const node = nodeAt(event)
  const nextHover = node?.id || ''
  if (nextHover !== graphHoveredID) {
    graphHoveredID = nextHover
    if (graphCanvas.value) graphCanvas.value.style.cursor = node ? 'pointer' : 'grab'
    drawGraph2D()
  }
}

function onGraphPointerUp(event: PointerEvent) {
  const pointer = graphPointerDown
  graphPointerDown = null
  if (graphDragNode) {
    graphDragNode = null
    graphSimulation?.alphaTarget(0)
  }
  graphPanOrigin = null
  if (graphCanvas.value) graphCanvas.value.style.cursor = 'grab'
  if (!pointer || pointer.moved) return
  const node = nodeAt(event)
  if (node) {
    selectNode(node)
  } else {
    clearSelection()
  }
}

function onGraphPointerLeave() {
  if (graphDragNode || graphPanOrigin) return
  graphHoveredID = ''
  drawGraph2D()
}

function onGraphWheel(event: WheelEvent) {
  event.preventDefault()
  const rect = graphCanvas.value?.getBoundingClientRect()
  if (!rect) return
  const world = graphWorldPoint(event)
  const nextScale = Math.max(0.35, Math.min(4, graphView.scale * (event.deltaY < 0 ? 1.12 : 0.89)))
  graphView.x = event.clientX - rect.left - world.x * nextScale
  graphView.y = event.clientY - rect.top - world.y * nextScale
  graphView.scale = nextScale
  drawGraph2D()
}

function destroyGraph2D() {
  graphSimulation?.stop()
  graphSimulation = null
  graphResizeObserver?.disconnect()
  graphResizeObserver = null
}

function nodeColor(node: GraphNode) {
  // 参考 LightRAG WebUI 的实体配色，便于在不同项目间保持一致认知。
  if (node.type === 'entity') {
    switch (node.kind) {
      case '组织': return '#00cc00'
      case '人员': return '#4169e1'
      case '地点': return '#cf6d17'
      case '事件': return '#00bfa0'
      case '制度': return '#e3493b'
      case '设备': return '#4421af'
      case '风险': return '#b71c1c'
      case '指标': return '#0000ff'
      default: return '#f4d371'
    }
  }
  switch (node.type) {
    case 'unit': return '#e3493b'
    case 'tag': return '#4169e1'
    case 'category': return '#f4d371'
    case 'industry': return '#cf6d17'
    case 'doc_type': return '#4421af'
    default: return '#b0b0b0'
  }
}

const legendItems = [
  { label: '组织', color: '#00cc00' },
  { label: '人员', color: '#4169e1' },
  { label: '地点', color: '#cf6d17' },
  { label: '事件', color: '#00bfa0' },
  { label: '制度', color: '#e3493b' },
  { label: '设备', color: '#4421af' },
  { label: '风险', color: '#b71c1c' },
  { label: '指标', color: '#0000ff' },
  { label: '其他', color: '#f4d371' },
]

async function loadGraph() {
  if (!id.value) return
  graphLoading.value = true
  graphError.value = ''
  try {
    const { data } = await axios.get(`/api/v1/units/${encodeURIComponent(id.value)}/graph`, {
      params: { source: 'entity', ...(graphDocID.value ? { doc_id: graphDocID.value } : {}) },
    })
    graphNodes.value = data.nodes || []
    graphEdges.value = data.edges || []
    graphStats.value = data.stats || null
    graphDocName.value = data.doc_name || ''
    selectedNode.value = null
    if (tab.value === 'graph') {
      await nextTick()
      ensureGraph2D()
    }
  } catch (e: any) {
    graphNodes.value = []
    graphEdges.value = []
    graphError.value = e.response?.data?.error || '读取知识图谱失败。'
  } finally {
    graphLoading.value = false
  }
}

async function loadKgStatus() {
  try {
    const { data } = await axios.get(`/api/v1/units/${encodeURIComponent(id.value)}/kg/status`)
    kgJob.value = data
    if (data.status === 'running') {
      if (kgTimer) clearTimeout(kgTimer)
      kgTimer = setTimeout(async () => {
        await loadKgStatus()
        if (kgJob.value?.status !== 'running') await loadGraph()
      }, 2500)
    }
  } catch {
    // 状态读取失败不阻塞图谱展示。
  }
}

async function runKGExtract() {
  if (!id.value) return
  kgLoading.value = true
  try {
    await axios.post(`/api/v1/units/${encodeURIComponent(id.value)}/kg/extract`)
    ElMessage.success('已开始构建实体图谱，可在下方查看进度')
    await loadKgStatus()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '启动失败，请检查模型配置')
  } finally {
    kgLoading.value = false
  }
}

function openAgent() {
  router.push({ path: '/agent', query: { unit: id.value } })
}

function openUpload() {
  router.push({ path: '/upload', query: { unit: id.value } })
}

watch(tab, (value) => {
  // 直接点击「项目设置」标签时也要带上当前画像。
  if (value === 'settings') fillSettings()
  if (value === 'graph') {
    if (!graphNodes.value.length) loadGraph()
    loadKgStatus()
    nextTick(() => ensureGraph2D())
  }
})

function clearDocFilter() {
  graphDocID.value = ''
  graphDocName.value = ''
  graphNodes.value = []
  router.replace({ path: `/units/${id.value}`, query: { tab: 'graph' } })
  loadGraph()
}

// 同一页面内从文档库切换到另一份文档时重新拉图谱。
watch(() => route.query.doc, (value) => {
  const next = String(value || '')
  if (next === graphDocID.value) return
  graphDocID.value = next
  graphDocName.value = ''
  graphNodes.value = []
  if (next) {
    tab.value = 'graph'
    loadGraph()
    nextTick(() => ensureGraph2D())
  }
})

watch([highConfidenceOnly, hideLeaves, graphKeyword, graphKindFilter], () => {
  selectedLink.value = null
  selectedNode.value = null
  updateGraph2D()
})

watch(id, async () => {
  unitJobs.value = []
  // 从文档库跳到另一个知识库时，tab 与文档过滤都跟着 query 走。
  if (route.query.tab === 'graph') tab.value = 'graph'
  graphDocID.value = String(route.query.doc || '')
  graphDocName.value = ''
  graphNodes.value = []
  await Promise.all([load(), loadDocuments()])
  if (tab.value === 'graph') {
    await loadGraph()
    nextTick(() => ensureGraph2D())
  }
}, { immediate: true })

onMounted(() => {
  loadOutline()
  loadOptions()
  if (tab.value === 'graph') {
    loadGraph()
    loadKgStatus()
    nextTick(() => ensureGraph2D())
  }
})
onUnmounted(() => {
  if (kgTimer) clearTimeout(kgTimer)
  destroyGraph2D()
  window.removeEventListener('resize', resizeGraph2D)
})

onMounted(() => {
  window.addEventListener('resize', resizeGraph2D)
})
</script>

<style scoped>
.job-card {
  padding: 12px 14px;
  margin-bottom: 10px;
  border-radius: 10px;
  background: rgba(128, 128, 128, 0.06);
}
.job-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin-bottom: 8px;
  flex-wrap: wrap;
}
.job-head b {
  font-weight: 600;
}
.failed-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 4px 0;
  font-size: calc(13px * var(--fs-scale));
}

/* 概览只保留文档与文本块两项 */
.unit-detail-page .stats {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}
</style>
<style scoped>
.tool-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.settings-panel {
  padding: 22px;
}
.settings-form {
  max-width: 620px;
}
.tag-row {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}
.graph-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 12px;
}
.graph-heading {
  min-width: 0;
}
.graph-heading > b {
  display: block;
  color: var(--text);
  font-size: calc(15px * var(--fs-scale));
  font-weight: 650;
}
.graph-heading > p {
  margin: 4px 0 0;
  color: var(--text-muted);
  font-size: calc(11px * var(--fs-scale));
  line-height: 1.6;
}
.graph-header-actions {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}
.graph-size-pill {
  padding: 4px 8px;
  border-radius: 999px;
  background: var(--bg-subtle);
  color: var(--text-muted);
  font-size: calc(10px * var(--fs-scale));
  white-space: nowrap;
}
.graph-controls {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 9px;
  padding: 11px;
  border: 1px solid var(--border);
  border-radius: 11px;
  background: var(--bg-subtle);
}
.graph-search {
  width: 230px;
}
.graph-search :deep(.el-input__prefix svg) {
  width: 14px;
  height: 14px;
  color: var(--text-muted);
}
.graph-kind-filter {
  width: 165px;
}
.graph-view-toggles {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  margin-left: auto;
}
.graph-summary {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px;
  margin-top: 12px;
}
.graph-summary > div {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 10px 11px;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: var(--surface);
}
.graph-summary span {
  color: var(--text-muted);
  font-size: calc(9px * var(--fs-scale));
}
.graph-summary b {
  overflow: hidden;
  color: var(--text);
  font-size: calc(14px * var(--fs-scale));
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.graph-insights {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
  margin-top: 10px;
}
.graph-insights section {
  min-width: 0;
  padding: 11px 12px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface);
}
.graph-insight-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 6px;
}
.graph-insight-head b {
  color: var(--text-secondary);
  font-size: calc(11px * var(--fs-scale));
  font-weight: 650;
}
.graph-insight-head span {
  color: var(--text-muted);
  font-size: calc(9px * var(--fs-scale));
}
.graph-insights section > button {
  display: flex;
  width: 100%;
  min-width: 0;
  align-items: center;
  gap: 7px;
  padding: 5px 6px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--text-secondary);
  font: inherit;
  font-size: calc(10px * var(--fs-scale));
  text-align: left;
  cursor: pointer;
}
.graph-insights section > button:hover {
  background: var(--bg-subtle);
  color: var(--text);
}
.graph-insights button > i {
  width: 7px;
  height: 7px;
  flex: none;
  border-radius: 50%;
}
.graph-insights button > span {
  overflow: hidden;
  min-width: 0;
  flex: 1;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.graph-insights button > em {
  flex: none;
  color: var(--text-muted);
  font-size: calc(9px * var(--fs-scale));
  font-style: normal;
}
.graph-stage {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(210px, 30%);
  gap: 12px;
  align-items: stretch;
}
.graph-canvas-2d {
  position: relative;
  width: 100%;
  height: 600px;
  border: 1px solid var(--border);
  border-radius: 12px;
  overflow: hidden;
  background:
    radial-gradient(circle at 50% 45%, color-mix(in srgb, var(--accent) 3%, transparent), transparent 42%),
    var(--surface);
}
.graph-canvas-surface {
  display: block;
  width: 100%;
  height: 100%;
  touch-action: none;
}
.graph-fit-btn {
  position: absolute;
  right: 10px;
  bottom: 10px;
  z-index: 3;
  padding: 5px 9px;
  border: 1px solid var(--border);
  border-radius: 7px;
  background: color-mix(in srgb, var(--surface) 92%, transparent);
  color: var(--text-secondary);
  font-size: calc(10px * var(--fs-scale));
  cursor: pointer;
}
.graph-fit-btn:hover {
  border-color: var(--border-strong);
  color: var(--text);
}
.graph-side-panel {
  min-width: 0;
  max-height: 600px;
  overflow: auto;
  padding: 15px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface);
}
.graph-side-panel h3 {
  margin: 6px 0 0;
  color: var(--text);
  font-size: calc(15px * var(--fs-scale));
  font-weight: 650;
  line-height: 1.45;
  overflow-wrap: anywhere;
}
.graph-side-eyebrow {
  color: var(--accent);
  font-size: calc(9px * var(--fs-scale));
  font-weight: 750;
  letter-spacing: 0.1em;
}
.graph-side-title {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}
.graph-side-title > span {
  width: 9px;
  height: 9px;
  flex: none;
  margin-top: 10px;
  border-radius: 50%;
  box-shadow: 0 0 0 4px var(--bg-subtle);
}
.graph-properties {
  display: grid;
  gap: 1px;
  margin: 13px 0 0;
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: var(--border);
}
.graph-properties > div {
  display: grid;
  grid-template-columns: 58px minmax(0, 1fr);
  gap: 8px;
  padding: 8px 9px;
  background: var(--bg-subtle);
}
.graph-properties dt {
  color: var(--text-muted);
  font-size: calc(10px * var(--fs-scale));
}
.graph-properties dd {
  overflow: hidden;
  margin: 0;
  color: var(--text-secondary);
  font-size: calc(10px * var(--fs-scale));
  text-overflow: ellipsis;
  white-space: nowrap;
}
.graph-description {
  margin: 12px 0 0;
  color: var(--text-secondary);
  font-size: calc(11px * var(--fs-scale));
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.graph-side-subtitle {
  margin-top: 14px;
  color: var(--text-secondary);
  font-size: calc(10px * var(--fs-scale));
  font-weight: 650;
}
.graph-guide-list {
  display: grid;
  gap: 10px;
  margin-top: 16px;
  color: var(--text-muted);
  font-size: calc(10px * var(--fs-scale));
}
.graph-guide-list span {
  display: flex;
  align-items: center;
  gap: 8px;
}
.graph-guide-list i {
  display: inline-block;
  flex: none;
}
.dot-node {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: var(--accent);
}
.dot-line {
  width: 14px;
  height: 1px;
  background: var(--border-strong);
}
.dot-focus {
  width: 10px;
  height: 10px;
  border: 2px solid #8b5cf6;
  border-radius: 50%;
}
.graph-side-close {
  margin-top: 12px;
}
.graph-canvas-3d {
  position: relative;
  width: 100%;
  height: 620px;
  border: 1px solid var(--border);
  border-radius: 10px;
  overflow: hidden;
  background: var(--bg-subtle);
}
.graph-canvas-hint {
  position: absolute;
  top: 9px;
  left: 10px;
  z-index: 2;
  padding: 4px 7px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: color-mix(in srgb, var(--surface) 88%, transparent);
  color: var(--text-muted);
  font-size: calc(9px * var(--fs-scale));
  pointer-events: none;
}
.graph-empty {
  position: absolute;
  inset: 0;
  z-index: 1;
  display: grid;
  place-items: center;
  font-size: calc(13px * var(--fs-scale));
  pointer-events: none;
}
.graph-legend {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 14px;
  margin-bottom: 8px;
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-secondary);
}
.graph-legend-item {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}
.graph-legend-item i {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: none;
}
.graph-fallback {
  margin-top: 12px;
}
.graph-fallback-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 240px;
  overflow: auto;
}
.graph-fallback-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border-radius: 6px;
  font-size: calc(12.5px * var(--fs-scale));
  cursor: pointer;
}
.graph-fallback-item:hover {
  background: var(--bg-subtle);
}
.graph-fallback-item .dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: none;
}
.graph-switch {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}
.graph-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: none;
}
.kg-status {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 14px;
  margin-bottom: 6px;
  font-size: calc(12px * var(--fs-scale));
}
.graph-detail {
  position: absolute;
  top: 12px;
  right: 12px;
  z-index: 50;
  width: min(340px, 46%);
  max-height: calc(100% - 24px);
  overflow: auto;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--bg);
  box-shadow: 0 8px 24px rgba(15, 23, 42, 0.12);
  font-size: calc(12.5px * var(--fs-scale));
}
@media (max-width: 900px) {
  .graph-header {
    flex-direction: column;
  }
  .graph-header-actions {
    width: 100%;
    justify-content: flex-start;
  }
  .graph-search,
  .graph-kind-filter {
    width: 100%;
  }
  .graph-view-toggles {
    width: 100%;
    margin-left: 0;
  }
  .graph-summary {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .graph-insights {
    grid-template-columns: 1fr;
  }
  .graph-stage {
    grid-template-columns: 1fr;
  }
  .graph-side-panel {
    max-height: 320px;
  }
  .graph-detail {
    width: calc(100% - 24px);
    max-height: 46%;
  }
}
.graph-neighbors {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin-top: 8px;
}
.graph-neighbor,
.graph-side-panel .graph-neighbors button {
  display: flex;
  width: 100%;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--text-secondary);
  font: inherit;
  font-size: calc(10px * var(--fs-scale));
  text-align: left;
  cursor: pointer;
}
.graph-neighbor:hover,
.graph-side-panel .graph-neighbors button:hover {
  background: var(--bg-subtle);
}
.graph-neighbor .dot,
.graph-side-panel .graph-neighbors .dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: none;
}
.graph-rel {
  color: var(--accent);
  font-size: calc(12px * var(--fs-scale));
}
.graph-detail-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.settings-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 10px;
  padding-top: 14px;
  border-top: 1px solid var(--border);
}
.source-viewer {
  min-height: 180px;
}
.source-hint {
  margin: 0 0 10px;
}
</style>
