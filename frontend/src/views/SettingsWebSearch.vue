<template>
  <section class="settings-section">
    <header class="page-head">
      <div>
        <div class="eyebrow">Web Search</div>
        <h1>联网搜索设置</h1>
        <p class="subtitle">
          为 Agent 提供 <code>web_search</code> 工具和网页入库能力；默认关闭，开启后仍需用户在对话中勾选才会调用
        </p>
      </div>
      <div class="head-actions">
        <el-button :loading="loading" @click="load">重新读取</el-button>
        <el-button type="primary" :loading="saving" :disabled="!loaded" @click="save">保存配置</el-button>
      </div>
    </header>

    <div v-if="error" class="notice error">{{ error }}</div>
    <div v-else-if="notice" class="notice">{{ notice }}</div>

    <el-skeleton v-if="!loaded" :rows="6" animated />

    <div v-else class="settings-content">
      <el-tabs v-model="settingsTab" class="settings-provider-tabs">
        <el-tab-pane label="搜索服务" name="service">
          <div class="card settings-form-card">
            <div class="card-title">搜索服务</div>
            <p class="muted" style="margin-top: -6px">
              配置保存在数据库（<code>web_search_settings</code>），优先于 <code>WEB_SEARCH_*</code> 环境变量，保存后立即生效。
            </p>
            <el-form label-position="top">
              <el-form-item label="启用">
                <el-switch v-model="web.enabled" />
                <span class="muted" style="margin-left: 8px">
                  {{ web.active ? '已生效' : web.enabled ? '配置不完整，尚未生效' : '未启用' }}
                </span>
              </el-form-item>
              <el-form-item label="搜索服务">
                <el-select v-model="web.provider" placeholder="选择 provider" style="width: 240px">
                  <el-option label="Tavily" value="tavily" />
                  <el-option label="博查 Bocha" value="bocha" />
                  <el-option label="Brave Search" value="brave" />
                </el-select>
              </el-form-item>
              <el-form-item label="API Key">
                <el-input
                  v-model="web.api_key"
                  type="password"
                  show-password
                  clearable
                  :placeholder="web.api_key_configured ? `已配置 ${web.api_key_masked}，留空不修改` : '填写搜索服务 API Key'"
                />
              </el-form-item>
              <el-form-item label="Base URL（自建或代理，可留空）">
                <el-input v-model="web.base_url" placeholder="https://api.tavily.com" clearable />
              </el-form-item>
            </el-form>

            <div class="test-row">
              <el-button :loading="testing" @click="test">测试搜索</el-button>
              <el-button v-if="web.api_key_configured" link type="danger" @click="clearKey">清除 Key</el-button>
            </div>
            <div v-if="testResult" class="test-result">
              <el-tag size="small" :type="testResult.ok ? 'success' : 'danger'" effect="light">
                {{ testResult.ok ? '搜索正常' : '搜索失败' }}
              </el-tag>
              <span v-if="testResult.ok" class="muted">{{ testResult.provider }} · {{ testResult.count }} 条结果</span>
              <span v-else class="muted">{{ testResult.error }}</span>
            </div>
          </div>

        </el-tab-pane>

        <el-tab-pane label="范围与限制" name="scope">
          <div class="card settings-form-card">
            <div class="card-title">范围与限制</div>
            <el-form label-position="top">
              <el-form-item label="站点白名单（逗号分隔，留空不限站点）">
                <el-input v-model="web.allow_domains" placeholder="gov.cn,mem.gov.cn" clearable />
              </el-form-item>
              <el-form-item label="站点黑名单（逗号分隔）">
                <el-input v-model="web.deny_domains" placeholder="spam.example.com" clearable />
              </el-form-item>
              <el-form-item label="超时（毫秒）">
                <el-input-number v-model="web.timeout_ms" :min="1000" :max="60000" :step="1000" />
              </el-form-item>
              <el-form-item label="结果缓存">
                <el-input-number v-model="web.cache_ttl_seconds" :min="0" :max="86400" :step="60" />
                <span class="muted" style="margin-left: 8px">秒</span>
                <el-input-number v-model="web.cache_entries" :min="1" :max="5000" :step="50" style="margin-left: 12px" />
                <span class="muted" style="margin-left: 8px">条</span>
              </el-form-item>
            </el-form>

          </div>
        </el-tab-pane>
      </el-tabs>

      <div class="card settings-info-card">
        <div class="card-title">使用说明</div>
        <ul class="muted" style="margin: 0; padding-left: 18px; line-height: 1.9">
          <li>开启后 Agent 会多出 <code>web_search</code> 工具，但<b>本轮对话必须勾选「联网搜索」</b>才会真正调用。</li>
          <li>搜索关键词会离开本机，仅返回标题、摘要和链接；网页中的指令不会被执行。</li>
          <li>对话里的网页引用可「保存到知识库」：服务端抓取正文后走与上传相同的索引队列。</li>
          <li>抓取有 SSRF 防护：拒绝内网、回环、链路本地地址，重定向逐跳复检，正文上限 4MB。</li>
          <li>API Key 仅以掩码回显，保存后不再返回明文。</li>
          <li v-if="web.updated_at">最近保存：{{ new Date(web.updated_at).toLocaleString() }}（来源：{{ web.source === 'database' ? '数据库' : '环境变量' }}）</li>
        </ul>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import axios from 'axios'

type WebSearchSettings = {
  enabled: boolean
  provider: string
  api_key_configured: boolean
  api_key_masked: string
  base_url: string
  timeout_ms: number
  cache_ttl_seconds: number
  cache_entries: number
  allow_domains: string
  deny_domains: string
  auto_import: boolean
  active: boolean
  source: string
  updated_at?: string | null
}

type TestPayload = { ok: boolean; count: number; provider: string; error?: string }

const web = reactive<WebSearchSettings & { api_key: string }>({
  enabled: false,
  provider: 'tavily',
  api_key: '',
  api_key_configured: false,
  api_key_masked: '',
  base_url: '',
  timeout_ms: 15000,
  cache_ttl_seconds: 900,
  cache_entries: 200,
  allow_domains: '',
  deny_domains: '',
  auto_import: false,
  active: false,
  source: 'env',
})
const loaded = ref(false)
const loading = ref(false)
const saving = ref(false)
const testing = ref(false)
const error = ref('')
const notice = ref('')
const testResult = ref<TestPayload | null>(null)
const settingsTab = ref('service')

function errorText(e: unknown) {
  const err = e as { response?: { data?: { error?: string; started_at?: string }; status?: number }; message?: string }
  const detail = err.response?.data?.error
  if (err.response?.status === 404) {
    const started = err.response?.data?.started_at
    return `服务端没有这个接口，后端可能还是旧版本${started ? `（该进程启动于 ${started}）` : ''}：请用最新代码重新构建并重启后端。`
  }
  if (detail) return detail
  if (err.response?.status === 503) return '服务端未连接数据库，无法保存配置'
  return err.message || '请求失败'
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await axios.get<WebSearchSettings>('/api/v1/settings/web-search', { timeout: 10000 })
    Object.assign(web, res.data, { api_key: '' })
    loaded.value = true
  } catch (e) {
    error.value = errorText(e)
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  error.value = ''
  notice.value = ''
  const payload: Record<string, unknown> = {
    enabled: web.enabled,
    provider: web.provider,
    base_url: web.base_url.trim(),
    timeout_ms: web.timeout_ms,
    cache_ttl_seconds: web.cache_ttl_seconds,
    cache_entries: web.cache_entries,
    allow_domains: web.allow_domains.trim(),
    deny_domains: web.deny_domains.trim(),
    auto_import: web.auto_import,
  }
  // 只有实际输入了新 Key 才提交，避免用空值覆盖已保存的凭据。
  if (web.api_key.trim()) payload.api_key = web.api_key.trim()
  try {
    const res = await axios.put<WebSearchSettings>('/api/v1/settings/web-search', payload, { timeout: 15000 })
    Object.assign(web, res.data, { api_key: '' })
    notice.value = '配置已保存并生效。'
  } catch (e) {
    error.value = errorText(e)
  } finally {
    saving.value = false
  }
}

async function clearKey() {
  if (!window.confirm('确定清除已保存的搜索 API Key？清除后联网搜索将不可用。')) return
  try {
    const res = await axios.put<WebSearchSettings>('/api/v1/settings/web-search', { api_key: '' }, { timeout: 15000 })
    Object.assign(web, res.data, { api_key: '' })
    notice.value = '已清除 API Key。'
  } catch (e) {
    error.value = errorText(e)
  }
}

async function test() {
  testing.value = true
  testResult.value = null
  try {
    const res = await axios.post<TestPayload>(
      '/api/v1/settings/web-search/test',
      { query: '消防' },
      { timeout: 30000 },
    )
    testResult.value = res.data
  } catch (e) {
    testResult.value = { ok: false, count: 0, provider: web.provider, error: errorText(e) }
  } finally {
    testing.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.test-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 4px;
}

.test-result {
  margin-top: 12px;
  display: flex;
  align-items: center;
  gap: 8px;
}
</style>
