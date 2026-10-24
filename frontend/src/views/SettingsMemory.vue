<template>
  <div class="settings-section">
    <header class="page-head">
      <div>
        <div class="eyebrow">Memory</div>
        <h1>记忆与上下文</h1>
        <p class="subtitle">
          模型每次只看到最近 {{ window.messages }} 条消息，每条超过 {{ window.runes }} 字会被截断。
          这里能看到实际带进去了哪几条。
        </p>
      </div>
      <div class="head-actions">
        <el-button @click="$router.push('/settings/retrieval')">调整窗口</el-button>
        <el-button :loading="loading" @click="loadConversations">刷新</el-button>
      </div>
    </header>

    <div v-if="error" class="notice error">{{ error }}</div>

    <div class="mem-layout">
      <aside class="mem-rail">
        <div class="mem-rail-head">会话 · {{ conversations.length }}</div>
        <div class="mem-rail-list" v-loading="loading">
          <button
            v-for="c in conversations"
            :key="c.conversation_id"
            type="button"
            class="mem-rail-item"
            :class="{ active: c.conversation_id === selectedId }"
            @click="selectConversation(c.conversation_id)"
          >
            <span class="mem-rail-title">{{ c.title || '未命名会话' }}</span>
            <span class="mem-rail-meta">
              {{ c.message_count }} 条 · {{ shortTime(c.updated_at) }}
            </span>
            <span v-if="c.preview" class="mem-rail-preview">{{ c.preview }}</span>
          </button>
          <p v-if="!loading && !conversations.length" class="mem-empty">还没有对话记录。</p>
        </div>
      </aside>

      <section class="mem-stage">
        <template v-if="selected">
          <header class="mem-stage-head">
            <div>
              <h2>{{ selected.title || '未命名会话' }}</h2>
              <p class="mem-stage-meta">
                共 {{ messages.length }} 条消息 ·
                带入模型 {{ inWindow.length }} 条（约 {{ windowChars }} 字）·
                滑出窗口 {{ messages.length - inWindow.length }} 条
              </p>
            </div>
          </header>

          <div v-if="memoryEnabled === false" class="mem-disabled">
            摘要与事实已关闭，当前只回放最近 {{ window.messages }} 条原文，更早的内容会直接丢失。
            <router-link to="/settings/retrieval">去开启</router-link>
          </div>

          <!-- 摘要：覆盖「已经滑出窗口」的部分。它是有损压缩，只能当上下文，不能当依据。 -->
          <section class="mem-block">
            <div class="mem-block-head">
              <span class="mem-block-title">历史摘要</span>
              <span class="mem-block-meta">
                <template v-if="memory.summary">覆盖到第 {{ memory.summary_upto }} 条</template>
                <template v-if="memory.summary_at"> · {{ shortTime(memory.summary_at) }}</template>
                <template v-else-if="!memory.summary">尚未生成</template>
              </span>
              <button class="mem-rebuild" type="button" :disabled="rebuilding" @click="rebuildMemory">
                {{ rebuilding ? '重建中…' : '重建' }}
              </button>
            </div>
            <p v-if="memory.summary" class="mem-summary">{{ memory.summary }}</p>
            <p v-else class="mem-block-hint">
              消息超出窗口后会自动压缩成摘要。也可以点「重建」把窗口之前的内容一次性压缩。
            </p>
            <p v-if="memory.summary" class="mem-block-hint">
              摘要只用于理解上下文，不会作为回答依据，也不会出现在引用编号里。
            </p>
          </section>

          <!-- 事实：用户明确说过的约束，比摘要更该长期有效。 -->
          <section class="mem-block">
            <div class="mem-block-head">
              <span class="mem-block-title">记忆事实</span>
              <span class="mem-block-meta">{{ memory.facts.length }} 条</span>
            </div>
            <ul v-if="memory.facts.length" class="mem-facts">
              <li v-for="f in memory.facts" :key="f.id">
                <span class="mem-fact-kind" :class="f.kind">{{ factKind(f.kind) }}</span>
                <span class="mem-fact-text">{{ f.content }}</span>
                <button v-if="f.source_message_id" class="mem-fact-src" type="button" @click="jumpTo(f.source_message_id)">
                  第 {{ f.source_message_id }} 条
                </button>
              </li>
            </ul>
            <p v-else class="mem-block-hint">
              还没有提取到约束。用户明确说「只看某个库」「用表格回答」这类话时才会记录。
            </p>
          </section>

          <details v-if="memory.history.length" class="mem-history">
            <summary>已失效的旧说法 · {{ memory.history.length }}</summary>
            <ul>
              <li v-for="f in memory.history" :key="f.id">
                <span class="mem-fact-kind stale">{{ factTopic(f.topic) }}</span>
                <span class="mem-fact-text stale">{{ f.content }}</span>
                <span class="mem-fact-when">{{ shortTime(f.superseded_at || '') }}</span>
              </li>
            </ul>
            <p class="mem-block-hint">
              用户改主意时，旧说法会被同主题的新说法取代并保留在这里；模型只会看到上面仍然有效的那几条。
            </p>
          </details>

          <!-- 这条比数字更重要：窗口是「最近 N 条」，不是「全部」。
               二十轮之后，第一轮说的约束就掉出去了，而且没有任何提示。 -->
          <p class="mem-note">
            窗口按条数滑动，不做摘要。掉出去的内容不会进入模型，也不会被压缩进上下文——
            长会话里早期说过的约束会就此失效。
          </p>

          <div class="mem-messages" v-loading="loadingDetail">
            <div
              v-for="(m, i) in messages"
              :key="m.id"
              class="mem-msg"
              :class="[m.role, { out: isOutOfWindow(m, i), flash: flashId === m.id }]"
              :data-id="m.id"
            >
              <div class="mem-msg-head">
                <span class="mem-role">{{ m.role === 'user' ? '你' : '助手' }}</span>
                <span class="mem-msg-time">{{ shortTime(m.created_at) }}</span>
                <span class="mem-msg-chars">{{ charCount(m.content) }} 字</span>
                <span v-if="isOutOfWindow(m, i)" class="mem-out-tag">已滑出窗口，不会带入模型</span>
                <span v-else-if="charCount(m.content) > window.runes" class="mem-cut-tag">
                  超出单条上限，带入时截断为 {{ window.runes }} 字
                </span>
              </div>
              <div class="mem-msg-body" :class="{ expanded: expanded[m.id] }">
                {{ m.content }}
              </div>
              <button
                v-if="charCount(m.content) > 160"
                type="button"
                class="mem-toggle"
                @click="expanded[m.id] = !expanded[m.id]"
              >{{ expanded[m.id] ? '收起' : '展开全文' }}</button>
            </div>
            <p v-if="!loadingDetail && !messages.length" class="mem-empty">这条会话没有消息。</p>
          </div>
        </template>
        <div v-else class="mem-stage-empty">
          <p>选择左侧一个会话，查看它实际带进模型的历史。</p>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import axios from 'axios'

interface ConversationRow {
  conversation_id: string
  unit_id: string
  title: string
  message_count: number
  preview: string
  updated_at: string
}

interface MessageRow {
  id: number
  role: string
  content: string
  created_at: string
  citations?: unknown[]
}

const loading = ref(false)
const loadingDetail = ref(false)
const error = ref('')
const conversations = ref<ConversationRow[]>([])
const selectedId = ref('')
const messages = ref<MessageRow[]>([])
const expanded = reactive<Record<number, boolean>>({})

// 窗口参数直接来自检索设置页，保持一致：这里显示的就是实际生效的值。
const window = reactive({ messages: 20, runes: 4000 })

const selected = computed(() => conversations.value.find((c) => c.conversation_id === selectedId.value) || null)

interface Fact {
  id: number
  kind: string
  topic: string
  content: string
  confidence: number
  source_message_id: number
  active: boolean
  superseded_at?: string | null
}

const memory = ref<{
  summary: string
  summary_upto: number
  summary_at: string | null
  facts: Fact[]
  history: Fact[]
}>({
  summary: '',
  summary_upto: 0,
  summary_at: null,
  facts: [],
  history: [],
})
const memoryEnabled = ref(true)
const rebuilding = ref(false)
const flashId = ref(0)

const factKind = (kind: string) =>
  ({ constraint: '约束', preference: '偏好', decision: '结论' } as Record<string, string>)[kind] || '约束'

// 主题决定「哪些说法是同一件事」，取代只在同主题内发生。
const factTopic = (topic: string) =>
  ({ scope: '范围', format: '格式', identity: '身份', language: '语言', focus: '关注' } as Record<string, string>)[topic] || '其他'

async function loadMemory(id: string) {
  try {
    const { data } = await axios.get(`/api/v1/agent/conversations/${id}/memory`)
    memory.value = {
      summary: data.summary || '',
      summary_upto: data.summary_upto || 0,
      summary_at: data.summary_at || null,
      facts: data.facts || [],
      history: data.history || [],
    }
    memoryEnabled.value = data.enabled !== false
  } catch {
    memory.value = { summary: '', summary_upto: 0, summary_at: null, facts: [], history: [] }
  }
}

async function rebuildMemory() {
  if (!selectedId.value) return
  rebuilding.value = true
  try {
    await axios.post(`/api/v1/agent/conversations/${selectedId.value}/memory/rebuild`)
    await loadMemory(selectedId.value)
  } catch {
    // 重建失败保留旧摘要，页面上不弹窗打断阅读。
  } finally {
    rebuilding.value = false
  }
}

function jumpTo(messageId: number) {
  const el = document.querySelector(`.mem-msg[data-id="${messageId}"]`)
  if (!el) return
  el.scrollIntoView({ behavior: 'smooth', block: 'center' })
  flashId.value = messageId
  setTimeout(() => {
    if (flashId.value === messageId) flashId.value = 0
  }, 1600)
}

const charCount = (text: string) => Array.from(text || '').length

// 与后端一致：只回放 user / assistant，取最近 N 条。
const eligible = computed(() => messages.value.filter((m) => m.role === 'user' || m.role === 'assistant'))
const inWindow = computed(() => eligible.value.slice(-window.messages))
const cutoffId = computed(() => inWindow.value[0]?.id ?? Number.MAX_SAFE_INTEGER)

const windowChars = computed(() =>
  inWindow.value.reduce((sum, m) => sum + Math.min(charCount(m.content), window.runes), 0),
)

function isOutOfWindow(m: MessageRow, _index: number) {
  if (m.role !== 'user' && m.role !== 'assistant') return true
  return m.id < cutoffId.value
}

const shortTime = (v: string) =>
  v ? new Date(v).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '—'

async function loadWindow() {
  try {
    const { data } = await axios.get('/api/v1/settings/retrieval')
    window.messages = data.values?.memory_messages ?? 20
    window.runes = data.values?.memory_runes ?? 4000
  } catch {
    // 读不到设置就按默认值展示，不影响查看消息。
  }
}

async function loadConversations() {
  loading.value = true
  error.value = ''
  try {
    const { data } = await axios.get('/api/v1/agent/conversations', { params: { limit: 50 } })
    conversations.value = data || []
    if (!conversations.value.some((c) => c.conversation_id === selectedId.value)) {
      selectedId.value = conversations.value[0]?.conversation_id || ''
    }
    if (selectedId.value) await selectConversation(selectedId.value)
    else messages.value = []
  } catch (e: any) {
    conversations.value = []
    messages.value = []
    error.value = e.response?.data?.error || '读取会话列表失败'
  } finally {
    loading.value = false
  }
}

async function selectConversation(id: string) {
  selectedId.value = id
  loadingDetail.value = true
  try {
    const { data } = await axios.get(`/api/v1/agent/conversations/${id}`)
    messages.value = data.messages || []
    await loadMemory(id)
  } catch (e: any) {
    messages.value = []
    error.value = e.response?.data?.error || '读取会话内容失败'
  } finally {
    loadingDetail.value = false
  }
}

onMounted(async () => {
  await loadWindow()
  await loadConversations()
})
</script>

<style scoped>
.mem-disabled {
  margin-top: 12px;
  padding: 9px 12px;
  border: 1px solid var(--border-strong);
  border-radius: 8px;
  background: var(--bg-subtle);
  font-size: calc(12px * var(--fs-scale));
  line-height: 1.7;
  color: var(--text-secondary);
}

.mem-block {
  margin-top: 14px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
}

.mem-block-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
}

.mem-block-title {
  font-size: calc(12.5px * var(--fs-scale));
  font-weight: 600;
  color: var(--text);
}

.mem-block-meta {
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.mem-rebuild {
  margin-left: auto;
  padding: 0;
  border: 0;
  background: none;
  font: inherit;
  font-size: calc(11px * var(--fs-scale));
  color: var(--accent);
  cursor: pointer;
}

.mem-rebuild:disabled {
  color: var(--text-muted);
  cursor: default;
}

.mem-summary {
  margin: 9px 0 0;
  font-size: calc(12.5px * var(--fs-scale));
  line-height: 1.85;
  color: var(--text);
  white-space: pre-wrap;
}

.mem-block-hint {
  margin: 8px 0 0;
  font-size: calc(11.5px * var(--fs-scale));
  line-height: 1.7;
  color: var(--text-muted);
}

.mem-facts {
  margin: 8px 0 0;
  padding: 0;
  list-style: none;
}

.mem-facts li {
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 6px 0;
  border-top: 1px dashed var(--border);
}

.mem-facts li:first-child {
  border-top: 0;
}

.mem-fact-kind {
  flex: 0 0 auto;
  padding: 1px 7px;
  border-radius: 999px;
  background: var(--accent-soft);
  color: var(--accent);
  font-size: calc(10px * var(--fs-scale));
}

.mem-fact-kind.preference {
  background: var(--bg-hover);
  color: var(--text-secondary);
}

.mem-fact-kind.decision {
  background: var(--bg-hover);
  color: var(--text);
}

.mem-fact-text {
  flex: 1;
  min-width: 0;
  font-size: calc(12.5px * var(--fs-scale));
  color: var(--text);
}

.mem-fact-src {
  flex: 0 0 auto;
  padding: 0;
  border: 0;
  background: none;
  font: inherit;
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
  text-decoration: underline dotted;
  cursor: pointer;
}

.mem-history {
  margin-top: 10px;
  font-size: calc(12px * var(--fs-scale));
}

.mem-history summary {
  cursor: pointer;
  font-size: calc(11.5px * var(--fs-scale));
  color: var(--text-muted);
  list-style: none;
}

.mem-history summary::-webkit-details-marker {
  display: none;
}

.mem-history summary::before {
  content: '▸';
  display: inline-block;
  margin-right: 6px;
  transition: transform 0.14s ease;
}

.mem-history[open] summary::before {
  transform: rotate(90deg);
}

.mem-history ul {
  margin: 8px 0 0;
  padding: 0;
  list-style: none;
}

.mem-history li {
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 6px 0;
  border-top: 1px dashed var(--border);
  opacity: 0.72;
}

.mem-fact-kind.stale {
  background: var(--bg-hover);
  color: var(--text-muted);
}

.mem-fact-text.stale {
  color: var(--text-secondary);
  text-decoration: line-through;
  text-decoration-color: var(--border-strong);
}

.mem-fact-when {
  flex: 0 0 auto;
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.mem-msg.flash {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
}

.mem-layout {
  display: grid;
  grid-template-columns: 260px minmax(0, 1fr);
  gap: 16px;
  align-items: start;
}

.mem-rail {
  position: sticky;
  top: 12px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface);
  overflow: hidden;
}

.mem-rail-head {
  padding: 10px 12px;
  border-bottom: 1px solid var(--border);
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.mem-rail-list {
  max-height: calc(100vh - 260px);
  overflow-y: auto;
  padding: 6px;
}

.mem-rail-item {
  display: flex;
  flex-direction: column;
  gap: 3px;
  width: 100%;
  padding: 9px 10px;
  margin-bottom: 2px;
  border: 0;
  border-radius: 9px;
  background: transparent;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.mem-rail-item:hover {
  background: var(--bg-subtle);
}

.mem-rail-item.active {
  background: var(--accent-soft);
}

.mem-rail-title {
  font-size: calc(13px * var(--fs-scale));
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mem-rail-meta {
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.mem-rail-preview {
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mem-stage {
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface);
  padding: 18px 20px 20px;
  min-height: 320px;
}

.mem-stage-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.mem-stage-head h2 {
  margin: 0 0 4px;
  font-size: calc(18px * var(--fs-scale));
  font-weight: 600;
}

.mem-stage-meta {
  margin: 0;
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-muted);
}

.mem-unit {
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.mem-note {
  margin: 12px 0 0;
  padding: 9px 12px;
  border-left: 2px solid var(--border-strong);
  font-size: calc(12px * var(--fs-scale));
  line-height: 1.7;
  color: var(--text-secondary);
}

.mem-messages {
  margin-top: 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.mem-msg {
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: 10px;
}

.mem-msg.user {
  background: var(--bg-subtle);
}

.mem-msg.out {
  opacity: 0.55;
}

.mem-msg-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.mem-role {
  padding: 1px 7px;
  border-radius: 999px;
  background: var(--bg-hover);
  color: var(--text-secondary);
  font-size: calc(11px * var(--fs-scale));
}

.mem-out-tag {
  color: var(--warning, #b45309);
}

.mem-cut-tag {
  color: var(--text-muted);
}

.mem-msg-body {
  font-size: calc(12.5px * var(--fs-scale));
  line-height: 1.75;
  color: var(--text);
  white-space: pre-wrap;
  word-break: break-word;
  display: -webkit-box;
  -webkit-line-clamp: 4;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.mem-msg-body.expanded {
  -webkit-line-clamp: unset;
  display: block;
}

.mem-toggle {
  margin-top: 6px;
  padding: 0;
  border: 0;
  background: none;
  font: inherit;
  font-size: calc(11px * var(--fs-scale));
  color: var(--accent);
  cursor: pointer;
}

.mem-empty,
.mem-stage-empty {
  padding: 16px 4px;
  margin: 0;
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-muted);
}

.mem-stage-empty {
  display: grid;
  place-items: center;
  min-height: 320px;
}

@media (max-width: 900px) {
  .mem-disabled {
  margin-top: 12px;
  padding: 9px 12px;
  border: 1px solid var(--border-strong);
  border-radius: 8px;
  background: var(--bg-subtle);
  font-size: calc(12px * var(--fs-scale));
  line-height: 1.7;
  color: var(--text-secondary);
}

.mem-block {
  margin-top: 14px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
}

.mem-block-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
}

.mem-block-title {
  font-size: calc(12.5px * var(--fs-scale));
  font-weight: 600;
  color: var(--text);
}

.mem-block-meta {
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.mem-rebuild {
  margin-left: auto;
  padding: 0;
  border: 0;
  background: none;
  font: inherit;
  font-size: calc(11px * var(--fs-scale));
  color: var(--accent);
  cursor: pointer;
}

.mem-rebuild:disabled {
  color: var(--text-muted);
  cursor: default;
}

.mem-summary {
  margin: 9px 0 0;
  font-size: calc(12.5px * var(--fs-scale));
  line-height: 1.85;
  color: var(--text);
  white-space: pre-wrap;
}

.mem-block-hint {
  margin: 8px 0 0;
  font-size: calc(11.5px * var(--fs-scale));
  line-height: 1.7;
  color: var(--text-muted);
}

.mem-facts {
  margin: 8px 0 0;
  padding: 0;
  list-style: none;
}

.mem-facts li {
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 6px 0;
  border-top: 1px dashed var(--border);
}

.mem-facts li:first-child {
  border-top: 0;
}

.mem-fact-kind {
  flex: 0 0 auto;
  padding: 1px 7px;
  border-radius: 999px;
  background: var(--accent-soft);
  color: var(--accent);
  font-size: calc(10px * var(--fs-scale));
}

.mem-fact-kind.preference {
  background: var(--bg-hover);
  color: var(--text-secondary);
}

.mem-fact-kind.decision {
  background: var(--bg-hover);
  color: var(--text);
}

.mem-fact-text {
  flex: 1;
  min-width: 0;
  font-size: calc(12.5px * var(--fs-scale));
  color: var(--text);
}

.mem-fact-src {
  flex: 0 0 auto;
  padding: 0;
  border: 0;
  background: none;
  font: inherit;
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
  text-decoration: underline dotted;
  cursor: pointer;
}

.mem-history {
  margin-top: 10px;
  font-size: calc(12px * var(--fs-scale));
}

.mem-history summary {
  cursor: pointer;
  font-size: calc(11.5px * var(--fs-scale));
  color: var(--text-muted);
  list-style: none;
}

.mem-history summary::-webkit-details-marker {
  display: none;
}

.mem-history summary::before {
  content: '▸';
  display: inline-block;
  margin-right: 6px;
  transition: transform 0.14s ease;
}

.mem-history[open] summary::before {
  transform: rotate(90deg);
}

.mem-history ul {
  margin: 8px 0 0;
  padding: 0;
  list-style: none;
}

.mem-history li {
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 6px 0;
  border-top: 1px dashed var(--border);
  opacity: 0.72;
}

.mem-fact-kind.stale {
  background: var(--bg-hover);
  color: var(--text-muted);
}

.mem-fact-text.stale {
  color: var(--text-secondary);
  text-decoration: line-through;
  text-decoration-color: var(--border-strong);
}

.mem-fact-when {
  flex: 0 0 auto;
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.mem-msg.flash {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
}

.mem-layout {
    grid-template-columns: 1fr;
  }

  .mem-rail {
    position: static;
  }

  .mem-rail-list {
    max-height: 200px;
  }
}
</style>
