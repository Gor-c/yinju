<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import axios from 'axios'
import { ElMessage, ElMessageBox } from 'element-plus'

type Source = 'instance' | 'env_file' | 'default' | 'external'

interface Snapshot {
  config_file: string
  env_file: string
  saved: boolean
  saved_at?: string
  has_backup: boolean
  database: {
    host: string
    port: string
    database: string
    user: string
    sslmode: string
    password_set: boolean
    dsn_masked: string
  }
  vector: { store: string; url: string; tenant: string; database: string; collection: string }
  runtime: { listen_addr: string; upload_dir: string; brand_dir: string }
  sources: Record<string, Source>
  pending: string[] | null
  locked: string[] | null
}

const snapshot = ref<Snapshot | null>(null)
const loading = ref(false)
const saving = ref(false)
const testingDB = ref(false)
const testingVector = ref(false)
const dbResult = ref<{ ok: boolean; text: string } | null>(null)
const vectorResult = ref<{ ok: boolean; text: string } | null>(null)

const form = reactive({
  host: '',
  port: '',
  database: '',
  user: '',
  password: '',
  sslmode: 'disable',
  vector_store: 'pgvector',
  chroma_url: '',
  chroma_tenant: '',
  chroma_database: '',
  chroma_collection: '',
  listen_addr: '',
  upload_dir: '',
  brand_dir: '',
})

const pending = computed(() => snapshot.value?.pending || [])
const locked = computed(() => snapshot.value?.locked || [])
// PG_DSN 被外部注入时，主机/端口/库名/用户/密码整组都改不动，
// 单独禁用某个字段会让人以为是那一项有问题。
const dbLocked = computed(() => locked.value.includes('PG_DSN'))

// 每个字段的来源徽章。用户看这个页面时第一个问题就是
// 「这个值是我在页面上改的，还是 .env 里写死的」，不标出来就会反复怀疑改动没生效。
const sourceMeta: Record<Source, { label: string; tone: string; hint: string }> = {
  instance: { label: '已保存', tone: 'ok', hint: '来自本页面保存的配置文件，重启后生效' },
  env_file: { label: '.env', tone: 'plain', hint: '来自 .env，页面保存后会覆盖它' },
  default: { label: '默认', tone: 'plain', hint: '代码里的默认值，未做任何配置' },
  external: { label: '环境变量', tone: 'warn', hint: '由进程环境变量注入，页面修改不会生效' },
}

function sourceOf(key: string) {
  const tone = snapshot.value?.sources?.[key] || 'default'
  return sourceMeta[tone]
}

function fillForm(s: Snapshot) {
  form.host = s.database.host
  form.port = s.database.port
  form.database = s.database.database
  form.user = s.database.user
  form.password = ''
  form.sslmode = s.database.sslmode || 'disable'
  form.vector_store = s.vector.store
  form.chroma_url = s.vector.url
  form.chroma_tenant = s.vector.tenant
  form.chroma_database = s.vector.database
  form.chroma_collection = s.vector.collection
  form.listen_addr = s.runtime.listen_addr
  form.upload_dir = s.runtime.upload_dir
  form.brand_dir = s.runtime.brand_dir
}

async function load() {
  loading.value = true
  try {
    const { data } = await axios.get('/api/v1/instance/config')
    snapshot.value = data
    fillForm(data)
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '读取实例配置失败')
  } finally {
    loading.value = false
  }
}

async function testDB() {
  testingDB.value = true
  dbResult.value = null
  try {
    const { data } = await axios.post('/api/v1/instance/config/test-db', {
      host: form.host,
      port: form.port,
      database: form.database,
      user: form.user,
      password: form.password,
      sslmode: form.sslmode,
    })
    dbResult.value = data.ok
      ? { ok: true, text: `连接成功，${data.elapsed_ms} ms` }
      : { ok: false, text: data.error }
  } catch (e: any) {
    dbResult.value = { ok: false, text: e.response?.data?.error || '测试请求失败' }
  } finally {
    testingDB.value = false
  }
}

async function testVector() {
  testingVector.value = true
  vectorResult.value = null
  try {
    const { data } = await axios.post('/api/v1/instance/config/test-vector', {
      store: form.vector_store,
      url: form.chroma_url,
      password: form.password,
      database: {
        host: form.host,
        port: form.port,
        database: form.database,
        user: form.user,
        sslmode: form.sslmode,
      },
    })
    vectorResult.value = data.ok
      ? { ok: true, text: data.detail || `连通，${data.elapsed_ms} ms` }
      : { ok: false, text: data.error }
  } catch (e: any) {
    vectorResult.value = { ok: false, text: e.response?.data?.error || '测试请求失败' }
  } finally {
    testingVector.value = false
  }
}

async function save(force = false) {
  saving.value = true
  try {
    const { data } = await axios.put('/api/v1/instance/config', { ...form, force })
    snapshot.value = data.snapshot
    fillForm(data.snapshot)
    dbResult.value = null
    vectorResult.value = null
    ElMessage.success('已保存到配置文件，重启服务后生效')
  } catch (e: any) {
    const err = e.response?.data
    if (e.response?.status === 400 && err?.can_force) {
      try {
        await ElMessageBox.confirm(
          `${err.error}\n\n仍然保存吗？如果新配置不可用，重启后服务将连不上数据库。` +
            '本页面不依赖数据库，届时仍可打开并点「回滚上一版」。',
          '连接测试未通过',
          { confirmButtonText: '仍然保存', cancelButtonText: '返回修改', type: 'warning' },
        )
      } catch {
        saving.value = false
        return
      }
      saving.value = false
      await save(true)
      return
    }
    ElMessage.error(err?.error || '保存失败')
  } finally {
    saving.value = false
  }
}

async function rollback() {
  try {
    await ElMessageBox.confirm(
      '用上一版配置文件覆盖当前内容。回滚同样需要重启才生效。',
      '回滚上一版',
      { confirmButtonText: '回滚', cancelButtonText: '取消', type: 'warning' },
    )
  } catch {
    return
  }
  try {
    const { data } = await axios.post('/api/v1/instance/config/rollback')
    snapshot.value = data.snapshot
    fillForm(data.snapshot)
    ElMessage.success('已回滚，重启服务后生效')
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '回滚失败')
  }
}

onMounted(load)
</script>

<template>
  <div class="settings-section">
    <header class="page-head">
      <div>
        <div class="eyebrow">Instance</div>
        <h1>实例配置</h1>
        <p class="subtitle">
          数据库、向量库与运行目录。这些值决定进程如何启动，保存后需要重启服务才生效。
        </p>
      </div>
      <div class="head-actions">
        <el-button :loading="loading" @click="load">重新读取</el-button>
        <el-button v-if="snapshot?.has_backup" @click="rollback">回滚上一版</el-button>
        <el-button type="primary" :loading="saving" @click="save()">保存</el-button>
      </div>
    </header>

    <div class="inst-banner">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round">
        <path d="M12 8v5m0 3h.01" />
        <circle cx="12" cy="12" r="9" />
      </svg>
      <div>
        <b>改错会导致服务起不来，但这个页面永远能打开</b>
        <p>
          配置写入 <code>{{ snapshot?.config_file || 'data/instance.env' }}</code>，优先级高于
          <code>{{ snapshot?.env_file || '.env' }}</code>。保存前会先实测一次数据库连接，连不上就不写入；
          真写坏了，重启后仍可回到这里点「回滚上一版」。
        </p>
      </div>
    </div>

    <div v-if="pending.length" class="notice">
      有 {{ pending.length }} 项已保存但当前进程还没用上，重启后生效：
      <b>{{ pending.join('、') }}</b>
    </div>
    <div v-if="locked.length" class="notice error">
      以下配置由进程环境变量注入，在本页面修改不会生效，需要改部署配置（systemd / 容器）：
      <b>{{ locked.join('、') }}</b>
    </div>

    <!-- 数据库 -->
    <section class="panel inst-panel">
      <div class="inst-head">
        <b>数据库</b>
        <span class="muted">PostgreSQL。知识库数据、检索参数、会话记忆都在这里</span>
        <span class="inst-source" :class="sourceOf('PG_DSN').tone" :title="sourceOf('PG_DSN').hint">
          {{ sourceOf('PG_DSN').label }}
        </span>
      </div>

      <div class="inst-grid">
        <label class="inst-field">
          <span>主机</span>
          <el-input v-model="form.host" placeholder="127.0.0.1" :disabled="dbLocked" />
        </label>
        <label class="inst-field">
          <span>端口</span>
          <el-input v-model="form.port" :disabled="dbLocked" placeholder="5432" />
        </label>
        <label class="inst-field">
          <span>数据库名</span>
          <el-input v-model="form.database" :disabled="dbLocked" placeholder="knowledge_base" />
        </label>
        <label class="inst-field">
          <span>用户名</span>
          <el-input v-model="form.user" :disabled="dbLocked" placeholder="postgres" />
        </label>
        <label class="inst-field">
          <span>密码</span>
          <el-input
            v-model="form.password"
            type="password"
            show-password
            :disabled="dbLocked"
            :placeholder="snapshot?.database.password_set ? '已设置，留空表示不修改' : '未设置'"
          />
        </label>
        <label class="inst-field">
          <span>SSL 模式</span>
          <el-select v-model="form.sslmode" :disabled="dbLocked" style="width: 100%">
            <el-option label="disable（本机/内网）" value="disable" />
            <el-option label="prefer" value="prefer" />
            <el-option label="require" value="require" />
            <el-option label="verify-full" value="verify-full" />
          </el-select>
        </label>
      </div>

      <div class="inst-dsn">
        当前生效：<code>{{ snapshot?.database.dsn_masked || '未配置' }}</code>
      </div>

      <div class="inst-actions">
        <el-button size="small" :loading="testingDB" @click="testDB">测试连接</el-button>
        <span v-if="dbResult" class="inst-result" :class="dbResult.ok ? 'ok' : 'bad'">
          {{ dbResult.text }}
        </span>
      </div>
    </section>

    <!-- 向量库 -->
    <section class="panel inst-panel">
      <div class="inst-head">
        <b>向量库</b>
        <span class="muted">存放片段向量。pgvector 复用上面的数据库连接</span>
        <span class="inst-source" :class="sourceOf('VECTOR_STORE').tone" :title="sourceOf('VECTOR_STORE').hint">
          {{ sourceOf('VECTOR_STORE').label }}
        </span>
      </div>

      <div class="inst-grid">
        <label class="inst-field">
          <span>类型</span>
          <el-select v-model="form.vector_store" style="width: 100%">
            <el-option label="Chroma（独立服务）" value="chroma" />
            <el-option label="pgvector（复用数据库）" value="pgvector" />
          </el-select>
        </label>
        <label v-if="form.vector_store === 'chroma'" class="inst-field">
          <span>Chroma 地址</span>
          <el-input v-model="form.chroma_url" placeholder="http://127.0.0.1:8000" />
        </label>
        <template v-if="form.vector_store === 'chroma'">
          <label class="inst-field">
            <span>集合（collection）</span>
            <el-input v-model="form.chroma_collection" placeholder="knowledge_base" />
          </label>
          <label class="inst-field">
            <span>租户</span>
            <el-input v-model="form.chroma_tenant" placeholder="default_tenant" />
          </label>
          <label class="inst-field">
            <span>数据库</span>
            <el-input v-model="form.chroma_database" placeholder="default_database" />
          </label>
        </template>
      </div>

      <p v-if="form.vector_store === 'pgvector'" class="inst-hint">
        切到 pgvector 前请先执行 <code>deploy/schema-pgvector.sql</code> 建表，否则检索会报错。
        从 Chroma 切过来时向量需要重建，页面上的「重新提取」不够——要跑一次 <code>reindex</code>。
      </p>

      <div class="inst-actions">
        <el-button size="small" :loading="testingVector" @click="testVector">测试连通</el-button>
        <span v-if="vectorResult" class="inst-result" :class="vectorResult.ok ? 'ok' : 'bad'">
          {{ vectorResult.text }}
        </span>
      </div>
    </section>

    <!-- 运行目录 -->
    <section class="panel inst-panel">
      <div class="inst-head">
        <b>运行</b>
        <span class="muted">监听地址与数据目录。容器部署时通常挂载这些路径</span>
        <span class="inst-source" :class="sourceOf('LISTEN_ADDR').tone" :title="sourceOf('LISTEN_ADDR').hint">
          {{ sourceOf('LISTEN_ADDR').label }}
        </span>
      </div>

      <div class="inst-grid">
        <label class="inst-field">
          <span>监听地址</span>
          <el-input v-model="form.listen_addr" placeholder="127.0.0.1:8081" />
          <small>改端口后记得同步防火墙与反向代理配置。</small>
        </label>
        <label class="inst-field">
          <span class="inst-label-row">上传目录<i class="inst-mini" :class="sourceOf('UPLOAD_DIR').tone">{{ sourceOf('UPLOAD_DIR').label }}</i></span>
          <el-input v-model="form.upload_dir" placeholder="./data/uploads" />
          <small>原文件与解析产物。多实例部署需共享。</small>
        </label>
        <label class="inst-field">
          <span class="inst-label-row">品牌目录<i class="inst-mini" :class="sourceOf('BRAND_DIR').tone">{{ sourceOf('BRAND_DIR').label }}</i></span>
          <el-input v-model="form.brand_dir" placeholder="./data/brand" />
          <small>自定义图标存放处，与上传目录一样需要共享。</small>
        </label>
      </div>
    </section>

    <p class="inst-foot">
      这些配置存在数据库之外——正因为如此，把数据库地址改错了，也还能回到这个页面改回来。
      代价是它们不能像检索参数那样热更新，必须重启进程。
    </p>
  </div>
</template>

<style scoped>
.inst-banner {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  padding: 12px 14px;
  margin-bottom: 14px;
  border: 1px solid var(--border-strong);
  border-radius: 10px;
  background: var(--bg-subtle);
}

.inst-banner svg {
  width: 17px;
  height: 17px;
  flex: 0 0 auto;
  margin-top: 1px;
  color: var(--text-secondary);
}

.inst-banner b {
  display: block;
  font-size: calc(12.5px * var(--fs-scale));
  margin-bottom: 3px;
}

.inst-banner p {
  margin: 0;
  font-size: calc(11.5px * var(--fs-scale));
  line-height: 1.75;
  color: var(--text-muted);
}

.inst-banner code,
.inst-dsn code,
.inst-hint code,
.inst-foot code {
  padding: 1px 5px;
  border-radius: 4px;
  background: var(--bg-hover);
  font-size: calc(11px * var(--fs-scale));
}

.panel {
  padding: 14px 16px 16px;
  margin-bottom: 14px;
}

.inst-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin-bottom: 12px;
}

.inst-head b {
  font-size: calc(12.5px * var(--fs-scale));
  font-weight: 600;
}

.inst-head .muted {
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.inst-source {
  margin-left: auto;
  padding: 1px 7px;
  border-radius: 999px;
  font-size: calc(10px * var(--fs-scale));
  border: 1px solid var(--border);
  color: var(--text-muted);
  cursor: help;
}

.inst-label-row {
  display: flex;
  align-items: center;
  gap: 7px;
}

.inst-mini {
  padding: 0 5px;
  border-radius: 999px;
  border: 1px solid var(--border);
  font-size: calc(9.5px * var(--fs-scale));
  font-style: normal;
  line-height: 1.6;
  color: var(--text-muted);
  cursor: help;
}

.inst-mini.ok {
  border-color: var(--accent);
  color: var(--accent);
}

.inst-mini.warn {
  border-color: #b45309;
  color: #b45309;
}

.inst-source.ok {
  border-color: var(--accent);
  color: var(--accent);
}

.inst-source.warn {
  border-color: #b45309;
  color: #b45309;
}

.inst-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 16px;
}

@media (max-width: 900px) {
  .inst-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}

.inst-field {
  display: flex;
  flex-direction: column;
  gap: 5px;
  min-width: 0;
}

.inst-field > span {
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-secondary);
}

.inst-field small {
  font-size: calc(11px * var(--fs-scale));
  line-height: 1.6;
  color: var(--text-muted);
}

.inst-dsn {
  margin-top: 12px;
  font-size: calc(11.5px * var(--fs-scale));
  color: var(--text-muted);
}

.inst-hint {
  margin: 12px 0 0;
  font-size: calc(11.5px * var(--fs-scale));
  line-height: 1.75;
  color: var(--text-muted);
}

.inst-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 12px;
}

.inst-result {
  font-size: calc(11.5px * var(--fs-scale));
  line-height: 1.6;
}

.inst-result.ok {
  color: var(--accent);
}

.inst-result.bad {
  color: var(--danger);
}

.inst-foot {
  margin: 4px 0 0;
  font-size: calc(11.5px * var(--fs-scale));
  line-height: 1.8;
  color: var(--text-muted);
}
</style>
