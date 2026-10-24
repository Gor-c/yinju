import { ref } from 'vue'
import axios from 'axios'
import {
  applyAppearance,
  DEFAULT_FONT,
  DEFAULT_FONT_SIZE,
  DEFAULT_RADIUS,
  DEFAULT_THEME,
} from './appearance'

export interface BrandConfig {
  app_name: string
  app_subtitle: string
  browser_title: string
  logo_source: 'builtin' | 'upload'
  logo_url: string
  logo_type: string
  theme: string
  radius: string
  font_size: string
  font_family: string
}

export const defaultBrand: BrandConfig = {
  app_name: '知识库管理',
  app_subtitle: '智能业务平台',
  browser_title: '知识库管理 Agent',
  logo_source: 'builtin',
  logo_url: '',
  logo_type: '',
  theme: DEFAULT_THEME,
  radius: DEFAULT_RADIUS,
  font_size: DEFAULT_FONT_SIZE,
  font_family: DEFAULT_FONT,
}

// 模块级单例：侧栏、设置页、浏览器标签页读同一份状态。
// 保存后一处更新，其余位置自动跟随，不需要刷新页面。
export const brand = ref<BrandConfig>({ ...defaultBrand })

export function applyBrand(next: Partial<BrandConfig>) {
  brand.value = { ...defaultBrand, ...next }
  syncTitle(brand.value)
  syncFavicon(brand.value)
  applyAppearance(brand.value.theme, brand.value.radius, brand.value.font_size, brand.value.font_family)
}

// 浏览器标题留空时回落到项目名，避免标签页变成空白。
function syncTitle(cfg: BrandConfig) {
  const title = (cfg.browser_title || '').trim() || cfg.app_name
  if (title) document.title = title
}

function syncFavicon(cfg: BrandConfig) {
  const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (!link) return
  if (cfg.logo_source !== 'upload' || !cfg.logo_url) {
    link.href = '/favicon.svg'
    link.type = 'image/svg+xml'
    return
  }
  // 自定义图可能是 PNG/JPEG/WebP，type 必须跟着换：
  // 沿用 image/svg+xml 会让浏览器按错误格式解析，图标直接不显示。
  link.href = cfg.logo_url
  if (cfg.logo_type) link.type = cfg.logo_type
}

export async function loadBrand() {
  try {
    const { data } = await axios.get('/api/v1/brand')
    applyBrand(data)
  } catch {
    // 后端未就绪时保持默认值：界面不该因为拿不到品牌配置而空白。
    applyBrand({})
  }
}
