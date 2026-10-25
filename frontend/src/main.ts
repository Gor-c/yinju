import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
import App from './App.vue'
import Upload from './views/Upload.vue'
import Units from './views/Units.vue'
import Documents from './views/Documents.vue'
import UnitDetail from './views/UnitDetail.vue'
import UnitTree from './views/UnitTree.vue'
import Agent from './views/Agent.vue'
import AgentDefs from './views/AgentDefs.vue'
import SettingsLayout from './views/SettingsLayout.vue'
import SettingsModels from './views/SettingsModels.vue'
import SettingsRetrieval from './views/SettingsRetrieval.vue'
import SettingsMemory from './views/SettingsMemory.vue'
import SettingsWebSearch from './views/SettingsWebSearch.vue'
import SettingsBrand from './views/SettingsBrand.vue'
import SettingsInstance from './views/SettingsInstance.vue'
import NotFound from './views/NotFound.vue'
import './style.css'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/agent' },
    { path: '/agent', component: Agent },
    { path: '/upload', component: Upload },
    { path: '/units', component: Units },
    { path: '/documents', component: Documents },
    { path: '/units/:id', component: UnitDetail },
    { path: '/units/:id/outline', component: UnitTree },
    { path: '/agents', redirect: '/settings/agents' },
    {
      path: '/settings',
      component: SettingsLayout,
      redirect: '/settings/models',
      children: [
        { path: 'models', component: SettingsModels },
        { path: 'brand', component: SettingsBrand },
        { path: 'instance', component: SettingsInstance },
        { path: 'retrieval', component: SettingsRetrieval },
        { path: 'memory', component: SettingsMemory },
        { path: 'web-search', component: SettingsWebSearch },
        { path: 'agents', component: AgentDefs },
      ],
    },
    // 兜底：服务端对未知路径会回落到 index.html，前端必须给出明确提示，
    // 否则用户看到的是一个没有内容的空壳页面。
    { path: '/:pathMatch(.*)*', component: NotFound },
  ],
})

createApp(App).use(router).use(ElementPlus).mount('#app')
