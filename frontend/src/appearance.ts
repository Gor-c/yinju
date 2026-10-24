/**
 * 外观预设：配色主题 + 圆角档位。
 *
 * 只存 id，色值全部在这里 —— 加一套配色不需要改数据库，也不需要迁移。
 * 应用方式是在 <html> 上写 CSS 变量（行内样式优先级高于 :root 与 html.dark
 * 里的同名声明），所以能覆盖已有主题，且切换模式时重新应用一次即可。
 */

export interface ThemePreset {
  id: string
  name: string
  /** 浅色模式下的强调色 */
  accent: string
  /** 深色模式下用更亮的一档：同一个色值在深底上会显得发闷 */
  darkAccent: string
  /**
   * 页面底色的极轻色温。只改 --bg，不动 --surface：
   * 卡片保持纯白（深色下保持 #2a2a2a），于是「底有色温、卡片更亮」，
   * 层次反而比原来更清楚。色差控制在 RGB 6 以内，不碰对比度。
   */
  lightBg: string
  darkBg: string
  hint: string
}

export const THEME_PRESETS: ThemePreset[] = [
  { id: 'graphite', name: '石墨', accent: '#10a37f', darkAccent: '#19c37d', lightBg: '#ffffff', darkBg: '#212121', hint: '默认，青绿' },
  { id: 'indigo', name: '靛蓝', accent: '#4f46e5', darkAccent: '#818cf8', lightBg: '#fbfaff', darkBg: '#1f1e29', hint: '偏冷，沉稳' },
  { id: 'azure', name: '湖蓝', accent: '#0284c7', darkAccent: '#38bdf8', lightBg: '#f9fcff', darkBg: '#1e2126', hint: '清亮' },
  { id: 'pine', name: '松墨', accent: '#0f766e', darkAccent: '#2dd4bf', lightBg: '#f9fdfb', darkBg: '#1e2422', hint: '低饱和青' },
  { id: 'amber', name: '琥珀', accent: '#b45309', darkAccent: '#f59e0b', lightBg: '#fffcf6', darkBg: '#241f18', hint: '暖色，警示感强' },
  { id: 'violet', name: '紫藤', accent: '#7c3aed', darkAccent: '#a78bfa', lightBg: '#fcfaff', darkBg: '#221f2a', hint: '偏紫' },
  { id: 'rose', name: '胭脂', accent: '#be123c', darkAccent: '#fb7185', lightBg: '#fff9fa', darkBg: '#241e21', hint: '强对比' },
]

export interface RadiusPreset {
  id: string
  name: string
  sm: number
  base: number
  lg: number
  elBase: number
  elSmall: number
}

export const RADIUS_PRESETS: RadiusPreset[] = [
  { id: 'compact', name: '紧凑', sm: 6, base: 8, lg: 8, elBase: 8, elSmall: 6 },
  { id: 'standard', name: '标准', sm: 8, base: 12, lg: 12, elBase: 10, elSmall: 8 },
  { id: 'soft', name: '柔和', sm: 10, base: 16, lg: 16, elBase: 14, elSmall: 10 },
]

export interface FontSizePreset {
  id: string
  name: string
  scale: number
}

/**
 * 字号档位只改一个乘数。CSS 里所有 font-size 都写成
 * calc(基准px * var(--fs-scale))，所以标准档（1）与改造前的像素值完全一致，
 * 不存在「一上来就比原来大一点」的偏移。
 *
 * 范围收在 0.94–1.16：再小正文会掉到 12px 以下，再大侧栏的标签会换行。
 */
export const FONT_SIZE_PRESETS: FontSizePreset[] = [
  { id: 'sm', name: '小', scale: 0.94 },
  { id: 'standard', name: '标准', scale: 1 },
  { id: 'lg', name: '大', scale: 1.08 },
  { id: 'xl', name: '特大', scale: 1.16 },
]

export interface FontPreset {
  id: string
  name: string
  stack: string
  hint: string
}

/**
 * 字体栈按「中文优先」排列：中文字形直接影响这个系统的可读性，
 * 英文/西文只是附带。每套都留了 Linux 与 Windows 的兜底字体。
 */
export const FONT_PRESETS: FontPreset[] = [
  {
    id: 'system',
    name: '系统默认',
    stack:
      '-apple-system, BlinkMacSystemFont, "Segoe UI", "Noto Sans SC", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif',
    hint: '跟随操作系统',
  },
  {
    id: 'hei',
    name: '黑体',
    stack:
      '"PingFang SC", "Noto Sans SC", "Source Han Sans SC", "Microsoft YaHei", system-ui, sans-serif',
    hint: '中文无衬线，字形规整',
  },
  {
    id: 'song',
    name: '宋体',
    stack:
      '"Songti SC", "Noto Serif SC", "Source Han Serif SC", "SimSun", Georgia, serif',
    hint: '衬线，适合读长文',
  },
  {
    id: 'mono',
    name: '等宽',
    stack:
      '"JetBrains Mono", "SF Mono", "Cascadia Code", Menlo, Consolas, "Noto Sans Mono CJK SC", monospace',
    hint: '字距均匀，贴技术文档',
  },
]

export const DEFAULT_THEME = 'graphite'
export const DEFAULT_RADIUS = 'standard'
export const DEFAULT_FONT_SIZE = 'standard'
export const DEFAULT_FONT = 'system'

export function findTheme(id: string): ThemePreset {
  return THEME_PRESETS.find((t) => t.id === id) || THEME_PRESETS[0]
}

export function findRadius(id: string): RadiusPreset {
  return RADIUS_PRESETS.find((r) => r.id === id) || RADIUS_PRESETS[1]
}

export function findFontSize(id: string): FontSizePreset {
  return FONT_SIZE_PRESETS.find((f) => f.id === id) || FONT_SIZE_PRESETS[1]
}

export function findFont(id: string): FontPreset {
  return FONT_PRESETS.find((f) => f.id === id) || FONT_PRESETS[0]
}

// ---------- 颜色工具 ----------

function toRgb(hex: string): [number, number, number] {
  const v = hex.replace('#', '')
  const full = v.length === 3 ? v.split('').map((c) => c + c).join('') : v
  return [
    parseInt(full.slice(0, 2), 16),
    parseInt(full.slice(2, 4), 16),
    parseInt(full.slice(4, 6), 16),
  ]
}

function toHex(rgb: [number, number, number]): string {
  return '#' + rgb.map((n) => Math.round(n).toString(16).padStart(2, '0')).join('')
}

/** w 是 a 的占比：mix('#000', '#fff', 0.3) 得到 30% 黑 + 70% 白 */
function mix(a: string, b: string, w: number): string {
  const x = toRgb(a)
  const y = toRgb(b)
  return toHex([0, 1, 2].map((i) => x[i] * w + y[i] * (1 - w)) as [number, number, number])
}

function alpha(hex: string, a: number): string {
  const [r, g, b] = toRgb(hex)
  return `rgba(${r}, ${g}, ${b}, ${a})`
}

/**
 * 把外观预设写到 <html> 上。
 *
 * Element Plus 的 --el-color-primary-light-N 是在构建时算好的固定值，
 * 不改它们的话，主按钮颜色变了、但悬停态/选中背景还是旧的绿。
 * 这里按同一套混合规则现算，浅色模式混白、深色模式混黑。
 */
export function applyAppearance(
  themeId: string,
  radiusId: string,
  fontSizeId: string,
  fontFamilyId: string,
) {
  const theme = findTheme(themeId)
  const radius = findRadius(radiusId)
  const fontSize = findFontSize(fontSizeId)
  const font = findFont(fontFamilyId)
  const dark = document.documentElement.classList.contains('dark')
  const accent = dark ? theme.darkAccent : theme.accent
  const base = dark ? '#1c1c1c' : '#ffffff'

  const root = document.documentElement.style
  const set = (k: string, v: string) => root.setProperty(k, v)

  const bg = dark ? theme.darkBg : theme.lightBg
  set('--bg', bg)
  // 比底色再暗一点，用于分区底色；幅度与原来 #fff → #f7f7f8 的关系一致。
  set('--bg-subtle', mix(bg, '#000000', dark ? 0.9 : 0.975))

  set('--accent', accent)
  set('--accent-hover', dark ? mix(accent, '#ffffff', 0.86) : mix(accent, '#000000', 0.88))
  set('--accent-soft', mix(accent, base, dark ? 0.18 : 0.08))
  set('--focus-ring', alpha(accent, dark ? 0.22 : 0.16))

  set('--el-color-primary', accent)
  set('--el-color-primary-light-3', mix(accent, base, dark ? 0.72 : 0.7))
  set('--el-color-primary-light-5', mix(accent, base, dark ? 0.55 : 0.5))
  set('--el-color-primary-light-7', mix(accent, base, dark ? 0.36 : 0.3))
  set('--el-color-primary-light-8', mix(accent, base, dark ? 0.28 : 0.2))
  set('--el-color-primary-light-9', mix(accent, base, dark ? 0.2 : 0.1))
  set('--el-color-primary-dark-2', mix(accent, '#000000', 0.85))

  // 字号与字体：字号只改乘数，字体同时写 --font-sans 与 Element Plus 的
  // --el-font-family，否则弹窗和表格里的文字会留在旧字体上。
  set('--fs-scale', String(fontSize.scale))
  set('--font-sans', font.stack)
  set('--el-font-family', font.stack)

  set('--radius-sm', `${radius.sm}px`)
  set('--radius', `${radius.base}px`)
  set('--radius-lg', `${radius.lg}px`)
  set('--el-border-radius-base', `${radius.elBase}px`)
  set('--el-border-radius-small', `${radius.elSmall}px`)
}

/** 供设置页做色卡预览：不改全局，只算出一组可直接用在 style 上的值 */
export function themeSwatch(id: string) {
  const theme = findTheme(id)
  const dark = document.documentElement.classList.contains('dark')
  const accent = dark ? theme.darkAccent : theme.accent
  const base = dark ? '#1c1c1c' : '#ffffff'
  return { accent, soft: mix(accent, base, dark ? 0.18 : 0.1) }
}
