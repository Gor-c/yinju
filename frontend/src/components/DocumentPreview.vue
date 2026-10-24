<template>
  <div class="doc-preview">
    <div v-if="kind === 'unsupported'" class="preview-fallback">
      <p class="muted">
        {{ ext ? `暂不支持在线预览 ${ext} 文件` : '无法识别该文件的格式' }}，可下载原件后用本地软件打开。
      </p>
    </div>

    <div v-else-if="tooLarge" class="preview-fallback">
      <p class="muted">文件 {{ sizeMB }} MB，超过在线预览上限（{{ maxMB }} MB），请下载后用本地软件打开。</p>
      <a class="el-button el-button--small" :href="downloadURL" target="_blank" rel="noopener">下载原件</a>
    </div>

    <div v-else-if="loading" class="preview-status muted">正在加载预览…</div>

    <div v-else-if="error" class="preview-status">
      <div class="notice error">{{ error }}</div>
    </div>

    <template v-else-if="viewer">
      <div v-if="large" class="preview-notice muted">文件较大（{{ sizeMB }} MB），首次渲染可能需要几秒。</div>
      <div class="office-viewer">
        <component :is="viewer" :src="docData" @rendered="onRendered" @error="onRenderError" />
      </div>
    </template>

    <pre v-else-if="kind === 'text'" class="preview-plain">{{ text }}</pre>
    <div v-else class="preview-plain markdown-body" v-html="renderedText" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, shallowRef, watch } from 'vue'
import DOMPurify from 'dompurify'
import { marked } from 'marked'
// Styles for the Office renderers themselves.
import '@vue-office/docx/lib/index.css'
import '@vue-office/excel/lib/index.css'

const props = defineProps<{ src: string; filename: string }>()

type PreviewKind = 'pdf' | 'docx' | 'excel' | 'text' | 'markdown' | 'unsupported'

const ext = computed(() => {
  const dot = props.filename.lastIndexOf('.')
  return dot >= 0 ? props.filename.slice(dot).toLowerCase() : ''
})

const kind = computed<PreviewKind>(() => {
  switch (ext.value) {
    case '.pdf':
      return 'pdf'
    case '.docx':
      return 'docx'
    case '.xlsx':
    case '.xls':
    case '.csv':
      return 'excel'
    case '.md':
    case '.markdown':
      return 'markdown'
    case '.txt':
    case '.json':
    case '.html':
    case '.htm':
    case '':
      return 'text'
    default:
      return 'unsupported'
  }
})

// The Office renderers are heavy (pdf.js / SheetJS); only load the one needed.
const viewer = shallowRef<any>(null)
const docData = ref<ArrayBuffer | null>(null)
const text = ref('')
const loading = ref(true)
const error = ref('')
const sizeMB = ref('')
const large = ref(false)
const tooLarge = ref(false)

// Browser-side parsing is fine for ordinary documents but a huge file would
// freeze the tab, so oversized files fall back to a download.
const maxPreviewBytes = 20 * 1024 * 1024
const maxMB = maxPreviewBytes / 1024 / 1024
const warnBytes = 6 * 1024 * 1024

const downloadURL = computed(() => `${props.src}${props.src.includes('?') ? '&' : '?'}download=1`)

const renderedText = computed(() => DOMPurify.sanitize(marked.parse(text.value || '') as string))

async function load() {
  loading.value = true
  error.value = ''
  viewer.value = null
  docData.value = null
  text.value = ''
  large.value = false
  tooLarge.value = false
  sizeMB.value = ''
  if (kind.value === 'unsupported') {
    loading.value = false
    return
  }
  try {
    const res = await fetch(props.src)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    if (kind.value === 'text' || kind.value === 'markdown') {
      text.value = await res.text()
      return
    }
    const buffer = await res.arrayBuffer()
    sizeMB.value = (buffer.byteLength / 1024 / 1024).toFixed(1)
    if (buffer.byteLength > maxPreviewBytes) {
      tooLarge.value = true
      return
    }
    large.value = buffer.byteLength > warnBytes
    docData.value = buffer
    if (kind.value === 'pdf') {
      viewer.value = (await import('@vue-office/pdf')).default
    } else if (kind.value === 'docx') {
      viewer.value = (await import('@vue-office/docx')).default
    } else {
      viewer.value = (await import('@vue-office/excel')).default
    }
  } catch (e: any) {
    error.value = '预览失败：' + (e?.message || '无法读取源文件')
  } finally {
    loading.value = false
  }
}

function onRendered() {
  error.value = ''
}

function onRenderError(e: any) {
  error.value = '渲染失败：' + (e?.message || '文件可能损坏或格式不受支持')
}

onMounted(load)
watch(() => [props.src, props.filename], load)
</script>

<style scoped>
.doc-preview {
  min-height: 320px;
}

.preview-status,
.preview-fallback {
  padding: 32px 16px;
  text-align: center;
}

.preview-notice {
  margin-bottom: 8px;
  font-size: calc(12px * var(--fs-scale));
}

/* Office/PDF documents are authored on a white page; keep it white in dark mode. */
.office-viewer {
  min-height: 60vh;
  max-height: 68vh;
  overflow: auto;
  background: #fff;
  border-radius: 10px;
  border: 1px solid var(--border);
}

.preview-plain {
  margin: 0;
  padding: 18px 20px;
  white-space: pre-wrap;
  word-break: break-word;
  font-size: calc(13px * var(--fs-scale));
  line-height: 1.75;
  font-family: inherit;
}

.markdown-body {
  font-size: calc(14px * var(--fs-scale));
  white-space: normal;
}
</style>
