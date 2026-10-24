<template>
  <section class="settings-section settings-agent-defs">
    <header class="page-head">
      <div>
        <div class="eyebrow">Agent Registry</div>
        <h1>Agent 与技能</h1>
        <p class="subtitle">上传后即可在项目设置中绑定，对话时按绑定自动带出可用工具。</p>
      </div>
      <div class="head-actions">
        <el-button :loading="loading" @click="load">刷新</el-button>
      </div>
    </header>

    <div v-if="error" class="notice error">{{ error }}</div>

    <div class="panel upload-panel">
      <div class="upload-row">
        <div>
          <b>上传 Agent 定义</b>
          <p class="muted">支持 JSON 或 Markdown（front matter）。工具可选：get_unit_profile、query_hazards、audit_history、vector_search。</p>
        </div>
        <el-upload :show-file-list="false" :http-request="(o: any) => upload(o, 'experts')" accept=".json,.md,.markdown">
          <el-button type="primary" plain :loading="uploading === 'experts'">选择文件</el-button>
        </el-upload>
      </div>
      <div class="upload-row">
        <div>
          <b>上传技能定义</b>
          <p class="muted">技能用于补充回答规则，并通过 tools 声明它需要的只读工具。</p>
        </div>
        <el-upload :show-file-list="false" :http-request="(o: any) => upload(o, 'skills')" accept=".json,.md,.markdown">
          <el-button type="primary" plain :loading="uploading === 'skills'">选择文件</el-button>
        </el-upload>
      </div>

      <el-collapse class="format-help">
        <el-collapse-item title="定义文件格式示例" name="format">
          <p class="muted">Agent（JSON）：</p>
          <pre class="def-sample">{
  "id": "kb-analyst",
  "name": "资料分析员",
  "description": "基于知识库检索结果给出结论",
  "system_prompt": "你是资料分析助手，只依据工具返回的内容回答。",
  "tools": ["vector_search", "get_unit_profile"]
}</pre>
          <p class="muted">技能（Markdown）：</p>
          <pre class="def-sample">---
id: doc-review
name: 文档核对
description: 核对文档结论与依据
tools: vector_search
---

先检索相关片段，再逐条列出结论与依据，引用时标注文件名。</pre>
          <p class="muted">行业专属能力（风险清单、合规检查等）用同样格式上传即可，无需改代码。</p>
          <p class="muted">同名 id 再次上传会覆盖旧定义。</p>
        </el-collapse-item>
      </el-collapse>
    </div>

    <div class="panel" style="padding: 18px; margin-bottom: 18px">
      <div class="panel-head">
        <b>Agent 定义</b>
        <span class="muted">共 {{ experts.length }} 个</span>
      </div>
      <el-table :data="experts" v-loading="loading">
        <el-table-column label="名称" min-width="200">
          <template #default="s">
            <span>{{ s.row.name }}</span>
            <div class="muted">{{ s.row.id }}</div>
          </template>
        </el-table-column>
        <el-table-column label="说明" min-width="220" show-overflow-tooltip>
          <template #default="s">{{ s.row.description || '—' }}</template>
        </el-table-column>
        <el-table-column label="工具" min-width="220">
          <template #default="s">
            <el-tag v-for="t in s.row.tools || []" :key="t" size="small" effect="plain" class="tool-tag">
              {{ toolLabel(t) }}
            </el-tag>
            <span v-if="!s.row.tools?.length" class="muted">全部</span>
          </template>
        </el-table-column>
        <el-table-column label="来源" width="90">
          <template #default="s">
            <el-tag size="small" :type="s.row.source === 'upload' ? 'success' : 'info'" effect="light">
              {{ s.row.source === 'upload' ? '上传' : '内置' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="" width="90" align="right">
          <template #default="s">
            <el-button v-if="s.row.source === 'upload'" link type="danger" @click="remove('experts', s.row.id)">删除</el-button>
          </template>
        </el-table-column>
        <template #empty><div class="muted" style="padding: 28px 0">暂无 Agent 定义</div></template>
      </el-table>
    </div>

    <div class="panel" style="padding: 18px">
      <div class="panel-head">
        <b>技能定义</b>
        <span class="muted">共 {{ skills.length }} 个</span>
      </div>
      <el-table :data="skills" v-loading="loading">
        <el-table-column label="名称" min-width="200">
          <template #default="s">
            <span>{{ s.row.name }}</span>
            <div class="muted">{{ s.row.id }}</div>
          </template>
        </el-table-column>
        <el-table-column label="说明" min-width="220" show-overflow-tooltip>
          <template #default="s">{{ s.row.description || '—' }}</template>
        </el-table-column>
        <el-table-column label="工具" min-width="220">
          <template #default="s">
            <el-tag v-for="t in s.row.tools || []" :key="t" size="small" effect="plain" class="tool-tag">
              {{ toolLabel(t) }}
            </el-tag>
            <span v-if="!s.row.tools?.length" class="muted">不限制</span>
          </template>
        </el-table-column>
        <el-table-column label="来源" width="90">
          <template #default="s">
            <el-tag size="small" :type="s.row.source === 'upload' ? 'success' : 'info'" effect="light">
              {{ s.row.source === 'upload' ? '上传' : '内置' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="" width="90" align="right">
          <template #default="s">
            <el-button v-if="s.row.source === 'upload'" link type="danger" @click="remove('skills', s.row.id)">删除</el-button>
          </template>
        </el-table-column>
        <template #empty><div class="muted" style="padding: 28px 0">暂无技能定义</div></template>
      </el-table>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import axios from 'axios'
import { ElMessage, ElMessageBox } from 'element-plus'

interface ExpertItem {
  id: string
  name: string
  description: string
  tools?: string[]
  source?: string
}
interface SkillItem {
  id: string
  name: string
  description: string
  tools?: string[]
  source?: string
}

const experts = ref<ExpertItem[]>([])
const skills = ref<SkillItem[]>([])
const loading = ref(false)
const uploading = ref('')
const error = ref('')

const toolLabels: Record<string, string> = {
  get_unit_profile: '知识库信息',
  query_hazards: '记录清单',
  audit_history: '历史记录',
  vector_search: '知识库检索',
}
const toolLabel = (name: string) => toolLabels[name] || name

async function load() {
  loading.value = true
  error.value = ''
  try {
    const { data } = await axios.get('/api/v1/agent/definitions')
    experts.value = data.experts || []
    skills.value = data.skills || []
  } catch (e: any) {
    experts.value = []
    skills.value = []
    error.value = e.response?.status === 503
      ? '数据库未配置，无法读取 Agent 定义。'
      : e.response?.data?.error || '读取 Agent 定义失败。'
  } finally {
    loading.value = false
  }
}

async function upload(options: any, kind: 'experts' | 'skills') {
  uploading.value = kind
  const form = new FormData()
  form.append('file', options.file)
  try {
    await axios.post(`/api/v1/agent/${kind}`, form)
    ElMessage.success('定义已上传')
    options.onSuccess?.({})
    await load()
  } catch (e: any) {
    const message = e.response?.data?.error || '上传失败，请检查文件格式'
    ElMessage.error(message)
    options.onError?.(e)
  } finally {
    uploading.value = ''
  }
}

async function remove(kind: 'experts' | 'skills', id: string) {
  try {
    await ElMessageBox.confirm(`确定删除定义「${id}」？已绑定该定义的项目将回退到默认配置。`, '删除定义', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await axios.delete(`/api/v1/agent/${kind}/${encodeURIComponent(id)}`)
    ElMessage.success('已删除')
    await load()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '删除失败')
  }
}

onMounted(load)
</script>

<style scoped>
.upload-panel {
  padding: 18px;
  margin-bottom: 18px;
}
.upload-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  padding: 10px 0;
}
.upload-row p {
  margin: 4px 0 0;
  max-width: 720px;
}
.format-help {
  margin-top: 8px;
}
.def-sample {
  margin: 6px 0 14px;
  padding: 12px;
  border-radius: 8px;
  background: var(--code-bg, rgba(128, 128, 128, 0.1));
  font-size: calc(12px * var(--fs-scale));
  line-height: 1.6;
  overflow: auto;
}
.panel-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  margin-bottom: 10px;
}
.tool-tag {
  margin: 0 4px 4px 0;
}
</style>
