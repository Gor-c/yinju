<template>
  <div class="app-shell">
    <aside
      class="sidebar"
      :class="{ collapsed: sidebarCollapsed }"
      @click="expandSidebar"
    >
      <div class="brand">
        <span class="brand-icon" aria-hidden="true">
          <BrandMark :size="32" />
        </span>
        <div>
          <b>{{ brand.app_name }}</b>
          <small>{{ brand.app_subtitle }}</small>
        </div>
        <button
          v-if="!sidebarCollapsed"
          class="sidebar-toggle"
          type="button"
          title="收起侧栏"
          aria-label="收起侧栏"
          @click.stop="collapseSidebar"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <rect x="3" y="4" width="18" height="16" rx="3" />
            <path d="M9 4v16" />
            <path d="m16 9-3 3 3 3" />
          </svg>
        </button>
      </div>

      <nav class="nav">
        <div v-if="!sidebarCollapsed" class="nav-group-label">工作区</div>
        <el-tooltip content="Agent 对话" placement="right" :disabled="!sidebarCollapsed" :show-after="120">
          <router-link to="/agent" class="nav-item" :class="{ active: activeMenu === '/agent' }">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
              <path d="M21 12a8.5 8.5 0 0 1-8.5 8.5H4l2.2-3.1A8.5 8.5 0 1 1 21 12Z" />
              <path d="M8.5 11h7M8.5 14.5h4.5" />
            </svg>
            <span>Agent 对话</span>
          </router-link>
        </el-tooltip>
        <el-tooltip content="上传文档" placement="right" :disabled="!sidebarCollapsed" :show-after="120">
          <router-link to="/upload" class="nav-item" :class="{ active: activeMenu === '/upload' }">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 15V4m0 0L8 8m4-4 4 4" />
              <path d="M4 15v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3" />
            </svg>
            <span>上传文档</span>
          </router-link>
        </el-tooltip>
        <el-tooltip content="知识库" placement="right" :disabled="!sidebarCollapsed" :show-after="120">
          <router-link to="/units" class="nav-item" :class="{ active: activeMenu === '/units' }">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
              <path d="M4 21V6.5L12 3l8 3.5V21" />
              <path d="M9 21v-5h6v5M8 9h.01M12 9h.01M16 9h.01M8 12.5h.01M12 12.5h.01M16 12.5h.01" />
            </svg>
            <span>知识库</span>
          </router-link>
        </el-tooltip>
        <el-tooltip content="文档库" placement="right" :disabled="!sidebarCollapsed" :show-after="120">
          <router-link to="/documents" class="nav-item" :class="{ active: activeMenu === '/documents' }">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
              <path d="M7 3h7l4 4v10a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2Z" />
              <path d="M14 3v4h4M9 12h5M9 15.5h3" />
            </svg>
            <span>文档库</span>
          </router-link>
        </el-tooltip>

        <!-- 扩展区单独成组：以后加连接器、插件、外部工具都落在这里，
             不会再和「用知识库」的日常操作混在一起。 -->
        <div v-if="!sidebarCollapsed" class="nav-group-label">扩展</div>
        <el-tooltip content="Agent 与技能" placement="right" :disabled="!sidebarCollapsed" :show-after="120">
          <router-link to="/settings/agents" class="nav-item" :class="{ active: activeMenu === '/settings/agents' }">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
              <path d="M4 7h4M14 7h6M4 17h6M16 17h4" />
              <circle cx="11" cy="7" r="2.4" />
              <circle cx="13" cy="17" r="2.4" />
            </svg>
            <span>Agent 与技能</span>
          </router-link>
        </el-tooltip>
      </nav>

      <div class="sidebar-bottom">
        <div class="sidebar-footer">
          <div class="status-line">
            <i class="dot" :class="{ offline: !online }"></i>
            {{ online ? '服务已连接' : '服务不可用' }}
          </div>
          <div class="version">knowledge-base · v0.1.0</div>
        </div>
        <el-tooltip content="设置" placement="right" :disabled="!sidebarCollapsed" :show-after="120">
          <router-link to="/settings" class="model-settings-entry" :class="{ active: activeMenu === '/settings' }">
            <span class="settings-avatar" aria-hidden="true">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
                <circle cx="12" cy="12" r="3" />
                <path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.9.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.9 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.9l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.9.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.9-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.9V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1Z" />
              </svg>
            </span>
            <span class="settings-copy">
              <b>设置</b>
              <small>模型、联网搜索与 Agent</small>
            </span>
            <svg class="settings-chevron" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="m9 18 6-6-6-6" />
            </svg>
          </router-link>
        </el-tooltip>
      </div>
    </aside>

    <div class="app-main">
      <header class="topbar">
        <div class="breadcrumb">
          <span>{{ brand.app_name }}</span>
          <span>/</span>
          <b>{{ title }}</b>
        </div>
        <div class="topbar-actions">
          <router-link v-if="activeJobs > 0" to="/upload" class="job-pill">
            <i class="spinner"></i>
            {{ activeJobs }} 个入库任务进行中
          </router-link>
          <button class="icon-btn" type="button" :title="theme === 'dark' ? '切换到浅色' : '切换到深色'" @click="toggleTheme">
            <svg v-if="theme === 'dark'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
              <circle cx="12" cy="12" r="4" />
              <path d="M12 2.5v2M12 19.5v2M4.6 4.6l1.4 1.4M18 18l1.4 1.4M2.5 12h2M19.5 12h2M4.6 19.4 6 18M18 6l1.4-1.4" />
            </svg>
            <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
              <path d="M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5Z" />
            </svg>
          </button>
        </div>
      </header>

      <main class="content">
        <router-view />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import axios from 'axios'
import BrandMark from './components/BrandMark.vue'
// 名称、副标题、图标由后端配置：页面可随时改，不必重新打包。
import { brand, loadBrand } from './brand'
import { applyAppearance } from './appearance'

type Theme = 'light' | 'dark'

const route = useRoute()
const online = ref(true)
const theme = ref<Theme>('light')
const activeJobs = ref(0)
const sidebarCollapsed = ref(localStorage.getItem('kb-sidebar-collapsed') === '1')
let timer: ReturnType<typeof setInterval> | undefined

// 展开时用品牌行的按钮收起；折叠后点击侧栏任意位置展开。
function collapseSidebar() {
  sidebarCollapsed.value = true
  localStorage.setItem('kb-sidebar-collapsed', '1')
}

function expandSidebar() {
  if (!sidebarCollapsed.value) return
  sidebarCollapsed.value = false
  localStorage.setItem('kb-sidebar-collapsed', '0')
}
let jobTimer: ReturnType<typeof setInterval> | undefined

async function checkHealth() {
  try {
    const res = await axios.get('/health', { timeout: 5000 })
    online.value = res.data?.status === 'ok'
  } catch {
    online.value = false
  }
}

// 入库任务在后台运行，这里跨页面提示进行中的数量。
async function checkJobs() {
  try {
    const res = await axios.get('/api/v1/jobs', {
      params: { status: 'queued,running', limit: 50 },
      timeout: 5000,
    })
    activeJobs.value = res.data?.count ?? res.data?.items?.length ?? 0
  } catch {
    activeJobs.value = 0
  }
}

function applyTheme(value: Theme) {
  theme.value = value
  document.documentElement.classList.toggle('dark', value === 'dark')
  localStorage.setItem('fa-theme', value)
  // 强调色分浅/深两档（同一个色值在深底上会发闷），模式一变要重算。
  applyAppearance(brand.value.theme, brand.value.radius, brand.value.font_size, brand.value.font_family)
}

function toggleTheme() {
  applyTheme(theme.value === 'dark' ? 'light' : 'dark')
}

onMounted(() => {
  // 品牌配置最先拉：标签页标题和图标要在首屏就正确，
  // 否则用户会先看到默认名再跳变。
  loadBrand()
  const saved = localStorage.getItem('fa-theme')
  const preferred = window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  applyTheme(saved === 'dark' || saved === 'light' ? saved : preferred)
  checkHealth()
  checkJobs()
  timer = setInterval(checkHealth, 30000)
  jobTimer = setInterval(checkJobs, 8000)
})

onUnmounted(() => {
  clearInterval(timer)
  clearInterval(jobTimer)
})

const labels: Record<string, string> = {
  '/agent': 'Agent 对话',
  '/upload': '上传文档',
  '/units': '知识库',
  '/documents': '文档库',
  '/agents': 'Agent 与技能',
  '/settings': '设置',
  '/settings/brand': '系统外观',
  '/settings/instance': '实例配置',
  '/settings/models': '模型服务',
  '/settings/web-search': '联网搜索',
  '/settings/retrieval': '检索参数',
  '/settings/memory': '记忆与上下文',
  '/settings/agents': 'Agent 与技能',
}
const activeMenu = computed(() => {
  if (route.path.startsWith('/units')) return '/units'
  // Agent 与技能在「扩展」组里有自己的入口，不该同时点亮底部的设置。
  if (route.path === '/settings/agents') return '/settings/agents'
  if (route.path.startsWith('/settings')) return '/settings'
  return route.path
})
const title = computed(() => {
  if (route.path.startsWith('/units/')) return '知识库详情'
  // 没登记的设置子页至少回落成「设置」，面包屑不留空档。
  return labels[route.path] || (route.path.startsWith('/settings') ? '设置' : '')
})
</script>
<style scoped>
.job-pill {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 5px 12px;
  border-radius: 999px;
  font-size: calc(12px * var(--fs-scale));
  font-weight: 600;
  text-decoration: none;
  color: var(--text, inherit);
  background: rgba(230, 162, 60, 0.16);
  border: 1px solid rgba(230, 162, 60, 0.4);
}
.job-pill:hover {
  background: rgba(230, 162, 60, 0.26);
}
.spinner {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  border: 2px solid rgba(230, 162, 60, 0.35);
  border-top-color: rgba(230, 162, 60, 1);
  animation: job-spin 0.9s linear infinite;
}
@keyframes job-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
