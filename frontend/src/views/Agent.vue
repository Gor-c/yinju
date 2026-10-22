<template>
  <div class="chat-page">
    <div class="chat-main">
    <header class="chat-header">
      <div class="chat-header-heading">
        <h1>{{ activeConversationTitle }}</h1>
        <el-select
          v-model="expert"
          class="chat-model-select"
          size="small"
          aria-label="选择对话助手"
          :disabled="busy"
        >
          <el-option v-for="e in experts" :key="e.id" :label="e.name" :value="e.id" />
        </el-select>
      </div>
    </header>
    <div v-if="configError" class="notice error">{{ configError }}</div>

    <div class="chat-scroll" ref="scroll">
      <div v-if="!messages.length" class="chat-empty">
        <div class="empty-logo">知</div>
        <h2>{{ emptyStateTitle }}</h2>
        <p>{{ emptyStateDescription }}</p>
        <div class="suggestions">
          <button v-for="item in suggestions" :key="item.prompt" class="suggestion" type="button" :disabled="busy" @click="useSuggestedPrompt(item.prompt)">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
              <path :d="item.icon" />
            </svg>
            <b>{{ item.title }}</b>
            <span>{{ item.desc }}</span>
          </button>
        </div>
      </div>

      <div v-else class="message-list">
        <div v-for="(m, i) in messages" :key="i" :class="['message', m.role]">
          <template v-if="m.role === 'assistant'">
            <div class="avatar">知</div>
            <div class="message-body">
              <div v-if="m.preface" class="message-content md-body message-preface" v-html="renderMarkdownText(m.preface)"></div>
              <div v-if="m.notice" class="message-notice">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
                  <path d="M12 8v5m0 3h.01" />
                  <circle cx="12" cy="12" r="9" />
                </svg>
                <span>{{ m.notice }}</span>
              </div>
              <div v-if="isActive(i) && busy && !m.content && !m.steps?.length" class="typing"><span></span><span></span><span></span></div>
              <div v-if="isActive(i) && busy && status && !m.steps?.length" class="agent-status" role="status" aria-live="polite">{{ status }}</div>

              <div v-if="m.steps && m.steps.length" class="agent-steps">
                <button class="steps-head" type="button" :aria-expanded="!!m.stepsOpen" @click="m.stepsOpen = !m.stepsOpen">
                  <span class="steps-dot" :class="{ running: m.steps.some((s) => s.status === 'running') }"></span>
                  {{ m.steps.some((s) => s.status === 'running') ? '正在处理' : '查看过程' }} · {{ m.steps.length }} 步
                  <span class="muted">{{ m.stepsOpen ? '收起' : '展开' }}</span>
                </button>
                <div v-if="m.stepsOpen" class="steps-body">
                  <div v-for="(s, si) in m.steps" :key="si" class="step-row" :class="s.status">
                    <span class="step-index">{{ si + 1 }}</span>
                    <div class="step-main">
                      <div class="step-title">
                        <b>{{ s.label }}</b>
                        <span v-if="s.latency" class="muted">{{ s.latency }}ms</span>
                        <span v-if="s.status === 'error'" class="step-error">{{ s.error || '调用失败' }}</span>
                      </div>
                      <div v-if="s.arguments" class="step-detail">
                        <div class="step-detail-label">参数</div>
                        <pre class="step-args">{{ preview(formatStepData(s.arguments), 360) }}</pre>
                      </div>
                      <div v-if="s.result" class="step-detail">
                        <div class="step-detail-label">结果</div>
                        <pre class="step-result">{{ preview(formatStepData(s.result), 600) }}</pre>
                      </div>
                    </div>
                  </div>
                </div>
              </div>

              <div v-if="m.content" class="message-content md-body" v-html="renderMarkdown(m, isActive(i) && busy && !status)"></div>

              <div v-if="m.citations && m.citations.length" class="sources">
                <div class="sources-title">引用来源 · {{ m.citations.length }}</div>
                <details v-for="(c, ci) in m.citations" :key="ci" class="source">
                  <summary>
                    <span class="source-index">{{ ci + 1 }}</span>
                    <template v-if="c.source_type === 'web'">
                      <a class="source-link" :href="c.url" target="_blank" rel="noopener" @click.stop>{{ c.title || c.url }}</a>
                      <span class="source-meta">{{ c.site || hostOf(c.url) }} · 网页来源</span>
                      <button
                        class="text-btn"
                        type="button"
                        :disabled="!unitIds.length || importing === c.url"
                        @click.stop="importWeb(c.url || '')"
                      >
                        {{ importing === c.url ? '保存中…' : '保存到知识库' }}
                      </button>
                    </template>
                    <template v-else>
                      {{ c.filename }}
                      <span class="source-meta">
                        {{ c.unit_name ? `${c.unit_name} · ` : '' }}{{ c.doc_type || '文档' }}<template v-if="c.distance !== undefined"> · {{ c.distance.toFixed(3) }}</template>
                      </span>
                    </template>
                  </summary>
                  <p>{{ preview(c.content) }}</p>
                </details>
              </div>

              <div v-if="m.content && !busy" class="message-actions">
                <button class="text-btn" type="button" @click="copy(m.content)">复制</button>
                <button v-if="i === messages.length - 1" class="text-btn" type="button" @click="regenerate">
                  重新生成
                </button>
                <button v-if="m.runId" class="text-btn" type="button" @click="openRun(m.runId, m.content)">运行详情</button>
              </div>
              <div v-if="i === messages.length - 1 && m.content && !busy" class="follow-up-prompts">
                <span>继续追问</span>
                <button v-for="item in followUpPrompts" :key="item.prompt" type="button" @click="useSuggestedPrompt(item.prompt)">
                  {{ item.label }}
                </button>
              </div>
            </div>
          </template>
          <div v-else class="user-bubble">{{ m.content }}</div>
        </div>
      </div>
    </div>

    <div class="composer-wrap">
      <button
        v-if="showScrollToBottom"
        class="scroll-to-bottom"
        type="button"
        aria-label="回到底部"
        title="回到底部"
        @click="jumpToBottom"
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M12 5v14m-6-6 6 6 6-6" />
        </svg>
      </button>
      <div class="chat-context">
        <span class="context-label">知识库</span>
        <div class="context-chips">
          <span v-if="!profiles.length" class="context-empty">未选择，通用对话</span>
          <span v-for="p in profiles" :key="p.unit_id" class="context-chip">
            <i class="dot"></i>
            {{ p.name }}
            <button type="button" :title="`移除 ${p.name}`" :disabled="busy" @click="removeUnit(p.unit_id)">×</button>
          </span>
        </div>
        <button class="add-kb-btn" type="button" :disabled="busy" @click="openKbPicker">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
            <path d="M12 5v14M5 12h14" />
          </svg>
          添加知识库
        </button>
      </div>

      <div class="composer">
        <el-input
          ref="composerInput"
          v-model="draft"
          class="composer-input"
          type="textarea"
          :autosize="{ minRows: 1, maxRows: 6 }"
          resize="none"
          placeholder="输入问题，或直接开始通用对话…"
          @keydown.enter="onEnter"
        />
        <div class="composer-toolbar">
          <el-select
            v-model="selectedSkills"
            class="ghost-select"
            size="small"
            multiple
            collapse-tags
            placeholder="技能"
            :disabled="busy"
          >
            <el-option v-for="s in skills" :key="s.id" :label="s.name" :value="s.id" />
          </el-select>
          <el-tooltip
            content="开启后本次问题会发送给外部搜索服务；仅在知识库没有答案或需要最新信息时使用"
            placement="top"
          >
            <el-checkbox v-model="webSearch" size="small" :disabled="busy">联网搜索</el-checkbox>
          </el-tooltip>
          <span v-if="activeTools.length" class="connector-hint" :title="activeToolNames">工具 · {{ activeTools.length }}</span>
          <span class="spacer"></span>
          <span class="hint">{{ busy ? (status || '正在分析…') : 'Enter 发送 · Shift + Enter 换行' }}</span>
          <button v-if="!busy" class="send-btn" type="button" :disabled="!draft.trim()" aria-label="发送" @click="send">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 19V5m0 0-6 6m6-6 6 6" />
            </svg>
          </button>
          <button v-else class="send-btn stop" type="button" aria-label="停止生成" title="停止生成" @click="stop">
            <span class="stop-square"></span>
          </button>
        </div>
      </div>
    </div>

    </div>
    <div class="chat-page-actions">
      <el-tooltip :content="busy ? '停止当前回答并新建对话' : '新建对话'" placement="bottom">
        <button class="new-chat-btn" type="button" aria-label="新建对话" @click="newChat">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
            <path d="M12 5v14M5 12h14" />
          </svg>
        </button>
      </el-tooltip>
      <el-tooltip :content="sideCollapsed ? '展开知识库和历史对话' : '收起知识库和历史对话'" placement="left">
        <button
          class="side-tool side-panel-toggle"
          type="button"
          :aria-label="sideCollapsed ? '展开知识库和历史对话' : '收起知识库和历史对话'"
          :aria-expanded="!sideCollapsed"
          @click="toggleSide"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <rect x="3" y="4" width="18" height="16" rx="3" />
            <path d="M15 4v16" />
          </svg>
        </button>
      </el-tooltip>
    </div>
    <transition name="side-panel">
    <aside v-if="!sideCollapsed" class="chat-side">
      <el-tabs v-model="sideTab" class="side-tabs">
        <el-tab-pane label="知识库" name="kb">
          <div class="side-section">
            <div class="side-label">已加入的知识库</div>
            <el-select
              v-model="unitIds"
              multiple
              collapse-tags
              clearable
              filterable
              placeholder="选择知识库"
              :disabled="busy"
              style="width: 100%"
            >
              <el-option v-for="u in units" :key="u.unit_id" :label="u.name" :value="u.unit_id" />
            </el-select>
            <p v-if="unitIds.length > 1" class="side-note">Agent、技能和工具按第一个知识库的配置生效。</p>
          </div>

          <div v-if="profiles.length" class="side-cards">
            <div v-for="p in profiles" :key="p.unit_id" class="kb-card">
              <div class="kb-card-head">
                <b>{{ p.name }}</b>
                <span class="kb-type">{{ p.unit_type || '未分类' }}</span>
              </div>
              <div class="kb-meta">{{ p.document_count ? `${p.document_count} 篇文档` : '暂无文档' }}</div>
              <div class="kb-stats">
                <span>文本块 {{ p.indexed_chunks }}</span>
                <span v-if="p.tags?.length">{{ p.tags.join(' / ') }}</span>
              </div>
              <div class="kb-card-foot">
                <button class="link-btn" type="button" @click="toggleKbDocs(p.unit_id)">
                  {{ openKb === p.unit_id ? '收起文档' : `文档${p.document_count ? `（${p.document_count}）` : ''}` }}
                </button>
              </div>
              <div v-if="openKb === p.unit_id" class="kb-files">
                <div v-if="kbLoadingId === p.unit_id" class="muted kb-files-hint">加载中…</div>
                <template v-else-if="(kbDocs[p.unit_id] || []).length">
                  <button
                    v-for="d in kbDocs[p.unit_id]"
                    :key="d.filename"
                    class="kb-file"
                    type="button"
                    @click="openDoc(p.unit_id, d.filename)"
                  >
                    <span class="kb-file-name">{{ d.filename }}</span>
                    <span class="kb-file-meta">{{ d.doc_type || '未分类' }} · {{ d.chunk_count }} 块</span>
                  </button>
                </template>
                <div v-else class="muted kb-files-hint">该知识库还没有已索引文档。</div>
              </div>
            </div>
          </div>
          <p v-else class="muted side-empty">选择知识库后显示概览。</p>

          <div v-if="activeTools.length" class="side-section">
            <div class="side-label">生效工具</div>
            <div class="tool-chips">
              <el-tag v-for="t in activeTools" :key="t" size="small" effect="plain">{{ toolLabels[t] || t }}</el-tag>
            </div>
          </div>
        </el-tab-pane>

        <el-tab-pane label="历史对话" name="history">
          <div class="history-toolbar">
            <span class="muted">共 {{ conversations.length }} 条</span>
            <el-button link :loading="historyLoading" @click="loadConversations">刷新</el-button>
          </div>
          <div v-if="!conversations.length && !historyLoading" class="muted" style="padding: 20px 0">
            暂无历史对话，发送第一条消息后会自动保存。
          </div>
          <div
            v-for="c in conversations"
            :key="c.conversation_id"
            class="history-item"
            :class="{ active: c.conversation_id === conversationId, disabled: busy }"
          >
            <div class="history-main" @click="openConversation(c)">
              <div class="history-title">{{ c.title || '新对话' }}</div>
              <div class="history-meta">
                {{ unitLabel(c.unit_id) }} · {{ c.message_count }} 条 · {{ formatTime(c.updated_at) }}
              </div>
              <div v-if="c.preview" class="history-preview">{{ c.preview }}</div>
            </div>
            <el-button link type="danger" :disabled="busy" @click.stop="removeConversation(c)">删除</el-button>
          </div>
        </el-tab-pane>
      </el-tabs>
    </aside>
    </transition>

    <el-drawer v-model="runDrawer" title="运行详情" size="520px">
      <div v-if="runLoading" class="muted">加载中…</div>
      <template v-else-if="runDetail">
        <div class="run-summary-card">
          <div class="run-summary-title">
            <div>
              <div class="run-eyebrow">AGENT RUN</div>
              <strong>执行流程</strong>
            </div>
            <el-tag size="small" :type="runDetail.run.status === 'completed' ? 'success' : runDetail.run.status === 'running' ? 'warning' : 'danger'" effect="light">
              {{ runStatusText(runDetail.run.status) }}
            </el-tag>
          </div>
          <div class="run-summary-metrics">
            <span>{{ runDetail.steps.length }} 次工具调用</span>
            <span>{{ runDetail.run.prompt_tokens + runDetail.run.completion_tokens }} tokens</span>
            <span>{{ runDuration(runDetail.run) || '耗时统计中' }}</span>
          </div>
          <div class="run-summary-meta">
            {{ unitLabel(runDetail.run.unit_id) }} · {{ expertLabel(runDetail.run.expert) }} · {{ formatTime(runDetail.run.started_at) }}
          </div>
        </div>

        <div v-if="runDetail.run.error" class="run-flow-error" role="alert">{{ runDetail.run.error }}</div>

        <div v-if="runDetail.run.question" class="run-flow-question run-flow-question-context">
          <span class="run-flow-question-mark" aria-hidden="true">问</span>
          <div class="run-flow-question-body">
            <div class="run-flow-question-label">本次问题</div>
            <p>{{ runDetail.run.question }}</p>
          </div>
        </div>

        <div class="run-flow">
          <div v-if="!runDetail.steps.length" class="run-flow-empty">本次运行未调用工具，Agent 直接生成了回答。</div>
          <div v-for="(s, i) in runDetail.steps" :key="i" class="run-flow-item" :class="{ failed: !!s.error }">
            <div class="run-flow-rail"><span class="run-flow-marker">{{ s.error ? '!' : i + 1 }}</span></div>
            <section class="run-flow-card">
              <div class="run-flow-heading">
                <strong>{{ toolLabels[s.tool] || s.tool || '执行步骤' }}</strong>
                <span class="run-flow-state" :class="{ failed: !!s.error }">{{ s.error ? '失败' : '完成' }}</span>
              </div>
              <div class="run-flow-caption">
                第 {{ s.step }} 轮
                <template v-if="s.latency_ms"> · {{ s.latency_ms }} ms</template>
              </div>
              <div class="run-qa-pair">
                <div class="run-qa-row">
                  <span class="run-qa-mark run-qa-mark-question" aria-hidden="true">问</span>
                  <div class="run-qa-body">
                    <div class="run-qa-label">工具提问</div>
                    <pre>{{ stepQuestion(s) }}</pre>
                  </div>
                </div>
                <div class="run-qa-row">
                  <span class="run-qa-mark run-qa-mark-answer" aria-hidden="true">答</span>
                  <div class="run-qa-body">
                    <div class="run-qa-label">工具回答</div>
                    <pre v-if="s.result_preview">{{ formatRunResult(s.result_preview) }}</pre>
                    <p v-else class="run-qa-empty">工具未返回内容</p>
                  </div>
                </div>
              </div>
              <div v-if="s.error" class="run-flow-step-error">{{ s.error }}</div>
            </section>
          </div>

          <div class="run-flow-item run-flow-end" :class="{ failed: runDetail.run.status !== 'completed' }">
            <div class="run-flow-rail"><span class="run-flow-marker">{{ runDetail.run.status === 'completed' ? '✓' : '!' }}</span></div>
            <div class="run-flow-card">
              <div class="run-flow-heading">
                <strong>{{ runDetail.run.status === 'completed' ? '运行完成' : runStatusText(runDetail.run.status) }}</strong>
                <span class="run-flow-state">{{ runDetail.run.finished_at ? formatTime(runDetail.run.finished_at) : '' }}</span>
              </div>
              <div class="run-flow-caption">{{ runDetail.run.completion_tokens }} 输出 tokens</div>
              <div class="run-qa-pair">
                <div class="run-qa-row">
                  <span class="run-qa-mark run-qa-mark-question" aria-hidden="true">问</span>
                  <div class="run-qa-body">
                    <div class="run-qa-label">用户问题</div>
                    <pre>{{ runDetail.run.question || '未记录用户问题' }}</pre>
                  </div>
                </div>
                <div class="run-qa-row run-qa-row-answer">
                  <span class="run-qa-mark run-qa-mark-answer" aria-hidden="true">答</span>
                  <div class="run-qa-body">
                    <div class="run-qa-label">最终回答</div>
                    <div v-if="runAnswer" class="run-answer md-body" v-html="renderMarkdownText(runAnswer)"></div>
                    <p v-else class="run-qa-empty">没有可显示的回答内容</p>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </template>
    </el-drawer>

    <el-dialog v-model="docDialog" :title="docName || '文档预览'" width="min(1100px, 94vw)" top="4vh">
      <DocumentPreview v-if="docDialog && docName" :src="docSrc" :filename="docName" />
      <template #footer>
        <div class="source-footer">
          <span class="muted">预览基于上传的原件渲染；不支持的格式可直接下载</span>
          <el-button size="small" @click="docDialog = false">关闭</el-button>
          <a class="el-button el-button--primary el-button--small" :href="docSrc + '?download=1'" target="_blank" rel="noopener">下载原件</a>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import axios from 'axios'
import { ElMessage, ElMessageBox } from 'element-plus'
import DocumentPreview from '../components/DocumentPreview.vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'

marked.use({ gfm: true, breaks: true })

interface Unit {
  unit_id: string
  name: string
}
interface UnitProfile {
  unit_id: string
  name: string
  unit_type: string
  industry: string
  tags?: string[]
  document_count?: number
  doc_types?: string[]
  audit_count: number
  open_hazards: number
  indexed_chunks: number
}
interface SuggestedTask {
  title: string
  desc: string
  prompt: string
  icon: string
}
interface Expert {
  id: string
  name: string
  description: string
  tools?: string[]
}
interface Skill {
  id: string
  name: string
  tools?: string[]
}
interface Citation {
  // "web" citations come from external search and must stay visually distinct
  // from indexed knowledge-base documents.
  source_type?: 'document' | 'web'
  unit_id?: string
  unit_name?: string
  filename?: string
  doc_type?: string
  title?: string
  url?: string
  site?: string
  content: string
  distance?: number
}
interface AgentStep {
  tool: string
  label: string
  status: 'running' | 'done' | 'error'
  arguments?: string
  result?: string
  latency?: number
  error?: string
}

interface Msg {
  role: 'user' | 'assistant'
  preface?: string
  content: string
  citations?: Citation[]
  steps?: AgentStep[]
  stepsOpen?: boolean
  runId?: string
  notice?: string
}
interface ConversationItem {
  conversation_id: string
  unit_id: string
  title: string
  message_count: number
  preview: string
  updated_at: string
}

// 流式输出时同一条消息会被反复渲染，按内容缓存可避免重复解析整段 Markdown。
const markdownCache = new WeakMap<Msg, { text: string; html: string }>()

function markdownToHtml(message: Msg) {
  const cached = markdownCache.get(message)
  if (cached && cached.text === message.content) return cached.html
  // 模型输出可能包含原始 HTML，必须先清洗再交给 v-html。
  const html = DOMPurify.sanitize(marked.parse(message.content || '') as string)
  markdownCache.set(message, { text: message.content, html })
  return html
}

function renderMarkdownText(text: string) {
  return DOMPurify.sanitize(marked.parse(text || '') as string)
}

function renderMarkdown(message: Msg, streaming: boolean) {
  const html = markdownToHtml(message).trimEnd()
  if (!streaming) return html
  // 让光标落在最后一个块级元素内部，跟随文字而不是另起一行。
  const cursor = '<span class="cursor"></span>'
  if (/<\/(p|li|h[1-6]|blockquote|pre|td|th)>$/.test(html)) {
    return html.replace(/<\/(p|li|h[1-6]|blockquote|pre|td|th)>$/, `${cursor}</$1>`)
  }
  return html + cursor
}

const toolLabels: Record<string, string> = {
  get_unit_profile: '读取知识库信息',
  query_hazards: '查询记录清单',
  audit_history: '读取历史记录',
  vector_search: '检索知识库',
}

const route = useRoute()
const units = ref<Unit[]>([])
const experts = ref<Expert[]>([])
const skills = ref<Skill[]>([])
const messages = ref<Msg[]>([])
const draft = ref('')
const composerInput = ref<{ focus: () => void } | null>(null)
const busy = ref(false)
const unitIds = ref<string[]>([])
const profiles = ref<UnitProfile[]>([])
const expert = ref('kb-expert')
const selectedSkills = ref<string[]>([])
// Opt-in per run: when enabled the question may leave the machine.
const webSearch = ref(false)
const importing = ref('')
const defaultExpert = ref('kb-expert')
const defaultSkills = ref<string[]>([])
const scroll = ref<HTMLElement>()
const showScrollToBottom = ref(false)
const configError = ref('')
const status = ref('')
const activeTools = ref<string[]>([])
const configReady = ref(false)
const conversationId = ref('')
const sideTab = ref('kb')
const sideCollapsed = ref(localStorage.getItem('kb-agent-side-collapsed') === '1')

const primaryProfile = computed(() => profiles.value[0])
const emptyStateTitle = computed(() =>
  primaryProfile.value
    ? `想从「${primaryProfile.value.name}」了解什么？`
    : '先选择知识库，或直接开始对话',
)
const emptyStateDescription = computed(() =>
  primaryProfile.value
    ? '可以从下面的常见任务开始，也可以直接描述你最终想解决的问题。'
    : '选择知识库后，我会根据其中内容提供更适合的问题示例。',
)

const suggestions = computed<SuggestedTask[]>(() => {
  const profile = primaryProfile.value
  if (!profile) {
    return [
      {
        title: '理解一份资料',
        desc: '总结重点，形成清晰结论',
        prompt: '请概括我接下来提供的资料的核心内容，并用三点列出关键结论。',
        icon: 'M7 3h7l4 4v14H7zM14 3v5h5M9.5 13h5M9.5 16.5h3',
      },
      {
        title: '查找依据',
        desc: '定位原文，避免凭印象回答',
        prompt: '请查找与【主题】相关的原文依据，说明结论并注明出处。',
        icon: 'M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14Zm5.5 12.5L21 21',
      },
      {
        title: '整理行动项',
        desc: '把内容转成可执行的清单',
        prompt: '请把相关要求整理成行动清单，列出责任方、时间要求和待确认事项。',
        icon: 'M5 6h14M5 12h9M5 18h11M19 15l2 2 3-4',
      },
    ]
  }

  const name = `「${profile.name}」`
  const tasks: SuggestedTask[] = [
    {
      title: '快速了解',
      desc: '概括内容、结论和适用范围',
      prompt: `请概括${name}的核心内容，并用三点列出最重要的结论，注明档案依据。`,
      icon: 'M7 3h7l4 4v14H7zM14 3v5h5M9.5 13h5M9.5 16.5h3',
    },
    {
      title: '查找依据',
      desc: '围绕一个主题定位原文',
      prompt: `请查找${name}中与【主题】相关的原文依据，说明结论并注明文件名。`,
      icon: 'M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14Zm5.5 12.5L21 21',
    },
  ]
  if ((profile?.open_hazards || 0) > 0) {
    tasks.push({
      title: '检查风险',
      desc: '查看未整改问题与建议动作',
      prompt: `请检查${name}当前的未整改隐患，按风险等级排列，并给出建议的下一步动作。`,
      icon: 'M12 3 2.5 20h19L12 3Zm0 6v5m0 3h.01',
    })
  } else if ((profile?.audit_count || 0) > 0) {
    tasks.push({
      title: '回看审核',
      desc: '汇总最近问题与缺失项',
      prompt: `请总结${name}最近一次审核发现的问题、风险等级和仍需补充的材料。`,
      icon: 'M5 4h14v16H5zM8 8h8M8 12h6M8 16h4',
    })
  } else {
    tasks.push({
      title: '检查缺项',
      desc: '找出表述不清或证据不足处',
      prompt: `请检查${name}中表述不清、缺少依据或需要进一步确认的内容，并按重要性列出。`,
      icon: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18Zm0 5v5m0 3h.01',
    })
  }
  if ((profile.document_count || 0) > 1) {
    tasks.push({
      title: '对比材料',
      desc: '找出不同文档的差异与冲突',
      prompt: `请对比${name}中的不同文档，找出它们对【主题】的要求差异、冲突和共同点。`,
      icon: 'M8 4H5v16h3M16 4h3v16h-3M12 8v8',
    })
  } else {
    tasks.push({
      title: '整理行动项',
      desc: '把要求转成责任、时间和待办',
      prompt: `请把${name}中的关键要求整理成行动清单，列出责任方、时间要求和待确认事项。`,
      icon: 'M5 6h14M5 12h9M5 18h11M19 15l2 2 3-4',
    })
  }
  return tasks
})

const followUpPrompts = computed(() => {
  const task = primaryProfile.value
  return [
    { label: '补充原文依据', prompt: '请为上面的回答逐条补充原文依据，并注明文件名。' },
    { label: '压缩成三点', prompt: '请把上面的回答压缩成三点，保留关键结论和必要依据。' },
    task && task.open_hazards > 0
      ? { label: '整理整改清单', prompt: '请把上面的内容整理成整改清单，列出问题、责任方、时限和验收标准。' }
      : { label: '指出不确定处', prompt: '请指出上面的回答中证据不足、可能存在歧义或需要进一步确认的部分。' },
  ]
})

function toggleSide() {
  sideCollapsed.value = !sideCollapsed.value
  localStorage.setItem('kb-agent-side-collapsed', sideCollapsed.value ? '1' : '0')
}

function closeSideOnOutsidePointerDown(event: PointerEvent) {
  if (sideCollapsed.value) return
  const target = event.target
  if (!(target instanceof Element)) return
  // Element Plus teleports select menus outside the aside; treat those poppers as part of the panel.
  if (target.closest('.chat-side, .side-panel-toggle, .el-popper')) return
  sideCollapsed.value = true
  localStorage.setItem('kb-agent-side-collapsed', '1')
}

// 「添加知识库」：展开侧边栏并切到知识库页签，避免用户再找入口。
function openKbPicker() {
  sideTab.value = 'kb'
  if (sideCollapsed.value) toggleSide()
}
const conversations = ref<ConversationItem[]>([])
const activeConversationTitle = computed(() =>
  conversations.value.find((item) => item.conversation_id === conversationId.value)?.title || '新对话',
)
const historyLoading = ref(false)
let controller: AbortController | null = null
let scrollFrame = 0
let autoScroll = true
let programmaticScroll = false
let requestGeneration = 0
let projectConfigRequest = 0
let profileRequest = 0

const activeToolNames = computed(() => activeTools.value.map((t) => toolLabels[t] || t).join('、'))

interface KbDoc {
  filename: string
  doc_type: string
  chunk_count: number
}

const kbDocs = ref<Record<string, KbDoc[]>>({})
const kbLoadingId = ref('')
const openKb = ref('')
const docDialog = ref(false)
const docUnitId = ref('')
const docName = ref('')

const docSrc = computed(() =>
  docUnitId.value && docName.value
    ? `/api/v1/units/${encodeURIComponent(docUnitId.value)}/documents/${encodeURIComponent(docName.value)}/file`
    : '',
)

async function loadProfiles() {
  const request = ++profileRequest
  if (!unitIds.value.length) {
    profiles.value = []
    return
  }
  try {
    const { data } = await axios.get('/api/v1/units/profiles', {
      params: { ids: unitIds.value.join(',') },
    })
    if (request === profileRequest) profiles.value = data || []
  } catch {
    if (request === profileRequest) profiles.value = []
  }
}

// Documents are listed per knowledge base so each selected project keeps its own
// file list; results are cached until the project is removed from the selection.
watch(unitIds, (ids) => {
  const next: Record<string, KbDoc[]> = {}
  for (const id of ids) {
    if (kbDocs.value[id]) next[id] = kbDocs.value[id]
  }
  kbDocs.value = next
  if (openKb.value && !ids.includes(openKb.value)) openKb.value = ''
})

async function toggleKbDocs(unitId: string) {
  if (openKb.value === unitId) {
    openKb.value = ''
    return
  }
  openKb.value = unitId
  if (kbDocs.value[unitId]) return
  kbLoadingId.value = unitId
  try {
    const { data } = await axios.get(`/api/v1/units/${encodeURIComponent(unitId)}/documents`)
    kbDocs.value = { ...kbDocs.value, [unitId]: data || [] }
  } catch {
    kbDocs.value = { ...kbDocs.value, [unitId]: [] }
    ElMessage.error('读取文档列表失败')
  } finally {
    kbLoadingId.value = ''
  }
}

function openDoc(unitId: string, filename: string) {
  docUnitId.value = unitId
  docName.value = filename
  docDialog.value = true
}

// 多选时以第一个知识库的 Agent/技能/工具配置为准。
async function applyProjectConfig() {
  const request = ++projectConfigRequest
  const primary = unitIds.value[0] || ''
  expert.value = defaultExpert.value
  selectedSkills.value = [...defaultSkills.value]
  activeTools.value = []
  if (!primary) {
    return
  }
  try {
    const { data } = await axios.get(`/api/v1/units/${encodeURIComponent(primary)}/agent-config`)
    if (request !== projectConfigRequest) return
    if (data.expert_id && experts.value.some((e) => e.id === data.expert_id)) {
      expert.value = data.expert_id
    }
    if (Array.isArray(data.skill_ids) && data.skill_ids.length) {
      selectedSkills.value = data.skill_ids
    }
    activeTools.value = data.tools || []
  } catch {
    if (request === projectConfigRequest) activeTools.value = []
  }
}

watch(
  unitIds,
  () => {
    if (configReady.value) applyProjectConfig()
    loadProfiles()
  },
  { deep: true },
)

watch(
  () => route.query.unit,
  (value) => {
    if (value) unitIds.value = [String(value)]
  },
  { immediate: true },
)

onMounted(async () => {
  scroll.value?.addEventListener('scroll', onScroll, { passive: true })
  document.addEventListener('pointerdown', closeSideOnOutsidePointerDown)
  try {
    units.value = (await axios.get('/api/v1/units')).data
  } catch {
    units.value = []
  }
  try {
    const cfg = (await axios.get('/api/v1/agent/config')).data
    experts.value = cfg.experts || []
    skills.value = cfg.skills || []
    defaultSkills.value = skills.value.map((s) => s.id)
    if (experts.value.length && !experts.value.some((e) => e.id === expert.value)) {
      expert.value = experts.value[0].id
    }
    defaultExpert.value = expert.value
    selectedSkills.value = [...defaultSkills.value]
  } catch (e: any) {
    configError.value = e.response?.data?.error || '读取 Agent 配置失败'
  }
  configReady.value = true
  await Promise.all([applyProjectConfig(), loadProfiles(), loadConversations()])
})

onUnmounted(() => {
  requestGeneration++
  scroll.value?.removeEventListener('scroll', onScroll)
  document.removeEventListener('pointerdown', closeSideOnOutsidePointerDown)
  controller?.abort()
})

function isActive(index: number) {
  return index === messages.value.length - 1
}

function preview(text: string, limit = 500) {
  return text.length > limit ? text.slice(0, limit) + '…' : text
}

function formatStepData(value: string) {
  try {
    const parsed = JSON.parse(value)
    if (Array.isArray(parsed) && parsed.some((item) => item && typeof item === 'object' && typeof item.content === 'string')) {
      return parsed
        .filter((item) => item && typeof item === 'object' && typeof item.content === 'string')
        .map((item, index) => `${item.filename || item.title || `片段 ${index + 1}`}\n${item.content.trim()}`)
        .join('\n\n')
    }
    return JSON.stringify(parsed, null, 2)
  } catch {
    return value
  }
}

function formatRunResult(value: string) {
  // Some persisted previews are JSON strings containing another JSON payload. Decode
  // those layers first; previews may be truncated, so the inner payload can still fail
  // JSON.parse and must be handled by the partial-content fallback below.
  let text = value
  for (let depth = 0; depth < 2; depth++) {
    try {
      const parsed = JSON.parse(text)
      if (typeof parsed === 'string') {
        text = parsed
        continue
      }
      return formatStepData(text)
    } catch {
      break
    }
  }

  const parsed = formatStepData(text)
  if (parsed !== text) return parsed

  // Run previews are truncated before persistence. Recover content fields from the
  // incomplete JSON instead of exposing the serialized payload to the reader.
  // Older records can also contain escaped quotes without an outer JSON string.
  const normalized = text.replace(/\\"/g, '"')
  const normalizedResult = formatStepData(normalized)
  if (normalizedResult !== normalized) return normalizedResult

  const excerpts: string[] = []
  for (const match of normalized.matchAll(/"content"\s*:\s*"((?:\\.|[^"\\])*)"/g)) {
    try {
      excerpts.push(JSON.parse(`"${match[1]}"`).trim())
    } catch {
      excerpts.push(match[1].replace(/\\r\\n|\\n/g, '\n').replace(/\\t/g, '\t'))
    }
  }
  if (excerpts.length) return excerpts.map((content, index) => `片段 ${index + 1}\n${content}`).join('\n\n')

  // The final content string is often cut before its closing quote. Recover
  // that remaining field without exposing the serialized wrapper.
  const partial = normalized.match(/"content"\s*:\s*"([\s\S]+)$/)
  if (partial) {
    const content = partial[1]
      .replace(/…\s*$/, '')
      .replace(/\s*[\]\}],?\s*$/, '')
    try {
      return JSON.parse(`"${content}"`).trim()
    } catch {
      return content
        .replace(/\\r\\n|\\n/g, '\n')
        .replace(/\\t/g, '\t')
        .replace(/\\"/g, '"')
    }
  }
  return normalized
}

function stepQuestion(step: RunDetail['steps'][number]) {
  const raw = step.arguments || ''
  try {
    const parsed = JSON.parse(raw)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      for (const key of ['query', 'question', 'q']) {
        const value = parsed[key]
        if (typeof value === 'string' && value.trim()) return value.trim()
      }
    }
  } catch {
    // Older previews may be truncated JSON; show the available text below.
  }
  return raw && raw !== '{}'
    ? formatStepData(raw)
    : '本次运行未记录工具提问'
}

function hostOf(url?: string) {
  try {
    return new URL(url || '').hostname
  } catch {
    return ''
  }
}

// 网页来源可以一键转录为文档：服务端抓取正文后走与上传相同的索引流水线。
async function importWeb(url: string) {
  const unit = unitIds.value[0]
  if (!unit) {
    ElMessage.warning('请先在右侧选择知识库')
    return
  }
  importing.value = url
  try {
    const { data } = await axios.post(`/api/v1/units/${encodeURIComponent(unit)}/web-import`, { url })
    if (data?.duplicate) {
      ElMessage.info(`该网页已入库：${data.filename}`)
    } else {
      ElMessage.success(`已加入索引队列：${data.filename}`)
    }
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '保存失败')
  } finally {
    importing.value = ''
  }
}

async function scrollToBottom(smooth = true) {
  await nextTick()
  autoScroll = true
  showScrollToBottom.value = false
  programmaticScroll = true
  scroll.value?.scrollTo({ top: scroll.value.scrollHeight, behavior: smooth ? 'smooth' : 'auto' })
  requestAnimationFrame(() => {
    if (isNearBottom()) programmaticScroll = false
  })
}

function isNearBottom() {
  const el = scroll.value
  if (!el) return true
  return el.scrollHeight - el.scrollTop - el.clientHeight < 120
}

// 用户向上翻阅历史时不要强行拉回底部。
function onScroll() {
  if (programmaticScroll) {
    autoScroll = true
    showScrollToBottom.value = false
    if (isNearBottom()) programmaticScroll = false
    return
  }
  autoScroll = isNearBottom()
  showScrollToBottom.value = !autoScroll
}

function jumpToBottom() {
  void scrollToBottom(true)
}

function queueScroll() {
  if (scrollFrame || !autoScroll) return
  scrollFrame = requestAnimationFrame(() => {
    scrollFrame = 0
    scroll.value?.scrollTo({ top: scroll.value.scrollHeight })
  })
}

function onEnter(event: KeyboardEvent) {
  // Never send while an IME (e.g. Chinese input) is composing.
  if (event.isComposing || event.keyCode === 229) return
  if (event.shiftKey) return
  event.preventDefault()
  send()
}

function removeUnit(unitId: string) {
  unitIds.value = unitIds.value.filter((id) => id !== unitId)
}

function newChat() {
  requestGeneration++
  stop()
  controller = null
  busy.value = false
  status.value = ''
  autoScroll = true
  programmaticScroll = false
  showScrollToBottom.value = false
  messages.value = []
  draft.value = ''
  conversationId.value = ''
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success('已复制')
  } catch {
    ElMessage.error('复制失败')
  }
}

async function useSuggestedPrompt(prompt: string) {
  draft.value = prompt
  await nextTick()
  composerInput.value?.focus()
}

interface RunDetail {
  run: {
    run_id: string
    unit_id: string
    expert: string
    question: string
    status: string
    steps: number
    prompt_tokens: number
    completion_tokens: number
    error: string
    started_at: string
    finished_at?: string | null
  }
  steps: Array<{
    step: number
    kind: string
    tool: string
    arguments: string
    result_preview: string
    latency_ms: number
    error: string
  }>
}

const runDrawer = ref(false)
const runLoading = ref(false)
const runDetail = ref<RunDetail | null>(null)
const runAnswer = ref('')

function runStatusText(status: string) {
  const map: Record<string, string> = { completed: '完成', failed: '失败', interrupted: '中断', running: '进行中' }
  return map[status] || status
}

function expertLabel(id: string) {
  return experts.value.find((item) => item.id === id)?.name || id || 'Agent'
}

function runDuration(run: RunDetail['run']) {
  if (!run.finished_at) return ''
  const ms = new Date(run.finished_at).getTime() - new Date(run.started_at).getTime()
  return ms > 0 ? `${(ms / 1000).toFixed(1)}s` : ''
}

// 运行详情：从 agent_runs / agent_steps 还原一次 run 的时间线。
async function openRun(runId?: string, answer = '') {
  if (!runId) return
  runDrawer.value = true
  runLoading.value = true
  runDetail.value = null
  runAnswer.value = answer
  try {
    const { data } = await axios.get<RunDetail>(`/api/v1/agent/runs/${encodeURIComponent(runId)}`)
    runDetail.value = data
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '读取运行详情失败')
  } finally {
    runLoading.value = false
  }
}

function stop() {
  controller?.abort()
}

// 重新回答最后一条问题：截断到该问题之后重新发送。
async function regenerate() {
  if (busy.value) return
  const index = messages.value.map((item) => item.role).lastIndexOf('user')
  if (index < 0) return
  const content = messages.value[index].content
  messages.value = messages.value.slice(0, index)
  draft.value = content
  autoScroll = true
  await send()
}

const unitLabel = (id: string) => units.value.find((u) => u.unit_id === id)?.name || (id || '未关联知识库')

function formatTime(value: string) {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

async function loadConversations() {
  historyLoading.value = true
  try {
    conversations.value = (await axios.get('/api/v1/agent/conversations')).data || []
  } catch {
    conversations.value = []
  } finally {
    historyLoading.value = false
  }
}



async function openConversation(row: ConversationItem) {
  if (busy.value) {
    ElMessage.warning('请先停止当前回答，再切换历史对话。')
    return
  }
  try {
    const { data } = await axios.get(
      `/api/v1/agent/conversations/${encodeURIComponent(row.conversation_id)}`,
    )
    conversationId.value = data.conversation_id || row.conversation_id
    unitIds.value = Array.isArray(data.unit_ids) && data.unit_ids.length
      ? data.unit_ids
      : data.unit_id
        ? [data.unit_id]
        : []
    messages.value = (data.messages || []).map(
      (m: any): Msg => ({
        role: m.role === 'assistant' ? 'assistant' : 'user',
        content: m.content || '',
        citations: m.citations || [],
        runId: m.run_id || undefined,
      }),
    )
    await scrollToBottom(false)
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '读取历史对话失败')
  }
}

async function removeConversation(row: ConversationItem) {
  try {
    await ElMessageBox.confirm('删除后不可恢复，确定删除这条对话？', '删除对话', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await axios.delete(`/api/v1/agent/conversations/${encodeURIComponent(row.conversation_id)}`)
    if (conversationId.value === row.conversation_id) newChat()
    ElMessage.success('已删除')
    await loadConversations()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '删除失败')
  }
}

async function send() {
  const content = draft.value.trim()
  if (!content || busy.value) return
  const generation = ++requestGeneration
  draft.value = ''
  busy.value = true
  status.value = '正在分析…'

  const history = messages.value
    .slice(-20)
    .map((m) => ({ role: m.role, content: m.content }))

  messages.value.push({ role: 'user', content })
  messages.value.push({ role: 'assistant', content: '' })
  const target = messages.value[messages.value.length - 1]
  autoScroll = true
  await scrollToBottom()
  if (generation !== requestGeneration) return
  const activeController = new AbortController()
  controller = activeController

  try {
    await streamAgent(
      {
        unit_ids: unitIds.value,
        message: content,
        expert: expert.value,
        skills: selectedSkills.value,
        web_search: webSearch.value,
        history,
        conversation_id: conversationId.value,
      },
      target,
      activeController.signal,
      generation,
    )
    if (generation === requestGeneration && !target.content) target.content = '没有收到回答，请重试。'
  } catch (error: any) {
    if (generation !== requestGeneration) return
    if (error?.name === 'AbortError') {
      if (!target.content) target.content = '已停止。'
    } else {
      target.content = error?.message || '请求失败，请检查服务和模型配置。'
    }
  } finally {
    if (generation === requestGeneration) {
      busy.value = false
      status.value = ''
      controller = null
      await scrollToBottom()
      // 静默刷新历史列表，避免抽屉打开时缺最新会话。
      if (conversationId.value) loadConversations()
    }
  }
}

async function streamAgent(payload: any, target: Msg, signal: AbortSignal, generation: number) {
  const response = await fetch('/api/v1/agent/stream', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
    signal,
  })
  if (!response.ok) {
    let message = `请求失败（${response.status}）`
    try {
      const data = await response.json()
      message = data?.error || message
    } catch {
      // Keep the status-based message.
    }
    throw new Error(message)
  }
  if (!response.body) throw new Error('当前浏览器不支持流式响应')

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let pendingText = ''
  let flushTimer: ReturnType<typeof setTimeout> | null = null

  const flushText = () => {
    if (flushTimer) clearTimeout(flushTimer)
    flushTimer = null
    if (!pendingText) return
    target.content += pendingText
    pendingText = ''
    queueScroll()
  }

  const scheduleTextFlush = () => {
    if (flushTimer) return
    // 合并高频 SSE 小片段，减少逐字触发 Markdown 解析和页面重排。
    flushTimer = setTimeout(flushText, 36)
  }

  const handle = (event: string, data: any) => {
    if (generation !== requestGeneration) return
    switch (event) {
      case 'run_started':
        if (data.conversation_id) conversationId.value = data.conversation_id
        if (data.run_id) target.runId = data.run_id
        status.value = '已连接，正在分析…'
        break
      case 'step_started':
        status.value = `分析中 · 第 ${data.step} 步`
        break
      case 'tool_call': {
        flushText()
        if (!target.steps?.length && target.content.trim()) {
          target.preface = target.content
          target.content = ''
        }
        const label = toolLabels[data.tool] || data.tool
        target.steps = target.steps || []
        target.steps.push({
          tool: data.tool,
          label,
          status: 'running',
          arguments: data.arguments,
        })
        status.value = `正在${label}…`
        break
      }
      case 'tool_result': {
        const label = toolLabels[data.tool] || data.tool
        const steps = target.steps || []
        const step = [...steps].reverse().find((item) => item.tool === data.tool && item.status === 'running')
        if (step) {
          step.status = data.error ? 'error' : 'done'
          step.result = data.result
          step.error = data.error
          step.latency = data.latency_ms
        }
        status.value = data.error ? `${label}失败` : `已完成${label}`
        break
      }
      case 'message_delta':
        pendingText += data.delta || ''
        if (pendingText) {
          status.value = ''
          scheduleTextFlush()
        }
        break
      case 'citations':
        target.citations = data.citations || []
        break
      case 'run_completed':
        flushText()
        if (data.answer) target.content = data.answer
        target.citations = data.citations || target.citations || []
        status.value = ''
        break
      case 'error': {
        const text = data.message || 'agent 执行失败'
        // 已经流出了部分内容时保留它，把错误降级为提示，避免答案被整段覆盖。
        if (target.content.trim()) target.notice = text
        else target.content = text
        status.value = ''
        break
      }
    }
  }

  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      let boundary = buffer.indexOf('\n\n')
      while (boundary >= 0) {
        const block = buffer.slice(0, boundary)
        buffer = buffer.slice(boundary + 2)
        let event = 'message'
        const dataLines: string[] = []
        for (const line of block.split('\n')) {
          if (line.startsWith('event:')) event = line.slice(6).trim()
          else if (line.startsWith('data:')) dataLines.push(line.slice(5).trim())
        }
        if (dataLines.length) {
          try {
            handle(event, JSON.parse(dataLines.join('\n')))
          } catch {
            // Ignore malformed frames and keep the stream alive.
          }
        }
        boundary = buffer.indexOf('\n\n')
      }
    }
  } finally {
    flushText()
  }
}
</script>
