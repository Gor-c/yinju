<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import axios from 'axios'
import { ElMessage } from 'element-plus'
import BrandMark from '../components/BrandMark.vue'
import { applyBrand, brand, loadBrand } from '../brand'
import {
  applyAppearance,
  FONT_PRESETS,
  FONT_SIZE_PRESETS,
  RADIUS_PRESETS,
  THEME_PRESETS,
  findFont,
  findFontSize,
  themeSwatch,
} from '../appearance'

const form = reactive({
  app_name: '',
  app_subtitle: '',
  browser_title: '',
  theme: '',
  radius: '',
  font_size: '',
  font_family: '',
})
const loading = ref(false)
const saving = ref(false)
const uploading = ref(false)
const fileInput = ref<HTMLInputElement>()

const isCustom = computed(() => brand.value.logo_source === 'upload' && !!brand.value.logo_url)

// 预览读 form 而不是已保存值：改名字时不必先保存就能看到结果，
// 这正是「可随时改」该有的手感。
const previewName = computed(() => form.app_name.trim() || '未命名')
const previewSub = computed(() => form.app_subtitle.trim())
const previewTitle = computed(() => form.browser_title.trim() || form.app_name.trim() || '未命名')

function fillForm() {
  form.app_name = brand.value.app_name
  form.app_subtitle = brand.value.app_subtitle
  form.browser_title = brand.value.browser_title
  form.theme = brand.value.theme
  form.radius = brand.value.radius
  form.font_size = brand.value.font_size
  form.font_family = brand.value.font_family
}

// 预览用的字体与字号：直接取当前 form，改一下就能看到效果。
const previewStyle = computed(() => ({
  fontFamily: findFont(form.font_family).stack,
  fontSize: `${14 * findFontSize(form.font_size).scale}px`,
}))

// 主题与圆角点一下立刻应用到整站，但只有保存才落库。
// 离开页面时恢复成已保存的值，避免「看着变了、其实没存」。
const previewing = computed(
  () =>
    form.theme !== brand.value.theme ||
    form.radius !== brand.value.radius ||
    form.font_size !== brand.value.font_size ||
    form.font_family !== brand.value.font_family,
)

function dotStyle(id: string) {
  const sw = themeSwatch(id)
  return { background: sw.accent, boxShadow: `0 0 0 3px ${sw.soft}` }
}

// 四项外观共用一次应用，避免某个入口漏传参数导致「改了这项、那项被重置」。
function applyCurrent() {
  applyAppearance(form.theme, form.radius, form.font_size, form.font_family)
}

function pickTheme(id: string) {
  form.theme = id
  applyCurrent()
}

function pickRadius(id: string) {
  form.radius = id
  applyCurrent()
}

function pickFontSize(id: string) {
  form.font_size = id
  applyCurrent()
}

function pickFont(id: string) {
  form.font_family = id
  applyCurrent()
}

function revertPreview() {
  applyAppearance(brand.value.theme, brand.value.radius, brand.value.font_size, brand.value.font_family)
}

onUnmounted(revertPreview)

async function load() {
  loading.value = true
  try {
    await loadBrand()
    fillForm()
  } finally {
    loading.value = false
  }
}

function revertAll() {
  fillForm()
  revertPreview()
}

async function save() {
  if (!form.app_name.trim()) {
    ElMessage.warning('项目名称不能为空')
    return
  }
  saving.value = true
  try {
    const { data } = await axios.put('/api/v1/brand', {
      app_name: form.app_name.trim(),
      app_subtitle: form.app_subtitle.trim(),
      browser_title: form.browser_title.trim(),
      theme: form.theme,
      radius: form.radius,
      font_size: form.font_size,
      font_family: form.font_family,
    })
    applyBrand(data)
    fillForm()
    ElMessage.success('已保存，名称、配色与圆角已生效')
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '保存失败')
  } finally {
    saving.value = false
  }
}

function pickLogo() {
  fileInput.value?.click()
}

async function onFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return

  // 前端先挡一道，省得传完才被后端拒；后端仍会按魔数再验一次。
  if (file.size > 1 << 20) {
    ElMessage.error('图片超过 1 MB，请先压缩')
    input.value = ''
    return
  }
  if (!['image/png', 'image/jpeg', 'image/webp'].includes(file.type)) {
    ElMessage.error('只支持 PNG、JPEG、WebP')
    input.value = ''
    return
  }

  uploading.value = true
  try {
    const body = new FormData()
    body.append('file', file)
    const { data } = await axios.post('/api/v1/brand/logo', body)
    applyBrand(data)
    ElMessage.success('图标已更新')
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '上传失败')
  } finally {
    uploading.value = false
    input.value = ''
  }
}

async function restoreBuiltin() {
  uploading.value = true
  try {
    const { data } = await axios.put('/api/v1/brand', { logo_source: 'builtin' })
    applyBrand(data)
    ElMessage.success('已恢复默认标记')
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '恢复失败')
  } finally {
    uploading.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="settings-section">
    <header class="page-head">
      <div>
        <div class="eyebrow">Appearance</div>
        <h1>系统外观</h1>
        <p class="subtitle">
          项目名称、副标题与图标。名称保存后生效，图标上传即生效；两者都不需要重启或重新打包。
        </p>
      </div>
      <div class="head-actions">
        <el-button :loading="loading" @click="load">刷新</el-button>
        <el-button @click="revertAll">撤销改动</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </div>
    </header>

    <div class="brg-layout">
      <div class="brg-form">
        <section class="panel brg-panel">
          <div class="brg-head">
            <b>配色主题</b>
            <span class="muted">点击即时预览，保存后对所有人生效</span>
            <span v-if="previewing" class="brg-preview-tag">预览中 · 未保存</span>
          </div>

          <div class="brg-themes">
            <button
              v-for="t in THEME_PRESETS"
              :key="t.id"
              type="button"
              class="brg-theme"
              :class="{ on: form.theme === t.id }"
              @click="pickTheme(t.id)"
            >
              <i class="brg-dot" :style="dotStyle(t.id)"></i>
              <span class="brg-theme-copy">
                <b>{{ t.name }}</b>
                <small>{{ t.hint }}</small>
              </span>
              <svg v-if="form.theme === t.id" class="brg-check" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round">
                <path d="m5 12 5 5L20 7" />
              </svg>
            </button>
          </div>

          <div class="brg-radius-row">
            <span class="brg-radius-label">圆角</span>
            <button
              v-for="r in RADIUS_PRESETS"
              :key="r.id"
              type="button"
              class="brg-radius"
              :class="{ on: form.radius === r.id }"
              @click="pickRadius(r.id)"
            >
              <i :style="{ borderRadius: r.base + 'px' }"></i>
              <span>{{ r.name }}</span>
            </button>
            <span class="brg-radius-hint">影响卡片、输入框与按钮的圆角半径</span>
          </div>
        </section>

        <section class="panel brg-panel">
          <div class="brg-head">
            <b>文字</b>
            <span class="muted">字号只放大文字，不改布局间距</span>
          </div>

          <div class="brg-fs">
            <span class="brg-fs-label">字号</span>
            <button
              v-for="(sz, i) in FONT_SIZE_PRESETS"
              :key="sz.id"
              type="button"
              class="brg-fs-btn"
              :class="{ on: form.font_size === sz.id }"
              @click="pickFontSize(sz.id)"
            >
              <i :style="{ fontSize: `${11 + i * 2.4}px` }">文</i>
              <span>{{ sz.name }}</span>
            </button>
            <span class="brg-note-inline">标准档与出厂设置完全一致</span>
          </div>

          <div class="brg-fonts">
            <button
              v-for="f in FONT_PRESETS"
              :key="f.id"
              type="button"
              class="brg-font"
              :class="{ on: form.font_family === f.id }"
              :style="{ fontFamily: f.stack }"
              @click="pickFont(f.id)"
            >
              <b>{{ f.name }}</b>
              <span class="brg-font-sample">Aa 123 知识</span>
              <small>{{ f.hint }}</small>
            </button>
          </div>

          <div class="brg-typo-preview">
            <span class="brg-typo-tag">预览</span>
            <p :style="previewStyle">
              光缆敷设时牵引力不得超过允许值的 80%，弯曲半径不小于外径的 20 倍。
            </p>
          </div>
        </section>

        <section class="panel brg-panel">
          <div class="brg-head">
            <b>名称</b>
            <span class="muted">侧栏与浏览器标签页读取这里</span>
          </div>
          <div class="brg-fields">
            <label class="brg-field">
              <span class="brg-label">项目名称</span>
              <el-input v-model="form.app_name" maxlength="40" show-word-limit placeholder="知识库管理" />
              <small>侧栏主标题；浏览器标题留空时也用它。</small>
            </label>
            <label class="brg-field">
              <span class="brg-label">副标题</span>
              <el-input v-model="form.app_subtitle" maxlength="60" show-word-limit placeholder="智能业务平台" />
              <small>名称下方的小字，可以留空。</small>
            </label>
            <label class="brg-field">
              <span class="brg-label">浏览器标题</span>
              <el-input v-model="form.browser_title" maxlength="80" show-word-limit placeholder="留空则使用项目名称" />
              <small>标签页文字，与侧栏解耦，便于标出环境（如「知识库 · 测试」）。</small>
            </label>
          </div>
        </section>

        <section class="panel brg-panel">
          <div class="brg-head">
            <b>图标</b>
            <span class="muted">PNG / JPEG / WebP，不超过 1 MB</span>
          </div>
          <div class="brg-logo">
            <div class="brg-logo-box">
              <BrandMark :size="56" />
            </div>
            <div class="brg-logo-actions">
              <div class="brg-logo-buttons">
                <el-button size="small" :loading="uploading" @click="pickLogo">上传图片</el-button>
                <el-button v-if="isCustom" size="small" :disabled="uploading" @click="restoreBuiltin">
                  恢复默认
                </el-button>
              </div>
              <p class="brg-hint">
                建议正方形、至少 64×64，图形尽量铺满画面——侧栏里只有 32px，
                图自带留白会让标记看起来比周围小一圈。上传后立刻生效，不需要点保存。
              </p>
            </div>
          </div>
          <input
            ref="fileInput"
            type="file"
            accept="image/png,image/jpeg,image/webp"
            hidden
            @change="onFile"
          />
        </section>

        <p class="brg-note">
          不支持上传 SVG：SVG 可以内嵌脚本，而这个地址是能被直接打开的，
          允许上传等于开放一个同源脚本注入点。需要矢量标记请改代码里的内置版本。
        </p>
      </div>

      <aside class="brg-preview">
        <div class="brg-preview-label">实时预览</div>

        <div class="brg-mock">
          <div class="brg-mock-brand">
            <BrandMark :size="30" />
            <div class="brg-mock-copy">
              <b>{{ previewName }}</b>
              <small v-if="previewSub">{{ previewSub }}</small>
            </div>
          </div>
          <div class="brg-mock-nav">
            <span class="on">Agent 对话</span>
            <span>上传文档</span>
            <span>知识库</span>
            <span>文档库</span>
          </div>
        </div>

        <div class="brg-tab">
          <span class="brg-tab-dot"></span>
          <span class="brg-tab-favicon"><BrandMark :size="14" /></span>
          <span class="brg-tab-title">{{ previewTitle }}</span>
          <span class="brg-tab-x">×</span>
        </div>

        <p class="brg-preview-hint">
          预览用的是输入框里的值，保存后才会真正写进配置；图标因为已上传，已经是生效状态。
        </p>
      </aside>
    </div>
  </div>
</template>

<style scoped>
.brg-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 300px;
  gap: 18px;
  align-items: start;
}

@media (max-width: 1240px) {
  .brg-layout {
    grid-template-columns: minmax(0, 1fr);
  }
}

.brg-form {
  display: flex;
  flex-direction: column;
  gap: 14px;
  min-width: 0;
}

.brg-panel {
  padding: 14px 16px 16px;
}

.brg-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin-bottom: 12px;
}

.brg-head b {
  font-size: calc(12.5px * var(--fs-scale));
  font-weight: 600;
}

.brg-head .muted {
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.brg-preview-tag {
  margin-left: auto;
  padding: 1px 8px;
  border-radius: 999px;
  border: 1px solid var(--accent);
  color: var(--accent);
  font-size: calc(10px * var(--fs-scale));
}

.brg-themes {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(108px, 1fr));
  gap: 8px;
}

.brg-theme {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 9px 10px;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: var(--surface);
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition: border-color 0.14s ease, background 0.14s ease;
}

.brg-theme:hover {
  border-color: var(--border-strong);
}

.brg-theme.on {
  border-color: var(--accent);
  background: var(--accent-soft);
}

.brg-dot {
  width: 14px;
  height: 14px;
  flex: 0 0 auto;
  border-radius: 50%;
}

.brg-theme-copy {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.brg-theme-copy b {
  font-size: calc(12px * var(--fs-scale));
  font-weight: 500;
  color: var(--text);
  white-space: nowrap;
}

.brg-theme-copy small {
  font-size: calc(10px * var(--fs-scale));
  color: var(--text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.brg-check {
  width: 13px;
  height: 13px;
  margin-left: auto;
  flex: 0 0 auto;
  color: var(--accent);
}

.brg-radius-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-top: 14px;
  padding-top: 13px;
  border-top: 1px solid var(--border);
}

.brg-radius-label {
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-secondary);
  margin-right: 2px;
}

.brg-radius {
  display: flex;
  align-items: center;
  gap: 7px;
  white-space: nowrap;
  padding: 6px 10px 6px 8px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
  font: inherit;
  font-size: calc(11.5px * var(--fs-scale));
  color: var(--text-secondary);
  cursor: pointer;
  transition: border-color 0.14s ease;
}

.brg-radius i {
  width: 15px;
  height: 15px;
  border: 1.5px solid var(--text-muted);
}

.brg-radius.on {
  border-color: var(--accent);
  color: var(--text);
}

.brg-radius.on i {
  border-color: var(--accent);
}

.brg-radius-hint {
  margin-left: auto;
  font-size: calc(10.5px * var(--fs-scale));
  color: var(--text-muted);
}

.brg-fs {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.brg-fs-label {
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-secondary);
  margin-right: 2px;
}

.brg-fs-btn {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 5px 10px 5px 8px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
  font: inherit;
  font-size: calc(11.5px * var(--fs-scale));
  color: var(--text-secondary);
  cursor: pointer;
  white-space: nowrap;
  transition: border-color 0.14s ease;
}

.brg-fs-btn i {
  font-style: normal;
  line-height: 1;
  color: var(--text-muted);
}

.brg-fs-btn.on {
  border-color: var(--accent);
  color: var(--text);
}

.brg-fs-btn.on i {
  color: var(--accent);
}

.brg-note-inline {
  margin-left: auto;
  font-size: calc(10.5px * var(--fs-scale));
  color: var(--text-muted);
}

.brg-fonts {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
  margin-top: 14px;
  padding-top: 13px;
  border-top: 1px solid var(--border);
}

@media (max-width: 720px) {
  .brg-fonts {
    grid-template-columns: minmax(0, 1fr);
  }
}

.brg-font {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 9px 11px;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: var(--surface);
  text-align: left;
  cursor: pointer;
  transition: border-color 0.14s ease, background 0.14s ease;
}

.brg-font:hover {
  border-color: var(--border-strong);
}

.brg-font.on {
  border-color: var(--accent);
  background: var(--accent-soft);
}

.brg-font b {
  font-size: calc(13px * var(--fs-scale));
  font-weight: 500;
  color: var(--text);
}

.brg-font-sample {
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-secondary);
  letter-spacing: 0.02em;
}

.brg-font small {
  font-size: calc(10px * var(--fs-scale));
  color: var(--text-muted);
}

/* 预览块本身不跟字号缩放：它要展示的是「文字看起来多大」，
   外框跟着变大反而干扰判断。 */
.brg-typo-preview {
  position: relative;
  margin-top: 14px;
  padding: 12px 14px;
  border: 1px dashed var(--border-strong);
  border-radius: 9px;
  background: var(--bg-subtle);
}

.brg-typo-preview p {
  margin: 0;
  padding-right: 44px;
  line-height: 1.75;
  color: var(--text);
}

.brg-typo-tag {
  position: absolute;
  top: 10px;
  right: 12px;
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--bg-hover);
  font-size: calc(9.5px * var(--fs-scale));
  color: var(--text-muted);
}

.brg-fields {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.brg-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.brg-label {
  font-size: calc(12px * var(--fs-scale));
  color: var(--text-secondary);
}

.brg-field small {
  font-size: calc(11px * var(--fs-scale));
  line-height: 1.6;
  color: var(--text-muted);
}

.brg-logo {
  display: flex;
  align-items: center;
  gap: 16px;
}

.brg-logo-box {
  display: grid;
  place-items: center;
  width: 76px;
  height: 76px;
  flex: 0 0 auto;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--bg-subtle);
}

.brg-logo-actions {
  min-width: 0;
}

.brg-logo-buttons {
  display: flex;
  gap: 8px;
}

.brg-hint {
  margin: 9px 0 0;
  font-size: calc(11px * var(--fs-scale));
  line-height: 1.7;
  color: var(--text-muted);
}

.brg-note {
  margin: 0;
  padding: 10px 12px;
  border: 1px dashed var(--border-strong);
  border-radius: 9px;
  font-size: calc(11.5px * var(--fs-scale));
  line-height: 1.75;
  color: var(--text-muted);
}

/* ---- 预览 ---- */
.brg-preview {
  position: sticky;
  top: 12px;
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface);
}

.brg-preview-label {
  margin-bottom: 12px;
  font-size: calc(10.5px * var(--fs-scale));
  font-weight: 600;
  letter-spacing: 0.1em;
  color: var(--text-muted);
}

.brg-mock {
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: var(--bg-subtle);
}

.brg-mock-brand {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 12px 12px 10px;
}

.brg-mock-copy {
  min-width: 0;
}

.brg-mock-copy b {
  display: block;
  font-size: calc(13px * var(--fs-scale));
  font-weight: 600;
  line-height: 1.3;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.brg-mock-copy small {
  display: block;
  margin-top: 2px;
  font-size: calc(10.5px * var(--fs-scale));
  color: var(--text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.brg-mock-nav {
  display: flex;
  flex-direction: column;
  gap: 1px;
  padding: 0 8px 10px;
}

.brg-mock-nav span {
  padding: 6px 8px;
  border-radius: 6px;
  font-size: calc(11.5px * var(--fs-scale));
  color: var(--text-secondary);
}

.brg-mock-nav span.on {
  background: var(--bg-hover);
  color: var(--text);
}

/* 浏览器标签页示意：只保留能看出「图标 + 标题」的部分。 */
.brg-tab {
  display: flex;
  align-items: center;
  gap: 7px;
  margin-top: 14px;
  padding: 7px 9px;
  border: 1px solid var(--border);
  border-radius: 8px 8px 6px 6px;
  background: var(--bg-subtle);
}

.brg-tab-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--border-strong);
  flex: 0 0 auto;
}

.brg-tab-favicon {
  flex: 0 0 auto;
  line-height: 0;
}

.brg-tab-title {
  flex: 1;
  min-width: 0;
  font-size: calc(11.5px * var(--fs-scale));
  color: var(--text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.brg-tab-x {
  flex: 0 0 auto;
  font-size: calc(11px * var(--fs-scale));
  color: var(--text-muted);
}

.brg-preview-hint {
  margin: 12px 0 0;
  font-size: calc(11px * var(--fs-scale));
  line-height: 1.7;
  color: var(--text-muted);
}
</style>
