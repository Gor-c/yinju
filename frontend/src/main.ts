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
import Agent from './views/Agent.vue'
import AgentDefs from './views/AgentDefs.vue'
import SettingsLayout from './views/SettingsLayout.vue'
import SettingsModels from './views/SettingsModels.vue'
import SettingsWebSearch from './views/SettingsWebSearch.vue'
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
    { path: '/agents', redirect: '/settings/agents' },
    {
      path: '/settings',
      component: SettingsLayout,
      redirect: '/settings/models',
      children: [
        { path: 'models', component: SettingsModels },
        { path: 'web-search', component: SettingsWebSearch },
        { path: 'agents', component: AgentDefs },
      ],
    },
  ],
})

createApp(App).use(router).use(ElementPlus).mount('#app')
