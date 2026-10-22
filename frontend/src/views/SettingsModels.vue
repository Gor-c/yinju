<template>
  <section class="settings-section">
    <header class="page-head">
      <div>
        <div class="eyebrow">Model Providers</div>
        <h1>模型服务设置</h1>
        <p class="subtitle">管理多个模型来源，对话与向量各自选择，切换即时生效</p>
      </div>
      <div class="head-actions">
        <el-button :loading="loading" @click="load">重新读取</el-button>
        <el-button type="primary" @click="openCreate">新增来源</el-button>
      </div>
    </header>

    <div v-if="loadError" class="notice error">{{ loadError }}</div>
    <div v-if="saveError" class="notice error">{{ saveError }}</div>
    <div v-if="saveOk" class="notice">{{ saveOkText }}</div>

    <el-skeleton v-if="loading && !loaded" :rows="5" animated />

    <div v-else-if="loaded" class="settings-content">
      <div class="card settings-form-card">
        <div class="effective-head">
          <div class="card-title">当前生效</div>
          <el-button link :loading="testing.llm || testing.embed" @click="testAll">测试连接</el-button>
        </div>

        <div class="effective-row">
          <span class="effective-label">对话</span>
          <el-select
            v-model="selection.llm"
            style="width: 240px"
            :disabled="switching"
            @change="(value: number) => changeSelection('llm', Number(value))"
          >
            <el-option :value="0" label="手工配置 / 环境变量" />
            <el-option v-for="p in providersFor('llm')" :key="p.id" :value="p.id" :label="p.name" />
          </el-select>
          <span class="effective-value muted" :title="`${meta.llm?.base_url || ''} · ${meta.llm?.model || ''}`">
            {{ meta.llm?.model || '未配置' }}
            <template v-if="meta.llm?.base_url"> · {{ shortURL(meta.llm.base_url) }}</template>
          </span>
        </div>

        <div class="effective-row">
          <span class="effective-label">向量</span>
          <el-select
            v-model="selection.embed"
            style="width: 240px"
            :disabled="switching"
            @change="(value: number) => changeSelection('embed', Number(value))"
          >
            <el-option :value="0" label="手工配置 / 环境变量" />
            <el-option v-for="p in providersFor('embed')" :key="p.id" :value="p.id" :label="p.name" />
          </el-select>
          <span class="effective-value muted" :title="`${meta.embed?.base_url || ''} · ${meta.embed?.model || ''}`">
            {{ meta.embed?.model || '未配置' }}
            <template v-if="meta.embed?.base_url"> · {{ shortURL(meta.embed.base_url) }}</template>
          </span>
        </div>

        <div v-if="testResult?.chat || testResult?.embedding" class="effective-tests">
          <span v-if="testResult?.chat" class="test-chip">
            对话
            <el-tag size="small" :type="testResult.chat.ok ? 'success' : 'danger'" effect="light">
              {{ testResult.chat.ok ? '正常' : '失败' }}
            </el-tag>
            <span class="muted">{{ testResult.chat.latency_ms }} ms</span>
            <span v-if="testResult.chat.error" class="err">{{ testResult.chat.error }}</span>
          </span>
          <span v-if="testResult?.embedding" class="test-chip">
            向量
            <el-tag size="small" :type="testResult.embedding.ok ? 'success' : 'danger'" effect="light">
              {{ testResult.embedding.ok ? '正常' : '失败' }}
            </el-tag>
            <span class="muted">
              {{ testResult.embedding.latency_ms }} ms
              <template v-if="testResult.embedding.dimensions"> · {{ testResult.embedding.dimensions }} 维</template>
            </span>
            <span v-if="testResult.embedding.error" class="err">{{ testResult.embedding.error }}</span>
          </span>
        </div>
        <div v-if="testError.llm || testError.embed" class="err">{{ testError.llm || testError.embed }}</div>
      </div>

      <div class="card settings-form-card">
        <div class="effective-head">
          <div>
            <div class="card-title">模型来源</div>
            <div class="muted">本地服务（Ollama / LM Studio / vLLM）地址填到 <code>/v1</code>，API Key 可留空</div>
          </div>
        </div>

        <div v-if="!providers.length" class="provider-empty muted">
          还没有来源。新增后即可在上方切换，例如 <code>http://localhost:11434/v1</code> + <code>qwen2.5:7b</code>。
        </div>

        <div v-for="p in providers" :key="p.id" class="provider-row">
          <div class="provider-info">
            <div class="provider-name">
              {{ p.name }}
              <el-tag v-if="p.active_llm" size="small" type="success" effect="light">对话中</el-tag>
              <el-tag v-if="p.active_embed" size="small" type="warning" effect="light">向量中</el-tag>
            </div>
            <div class="provider-meta muted">
              <span class="nowrap">{{ shortURL(p.base_url) }}</span>
              <span>对话 {{ p.llm_model || '—' }}</span>
              <span>向量 {{ p.embed_model || '—' }}</span>
              <span>{{ keyText(p) }}</span>
            </div>
            <div v-if="providerTest[p.id]" class="provider-test">
              <el-tag size="small" :type="providerTest[p.id].ok ? 'success' : 'danger'" effect="light">
                {{ providerTest[p.id].ok ? '正常' : '失败' }}
              </el-tag>
              <span class="muted">{{ providerTest[p.id].latency_ms }} ms</span>
              <span v-if="providerTest[p.id].error" class="err">{{ providerTest[p.id].error }}</span>
            </div>
          </div>

          <div class="provider-actions">
            <el-button
              size="small"
              :type="p.active_llm ? 'success' : 'default'"
              :disabled="!p.llm_model || p.active_llm || switching"
              @click="changeSelection('llm', p.id)"
            >
              用作对话
            </el-button>
            <el-button
              size="small"
              :type="p.active_embed ? 'warning' : 'default'"
              :disabled="!p.embed_model || p.active_embed || switching"
              @click="changeSelection('embed', p.id)"
            >
              用作向量
            </el-button>
            <el-dropdown trigger="click" @command="(cmd: string) => onProviderCommand(cmd, p)">
              <el-button size="small" :loading="providerTesting[p.id]">更多</el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="test">测试连接</el-dropdown-item>
                  <el-dropdown-item command="edit">编辑</el-dropdown-item>
                  <el-dropdown-item command="delete" divided>删除</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </div>
        </div>
      </div>

      <el-collapse v-model="manualOpen" class="manual-collapse">
        <el-collapse-item name="manual" title="手工配置（未选择来源时生效，一般不常用）">
          <div class="manual-grid">
            <div class="manual-col">
              <div class="manual-title">对话模型</div>
              <el-form label-position="top">
                <el-form-item label="Base URL">
                  <el-input v-model="form.llm_base_url" placeholder="http://localhost:11434/v1" clearable />
                </el-form-item>
                <el-form-item label="API Key">
                  <el-input v-model="form.llm_api_key" type="password" show-password :placeholder="keyPlaceholder(meta.llm)" />
                </el-form-item>
                <el-form-item label="模型">
                  <div class="model-row">
                    <el-select v-model="form.llm_model" style="flex: 1" filterable allow-create clearable placeholder="qwen2.5:7b">
                      <el-option v-for="m in llmModelOptions" :key="m" :label="m" :value="m" />
                    </el-select>
                    <el-button :loading="fetching.llm" @click="fetchModels('llm')">获取</el-button>
                  </div>
                  <div v-if="listError.llm" class="err">{{ listError.llm }}</div>
                </el-form-item>
              </el-form>
            </div>

            <div class="manual-col">
              <div class="manual-title">向量模型</div>
              <el-form label-position="top">
                <el-form-item label="Base URL">
                  <el-input v-model="form.embed_base_url" placeholder="http://localhost:11434/v1" clearable />
                </el-form-item>
                <el-form-item label="API Key">
                  <el-input v-model="form.embed_api_key" type="password" show-password :placeholder="keyPlaceholder(meta.embed)" />
                </el-form-item>
                <el-form-item label="模型">
                  <div class="model-row">
                    <el-select v-model="form.embed_model" style="flex: 1" filterable allow-create clearable placeholder="bge-m3">
                      <el-option v-for="m in embedModelOptions" :key="m" :label="m" :value="m" />
                    </el-select>
                    <el-button :loading="fetching.embed" @click="fetchModels('embed')">获取</el-button>
                  </div>
                  <div class="muted" style="margin-top: 4px">必须返回 {{ dimensions }} 维</div>
                  <div v-if="listError.embed" class="err">{{ listError.embed }}</div>
                </el-form-item>
              </el-form>
            </div>
          </div>
          <div class="manual-actions">
            <el-button type="primary" :loading="saving" @click="save">保存手工配置</el-button>
          </div>
        </el-collapse-item>
      </el-collapse>

      <div class="muted footnote">
        生效顺序：选中的来源 → 手工配置 → 环境变量。更换 embedding 模型或维度后需重建索引。
      </div>
    </div>

    <el-dialog
      v-model="dialog.open"
      :title="dialog.id ? '编辑来源' : '新增来源'"
      width="560px"
      :close-on-click-modal="false"
      @closed="resetDialog"
    >
      <el-form label-position="top">
        <el-form-item label="名称">
          <el-input v-model="dialog.form.name" placeholder="本地 Ollama" clearable />
        </el-form-item>
        <el-form-item label="Base URL">
          <el-input v-model="dialog.form.base_url" placeholder="http://localhost:11434/v1" clearable />
          <div class="muted" style="margin-top: 4px">填到 <code>/v1</code> 这一层；本地地址可免 API Key</div>
        </el-form-item>
        <el-form-item :label="dialog.apiKeySet ? `API Key（已保存 ${dialog.apiKeyHint}，留空不修改）` : 'API Key'">
          <el-input
            v-model="dialog.form.api_key"
            type="password"
            show-password
            :placeholder="dialog.id ? '留空表示不修改' : '本地服务可留空'"
          />
        </el-form-item>

        <div class="dialog-models">
          <el-form-item label="对话模型">
            <div class="model-row">
              <el-select
                v-model="dialog.form.llm_model"
                style="flex: 1"
                placeholder="qwen2.5:7b"
                filterable
                allow-create
                default-first-option
                clearable
              >
                <el-option v-for="m in dialogOptions.llm" :key="m" :label="m" :value="m" />
              </el-select>
              <el-button :loading="dialogFetching.llm" @click="fetchDialogModels('llm')">获取</el-button>
            </div>
            <div v-if="dialogListError.llm" class="err">{{ dialogListError.llm }}</div>
          </el-form-item>
          <el-form-item label="向量模型">
            <div class="model-row">
              <el-select
                v-model="dialog.form.embed_model"
                style="flex: 1"
                placeholder="bge-m3"
                filterable
                allow-create
                default-first-option
                clearable
              >
                <el-option v-for="m in dialogOptions.embed" :key="m" :label="m" :value="m" />
              </el-select>
              <el-button :loading="dialogFetching.embed" @click="fetchDialogModels('embed')">获取</el-button>
            </div>
            <div v-if="dialogListError.embed" class="err">{{ dialogListError.embed }}</div>
          </el-form-item>
        </div>
        <div class="muted">至少填写一个模型；留空的角色表示该来源不提供该能力</div>
      </el-form>

      <div v-if="dialogTest" class="provider-test">
        <el-tag size="small" :type="dialogTest.ok ? 'success' : 'danger'" effect="light">
          {{ dialogTest.ok ? '连接正常' : '连接失败' }}
        </el-tag>
        <span class="muted">{{ dialogTest.latency_ms }} ms</span>
        <span v-if="dialogTest.error" class="err">{{ dialogTest.error }}</span>
      </div>
      <div v-if="dialogError" class="notice error" style="margin-top: 12px">{{ dialogError }}</div>

      <template #footer>
        <el-button :loading="dialogTesting" @click="testDialog">测试连接</el-button>
        <el-button @click="dialog.open = false">取消</el-button>
        <el-button type="primary" :loading="dialogSaving" @click="saveDialog">保存</el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import axios from 'axios'

type Source = 'db' | 'env' | 'unset' | 'provider'
type ProviderMeta = {
  base_url?: string
  model?: string
  api_key_set?: boolean
  api_key_hint?: string
  base_url_from?: Source
  model_from?: Source
  api_key_from?: Source
  provider_id?: number
  provider_name?: string
}

type ProviderItem = {
  id: number
  name: string
  base_url: string
  llm_model?: string
  embed_model?: string
  api_key_set?: boolean
  api_key_hint?: string
  active_llm?: boolean
  active_embed?: boolean
  local_endpoint?: boolean
}

type SettingsMeta = {
  embed?: ProviderMeta
  llm?: ProviderMeta
  providers?: ProviderItem[]
  selection?: { llm?: number; embed?: number }
  manual?: {
    embed?: { base_url?: string; model?: string }
    llm?: { base_url?: string; model?: string }
  }
  embedding_dimensions?: number
}

type TestResult = {
  embedding?: { ok: boolean; latency_ms: number; dimensions?: number; error?: string }
  chat?: { ok: boolean; latency_ms: number; error?: string }
}

type ModelItem = {
  id: string
  name?: string
  context_length?: number
  supported_features?: string[]
  output_modalities?: string[]
}

const loading = ref(false)
const loaded = ref(false)
const saving = ref(false)
const switching = ref(false)
const saveOkText = ref('配置已保存。')
const saveOk = ref(false)
const loadError = ref('')
const saveError = ref('')
const dimensions = ref(1024)
const meta = ref<SettingsMeta>({})
const providers = ref<ProviderItem[]>([])
const selection = reactive({ llm: 0, embed: 0 })
const testResult = ref<TestResult | null>(null)
const testing = reactive({ embed: false, llm: false })
const testError = reactive({ embed: '', llm: '' })
const providerTest = reactive<Record<number, { ok: boolean; latency_ms: number; error?: string }>>({})
const providerTesting = reactive<Record<number, boolean>>({})
const manualOpen = ref<string[]>([])

// 手工兜底配置：未选择来源时生效。
const form = reactive({
  embed_base_url: '',
  embed_model: '',
  embed_api_key: '',
  llm_base_url: '',
  llm_model: '',
  llm_api_key: '',
})

const embedModelOptions = ref(['bge-m3', 'BAAI/bge-m3', 'mxbai-embed-large'])
const llmModelOptions = ref(['qwen2.5:7b', 'Qwen/Qwen2.5-7B-Instruct', 'deepseek-v4.1-flash'])
const fetching = reactive({ embed: false, llm: false })
const listError = reactive({ embed: '', llm: '' })

const dialog = reactive({
  open: false,
  id: 0,
  apiKeySet: false,
  apiKeyHint: '',
  form: { name: '', base_url: '', api_key: '', llm_model: '', embed_model: '' },
})
const dialogOptions = reactive({ llm: [] as string[], embed: [] as string[] })
const dialogFetching = reactive({ llm: false, embed: false })
const dialogListError = reactive({ llm: '', embed: '' })
const dialogSaving = ref(false)
const dialogTesting = ref(false)
const dialogError = ref('')
const dialogTest = ref<{ ok: boolean; latency_ms: number; error?: string } | null>(null)

function shortURL(raw?: string) {
  if (!raw) return ''
  return raw.replace(/^https?:\/\//, '')
}

function keyPlaceholder(item?: ProviderMeta) {
  return item?.api_key_set ? `已配置 ${item.api_key_hint || ''}，留空不修改` : '本地服务可留空'
}

function keyText(p: ProviderItem) {
  if (p.api_key_set) return p.api_key_hint || '已配置'
  return p.local_endpoint ? '免 Key' : '未配置 Key'
}

function providersFor(role: 'llm' | 'embed') {
  return providers.value.filter((p) => (role === 'llm' ? !!p.llm_model : !!p.embed_model))
}

function errorText(e: unknown) {
  const err = e as { response?: { data?: { error?: string }; status?: number }; message?: string }
  const detail = err.response?.data?.error
  if (detail) return detail
  if (err.response?.status === 503) return '服务端未连接数据库，无法保存配置'
  return err.message || '请求失败'
}

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const res = await axios.get<SettingsMeta>('/api/v1/settings/models', { timeout: 10000 })
    meta.value = res.data || {}
    dimensions.value = res.data?.embedding_dimensions || 1024
    providers.value = res.data?.providers || []
    selection.llm = res.data?.selection?.llm || 0
    selection.embed = res.data?.selection?.embed || 0
    // 手工配置读 manual，避免把来源值误当成手工值再存回库。
    form.embed_base_url = res.data?.manual?.embed?.base_url || ''
    form.embed_model = res.data?.manual?.embed?.model || ''
    form.llm_base_url = res.data?.manual?.llm?.base_url || ''
    form.llm_model = res.data?.manual?.llm?.model || ''
    form.embed_api_key = ''
    form.llm_api_key = ''
    loaded.value = true
  } catch (e) {
    loadError.value = errorText(e)
    loaded.value = false
  } finally {
    loading.value = false
  }
}

async function changeSelection(role: 'llm' | 'embed', providerId: number) {
  switching.value = true
  saveError.value = ''
  saveOk.value = false
  try {
    await axios.put('/api/v1/settings/models/selection', { role, provider_id: providerId }, { timeout: 15000 })
    await load()
    saveOkText.value = providerId
      ? `${role === 'llm' ? '对话' : '向量'}模型已切换。`
      : `${role === 'llm' ? '对话' : '向量'}模型已回落到手工配置。`
    saveOk.value = true
  } catch (e) {
    saveError.value = errorText(e)
    await load()
  } finally {
    switching.value = false
  }
}

function onProviderCommand(command: string, p: ProviderItem) {
  if (command === 'test') void testProvider(p)
  else if (command === 'edit') openEdit(p)
  else if (command === 'delete') void removeProvider(p)
}

async function testProvider(p: ProviderItem) {
  providerTesting[p.id] = true
  const kind = p.llm_model && p.embed_model ? '' : p.llm_model ? 'llm' : 'embed'
  try {
    const res = await axios.post<TestResult>(
      `/api/v1/settings/models/providers/${p.id}/test`,
      { kind },
      { timeout: 60000 },
    )
    const item = res.data?.chat || res.data?.embedding
    if (item) providerTest[p.id] = { ok: !!item.ok, latency_ms: item.latency_ms || 0, error: item.error }
  } catch (e) {
    providerTest[p.id] = { ok: false, latency_ms: 0, error: errorText(e) }
  } finally {
    providerTesting[p.id] = false
  }
}

async function removeProvider(p: ProviderItem) {
  if (!window.confirm(`确定删除来源「${p.name}」？使用它的角色会回落到手工配置。`)) return
  saveError.value = ''
  try {
    await axios.delete(`/api/v1/settings/models/providers/${p.id}`, { timeout: 15000 })
    await load()
    saveOkText.value = '来源已删除。'
    saveOk.value = true
  } catch (e) {
    saveError.value = errorText(e)
  }
}

function openCreate() {
  dialog.open = true
  dialog.id = 0
  dialog.apiKeySet = false
  dialog.apiKeyHint = ''
  dialog.form = { name: '', base_url: '', api_key: '', llm_model: '', embed_model: '' }
  dialogError.value = ''
  dialogTest.value = null
  dialogOptions.llm = [...llmModelOptions.value]
  dialogOptions.embed = [...embedModelOptions.value]
}

function openEdit(p: ProviderItem) {
  dialog.open = true
  dialog.id = p.id
  dialog.apiKeySet = !!p.api_key_set
  dialog.apiKeyHint = p.api_key_hint || ''
  dialog.form = {
    name: p.name,
    base_url: p.base_url,
    api_key: '',
    llm_model: p.llm_model || '',
    embed_model: p.embed_model || '',
  }
  dialogError.value = ''
  dialogTest.value = null
  dialogOptions.llm = [...llmModelOptions.value]
  dialogOptions.embed = [...embedModelOptions.value]
}

function resetDialog() {
  dialogError.value = ''
  dialogTest.value = null
  dialogListError.llm = ''
  dialogListError.embed = ''
}

async function fetchDialogModels(kind: 'llm' | 'embed') {
  dialogFetching[kind] = true
  dialogListError[kind] = ''
  try {
    const res = await axios.post<{ models: ModelItem[] }>(
      '/api/v1/settings/models/list',
      {
        kind,
        base_url: dialog.form.base_url.trim(),
        api_key: dialog.form.api_key.trim(),
        provider_id: dialog.id,
      },
      { timeout: 25000 },
    )
    const ids = (res.data?.models || []).map((m) => m.id)
    dialogOptions[kind] = ids
    if (kind === 'llm' && !dialog.form.llm_model && ids.length) dialog.form.llm_model = ids[0]
    if (kind === 'embed' && !dialog.form.embed_model && ids.length) dialog.form.embed_model = ids[0]
  } catch (e) {
    dialogListError[kind] = errorText(e)
  } finally {
    dialogFetching[kind] = false
  }
}

async function testDialog() {
  dialogTesting.value = true
  dialogError.value = ''
  dialogTest.value = null
  const llmModel = dialog.form.llm_model.trim()
  const embedModel = dialog.form.embed_model.trim()
  if (!llmModel && !embedModel) {
    dialogError.value = '请先填写至少一个模型再测试'
    dialogTesting.value = false
    return
  }
  const kind = llmModel && embedModel ? '' : llmModel ? 'llm' : 'embed'
  try {
    const res = await axios.post<TestResult>(
      `/api/v1/settings/models/providers/${dialog.id || 0}/test`,
      {
        kind,
        base_url: dialog.form.base_url.trim(),
        api_key: dialog.form.api_key.trim(),
        llm_model: llmModel,
        embed_model: embedModel,
      },
      { timeout: 60000 },
    )
    const item = res.data?.chat || res.data?.embedding
    if (item) dialogTest.value = { ok: !!item.ok, latency_ms: item.latency_ms || 0, error: item.error }
  } catch (e) {
    dialogError.value = errorText(e)
  } finally {
    dialogTesting.value = false
  }
}

async function saveDialog() {
  dialogError.value = ''
  const baseURL = dialog.form.base_url.trim()
  if (!baseURL) {
    dialogError.value = '请填写 Base URL'
    return
  }
  if (!dialog.form.llm_model.trim() && !dialog.form.embed_model.trim()) {
    dialogError.value = '至少填写一个对话模型或向量模型'
    return
  }
  const payload: Record<string, string> = {
    name: dialog.form.name.trim(),
    base_url: baseURL,
    llm_model: dialog.form.llm_model.trim(),
    embed_model: dialog.form.embed_model.trim(),
  }
  if (dialog.form.api_key.trim()) payload.api_key = dialog.form.api_key.trim()

  dialogSaving.value = true
  try {
    if (dialog.id) {
      await axios.put(`/api/v1/settings/models/providers/${dialog.id}`, payload, { timeout: 15000 })
    } else {
      await axios.post('/api/v1/settings/models/providers', payload, { timeout: 15000 })
    }
    dialog.open = false
    await load()
    saveOkText.value = '来源已保存。'
    saveOk.value = true
  } catch (e) {
    dialogError.value = errorText(e)
  } finally {
    dialogSaving.value = false
  }
}

async function fetchModels(kind: 'embed' | 'llm') {
  const isEmbed = kind === 'embed'
  listError[kind] = ''
  fetching[kind] = true
  try {
    const res = await axios.post<{ models: ModelItem[] }>(
      '/api/v1/settings/models/list',
      {
        kind,
        base_url: isEmbed ? form.embed_base_url.trim() : form.llm_base_url.trim(),
        api_key: isEmbed ? form.embed_api_key.trim() : form.llm_api_key.trim(),
      },
      { timeout: 25000 },
    )
    const ids = (res.data?.models || []).map((m) => m.id)
    if (isEmbed) {
      embedModelOptions.value = ids
      if (!form.embed_model && ids.length) form.embed_model = ids[0]
    } else {
      llmModelOptions.value = ids
      if (!form.llm_model && ids.length) form.llm_model = ids[0]
    }
  } catch (e) {
    listError[kind] = errorText(e)
  } finally {
    fetching[kind] = false
  }
}

async function save() {
  saving.value = true
  saveError.value = ''
  saveOk.value = false
  const payload: Record<string, string> = {
    embed_base_url: form.embed_base_url.trim(),
    embed_model: form.embed_model.trim(),
    llm_base_url: form.llm_base_url.trim(),
    llm_model: form.llm_model.trim(),
  }
  // 只有用户实际输入了新的 key 才提交，避免用空值覆盖已保存的凭据。
  if (form.embed_api_key.trim()) payload.embed_api_key = form.embed_api_key.trim()
  if (form.llm_api_key.trim()) payload.llm_api_key = form.llm_api_key.trim()

  try {
    await axios.put('/api/v1/settings/models', payload, { timeout: 15000 })
    await load()
    saveOkText.value = '手工配置已保存。若已选择来源，来源仍然优先。'
    saveOk.value = true
  } catch (e) {
    saveError.value = errorText(e)
  } finally {
    saving.value = false
  }
}

async function test(kind: 'embed' | 'llm') {
  testing[kind] = true
  testError[kind] = ''
  const current = testResult.value || {}
  if (kind === 'embed') delete current.embedding
  else delete current.chat
  testResult.value = { ...current }
  try {
    const res = await axios.post<TestResult>('/api/v1/settings/models/test', { kind }, { timeout: 40000 })
    testResult.value = { ...(testResult.value || {}), ...res.data }
  } catch (e) {
    testError[kind] = errorText(e)
  } finally {
    testing[kind] = false
  }
}

async function testAll() {
  await Promise.all([test('llm'), test('embed')])
}

onMounted(load)
</script>

<style scoped>
.effective-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}
.effective-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 12px;
  flex-wrap: wrap;
}
.effective-label {
  width: 40px;
  font-size: 13px;
  color: var(--text-muted);
}
.effective-value {
  flex: 1;
  min-width: 0;
  font-size: 13px;
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.effective-tests {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  margin-top: 14px;
  padding-top: 12px;
  border-top: 1px solid var(--border, #eceff3);
}
.test-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
}
.provider-empty {
  padding: 16px;
  border: 1px dashed var(--border, #d0d7de);
  border-radius: 8px;
}
.provider-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 0;
  border-top: 1px solid var(--border, #eceff3);
}
.provider-info {
  min-width: 0;
  flex: 1;
}
.provider-name {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
  flex-wrap: wrap;
}
.provider-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 6px;
  font-size: 13px;
}
.nowrap {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 260px;
}
.provider-test {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
  font-size: 13px;
}
.provider-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
.manual-collapse {
  border: 1px solid var(--border, #eceff3);
  border-radius: 10px;
  padding: 4px 16px;
  background: var(--bg-subtle);
}
.manual-grid {
  display: grid;
  gap: 20px;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  padding-top: 8px;
}
.manual-title {
  font-size: 14px;
  font-weight: 600;
  margin-bottom: 8px;
}
.manual-actions {
  display: flex;
  justify-content: flex-end;
  padding: 4px 0 8px;
}
.dialog-models {
  display: grid;
  gap: 0 16px;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}
.model-row {
  display: flex;
  gap: 8px;
  align-items: center;
  width: 100%;
}
.err {
  color: var(--danger, #d9534f);
  font-size: 12px;
}
.footnote {
  font-size: 12px;
}

@media (max-width: 860px) {
  .provider-row {
    flex-direction: column;
    align-items: stretch;
  }
  .provider-actions {
    justify-content: flex-start;
  }
  .manual-grid,
  .dialog-models {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
