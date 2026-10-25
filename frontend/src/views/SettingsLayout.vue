<template>
  <div class="page settings-page">
    <div class="settings-shell">
      <nav class="settings-nav" aria-label="设置分类">
        <div v-for="group in groups" :key="group.label" class="settings-nav-group">
          <div class="settings-nav-heading">{{ group.label }}</div>
          <router-link
            v-for="item in group.items"
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
        </div>
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
const groups = [
  {
    label: '外观',
    items: [
      {
        to: '/settings/brand',
        label: '系统外观',
        hint: '项目名称、副标题与图标',
        icon: 'M4 7h16v10H4zM7 11h6M7 14h4',
      },
    ],
  },
  {
    label: '能力',
    items: [
      {
        to: '/settings/models',
        label: '模型服务',
        hint: '对话、向量与重排模型',
        icon: 'M12 3v3M12 18v3M4.2 7.5l2.6 1.5M17.2 15l2.6 1.5M4.2 16.5l2.6-1.5M17.2 9l2.6-1.5M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8Z',
      },
      {
        to: '/settings/web-search',
        label: '联网搜索',
        hint: '外部信息与网页入库',
        icon: 'M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14ZM20 20l-3.5-3.5',
      },
    ],
  },
  {
    label: '检索与记忆',
    items: [
      {
        to: '/settings/retrieval',
        label: '检索参数',
        hint: '召回、阈值与切块',
        icon: 'M4 6h16M4 12h10M4 18h7M18 15l2 2 2.5-3',
      },
      {
        to: '/settings/memory',
        label: '记忆与上下文',
        hint: '摘要、事实与回放窗口',
        icon: 'M12 4a5 5 0 0 1 5 5c0 2-1.2 3.2-2.4 4.2-.9.8-1.6 1.5-1.6 2.8h-2c0-2.2 1.2-3.3 2.5-4.4C14.4 10.7 15 10 15 9a3 3 0 1 0-6 0H7a5 5 0 0 1 5-5Zm-1 15h2v2h-2v-2Z',
      },
      {
        to: '/settings/drafts',
        label: '知识沉淀',
        hint: '问答候选、整理与采纳',
        icon: 'M5 4h11l3 3v13H5zM8 9h8M8 13h8M8 17h5',
      },
    ],
  },
  {
    label: '扩展',
    items: [
      {
        to: '/settings/agents',
        label: 'Agent 与技能',
        hint: '定义、技能与工具绑定',
        icon: 'M12 4v4M9 13h.01M15 13h.01M9 16h6M4 8h16v11H4z',
      },
    ],
  },
  {
    label: '部署',
    items: [
      {
        to: '/settings/instance',
        label: '实例配置',
        hint: '数据库、向量库与运行目录',
        icon: 'M4 6h16v5H4zM4 13h16v5H4M8 8.5h.01M8 15.5h.01',
      },
    ],
  },
]

const isActive = (to: string) => computed(() => route.path === to).value
</script>
