<template>
  <div class="page settings-page">
    <div class="settings-shell">
      <nav class="settings-nav" aria-label="设置分类">
        <div class="settings-nav-heading">配置项目</div>
        <router-link
          v-for="item in items"
          :key="item.to"
          :to="item.to"
          class="settings-nav-item"
          :class="{ active: isActive(item.to) }"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path :d="item.icon" />
          </svg>
          <span class="settings-nav-copy">
            <b>{{ item.label }}</b>
            <small>{{ item.hint }}</small>
          </span>
        </router-link>
      </nav>

      <section class="settings-panel">
        <router-view />
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

const route = useRoute()

// OpenAI-style section list: the sidebar keeps a single entry and the section
// switch lives on the page itself.
const items = [
  {
    to: '/settings/models',
    label: '模型服务',
    hint: 'Embedding 与对话模型',
    icon: 'M12 3v3M12 18v3M4.2 7.5l2.6 1.5M17.2 15l2.6 1.5M4.2 16.5l2.6-1.5M17.2 9l2.6-1.5M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8Z',
  },
  {
    to: '/settings/web-search',
    label: '联网搜索',
    hint: '搜索服务与网页入库',
    icon: 'M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14ZM20 20l-3.5-3.5',
  },
  {
    to: '/settings/agents',
    label: 'Agent 与技能',
    hint: '定义、技能与工具绑定',
    icon: 'M12 4v4M9 13h.01M15 13h.01M9 16h6M4 8h16v11H4z',
  },
]

const isActive = (to: string) => computed(() => route.path === to).value
</script>
