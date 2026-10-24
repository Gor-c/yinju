# 品牌资产

![品牌陈列](brand-sheet.png)

## 标记的语义

一页文档（深色圆角方块 + 右上折角），三条切分后的片段，中间那条用强调色。

对应产品唯一的核心动作：**从大量原文里定位到那一段**。没有用放大镜——放大镜讲的是「查」，这里讲的是「命中并回到原文」。

配色全部取自现有设计令牌，不引入第二种强调色：

| 色值 | 用途 |
|---|---|
| `#171717` | 文档本体 |
| `#10a37f` | 被命中的那一段 |
| `#ffffff` | 其余片段（以透明度分层） |

## 文件清单

放在 `frontend/public/`，构建时复制到 `dist/` 根目录，由 `NoRoute` 里的静态文件分支直接返回（`internal/app/app.go`）。

| 文件 | 用途 | 说明 |
|---|---|---|
| `favicon.svg` | 浏览器标签页 | **两线版**。16px 下三线会糊成一片，减线加粗后仍能读出「深色方块 + 一道绿」 |
| `logo-mark.svg` | 侧栏、lockup、浅色背景 | 三线版，本体 `#171717` |
| `logo-mark-dark.svg` | 深色背景 | 与上行同形，本体抬到 `#2b2b2b`，否则在深色底上容器会消失、只剩浮着的线 |
| `logo-lockup.svg` | 横向组合（标记 + 中文名 + 定位语） | 文字用 `<text>`，依赖系统字体栈 |
| `icon-square.svg` | 应用图标源文件 | 满版底色、无外圆角、内容缩到 80% 安全区 |
| `icon-512.png` | PWA / 应用市场 | 由 `icon-square.svg` 导出 |
| `apple-touch-icon.png` | iOS 桌面 | 180×180，系统自行裁剪圆角 |

界面里的侧栏标记是**内联 SVG**（`App.vue` 的 `.brand-icon`），因为深色模式需要改底色，而 `<img>` 引用无法继承页面的 CSS 变量。内联版的强调色用 `var(--accent)`，深色模式下会自动跟着换成 `#19c37d`。

## 尺寸策略

| 尺寸 | 用哪个版本 | 原因 |
|---|---|---|
| 16–24px | `favicon.svg`（两线） | 线条数量让位于可辨性 |
| 32px+ | `logo-mark.svg`（三线） | 折角与层次都能读出 |
| 桌面图标 | `icon-square.svg` | 无外轮廓时折角会读成孤立三角形，故这一版去掉折角 |

三份文件的共同点：深色方块 + 一条强调色横线。跨尺寸识别靠这个，而不是靠细节。

## 修改约定

改标记时三处要一起改，否则会出现「标签页和侧栏长得不一样」：

1. `frontend/public/*.svg`
2. `frontend/src/App.vue` 里 `.brand-icon` 的内联 SVG
3. 本文件顶部的陈列图（用 `docs/brand/brand-source.html` 重新导出）

重新导出 PNG 的命令（Chrome headless，尺寸精确）：

```sh
# 180 / 512 各生成一张，body 尺寸与窗口尺寸一致
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new \
  --screenshot=/tmp/icon-512.png --window-size=512,512 \
  "file:///path/to/gen-512.html"
```

## 运行时可改（设置 → 外观 → 系统外观）

上面那些文件是**出厂标记**。日常改名、换图标不需要改代码，也不需要重新打包：

| 项 | 存储 | 生效时机 |
|---|---|---|
| 项目名称 | `brand_settings.app_name` | 保存后，侧栏、面包屑、标签页一起更新 |
| 副标题 | `brand_settings.app_subtitle` | 保存后 |
| 浏览器标题 | `brand_settings.browser_title` | 保存后；留空则回落到项目名称 |
| 自定义图标 | `brand_settings.logo_file` + `BRAND_DIR` 下的文件 | **上传即生效**，不用保存 |

接口：`GET /api/v1/brand`、`PUT /api/v1/brand`、`POST /api/v1/brand/logo`、`GET /api/v1/brand/logo`。

### 几个刻意的取舍

**logo 不放 `frontend/dist`。** 那是构建产物，一次 `pnpm build` 就会清空；二进制里嵌入的也只是构建时刻的快照。上传的图落在 `BRAND_DIR`（默认 `./data/brand`），重启后仍在。

**只接受 PNG / JPEG / WebP。** SVG 能内嵌 `<script>`，虽然作为 `<img>` 引用时不会执行，但这个地址可以被直接打开——允许上传 SVG 等于开放一个同源脚本注入点。格式按**魔数**判断，不看扩展名（`internal/app/brand_test.go` 锁住了这条）。

**文件名带内容哈希**（`logo-<sha256前12位>.png`）。换图必然换 URL，浏览器缓存自动失效；重复上传同一张图不会堆文件。响应头是 `immutable` 的一年缓存。

**恢复默认会删文件。** 「恢复默认」把 `logo_source` 置回 `builtin` 并删掉磁盘上的图，不留孤儿文件；前端随即回落到内置 SVG。

**改动进同一本留痕账。** 与检索参数共用 `setting_changes`，键名 `app_name` / `app_subtitle` / `browser_title` / `brand_logo`。

### 多实例部署的注意点

名称与 logo 的**元数据**在数据库里，多实例共享。但**图片文件**在各自的 `BRAND_DIR` 里——多机部署时要把该目录挂成共享卷，否则只有上传的那台能显示自定义图标，其余实例会因 404 回落到内置标记（不会报错，但会让人以为上传没成功）。

### 不随配置变化的东西

- `apple-touch-icon.png`：iOS 只在「添加到主屏」时读取一次，改了图标不会自动更新已添加的桌面图标。
- `index.html` 里硬编码的 `<title>`：作为无 JS 时的兜底，有 JS 时会被配置覆盖。首屏可能有一次极短的标题跳变。

## 外观模板（配色 + 圆角）

设置 → 外观 → 系统外观，上半部分是模板选择。

### 七套配色

| id | 名称 | 强调色 | 底色色温 |
|---|---|---|---|
| `graphite` | 石墨（默认） | `#10a37f` 青绿 | 纯白 / `#212121` |
| `indigo` | 靛蓝 | `#4f46e5` | 极淡紫 |
| `azure` | 湖蓝 | `#0284c7` | 极淡蓝 |
| `pine` | 松墨 | `#0f766e` | 极淡青 |
| `amber` | 琥珀 | `#b45309` | 极淡暖黄 |
| `violet` | 紫藤 | `#7c3aed` | 极淡紫 |
| `rose` | 胭脂 | `#be123c` | 极淡粉 |

深色模式各有一档更亮的 `darkAccent`：同一个色值在深底上会发闷，比如靛蓝从 `#4f46e5` 提到 `#818cf8`。

### 三档圆角

`compact` 6/8 · `standard` 8/12（默认）· `soft` 10/16

同时写 `--radius-sm / --radius / --radius-lg` 与 Element Plus 的两个圆角变量。

### 只改三样东西

1. **强调色**（`--accent` 及派生的 `--el-color-primary-light-*`）
2. **页面底色**（`--bg`、`--bg-subtle`）
3. **圆角半径**

**文字色、边框色、卡片底色（`--surface`）一律不动。** 这是刻意的：底色带一点色温、卡片保持纯白/深灰，层次反而更清楚；而一旦让人随便改文字色，对比度就没有保障了。所有色温偏移控制在 RGB 6 以内。

### 实现方式

`frontend/src/appearance.ts` 在 `<html>` 上写行内 CSS 变量（行内优先级高于 `:root` 和 `html.dark` 里的同名声明），所以能盖掉已有主题；切换深浅模式时重算一次，因为强调色分两档。

Element Plus 的 `--el-color-primary-light-3/5/7/8/9` 是构建期算好的固定值，不改它们会出现「主按钮变色了、但悬停态和选中背景还是旧的绿」。这里按同样的混合规则现算：浅色模式混白、深色模式混黑。

### 预览与持久化

点一下色卡**立即**应用到整站，但只有点保存才写库。页面右上会出现「预览中 · 未保存」；离开页面时自动恢复成已保存的值，避免出现「看着变了、其实没存」的假象。

### 加一套新主题

两处必须同步改，否则后端会拒绝保存（白名单校验）：

1. `frontend/src/appearance.ts` 的 `THEME_PRESETS`：加 id、名称、浅/深强调色、浅/深底色
2. `internal/app/brand.go` 的 `appearanceThemes`：加同一个 id

后端只存 id、不存色值——换配色不需要迁移数据，也不会出现「库里存了半个旧主题」的状态。库里若是不认识的 id（手工改过），读取时自动回落到默认，而不是渲染成没有强调色的界面。

## 字号与字体

| 项 | 档位 | 存储 |
|---|---|---|
| 字号 | 小 0.94 · 标准 1 · 大 1.08 · 特大 1.16 | `brand_settings.appearance_font_size` |
| 字体 | 系统默认 · 黑体 · 宋体 · 等宽 | `brand_settings.appearance_font` |

### 字号为什么用乘数，而不是建字号表

项目里有 **364 处** `font-size`，散在 `style.css` 与 15 个 `.vue` 的 scoped 样式里。逐个抽成 `--fs-12` / `--fs-13` 这样的 token 不现实，而且每加一个新字号就要同步维护。

所以做法是：**所有字号都写成 `calc(基准px * var(--fs-scale))`**

```css
font-size: calc(13px * var(--fs-scale));   /* 原来写 13px */
```

设置页只改 `<html>` 上的一个 `--fs-scale`。好处是：

- **标准档（1）与改造前的像素值完全一致**，不存在「一上来就比原来大一点」的偏移；
- 新写的样式照常用 px 即可（只要在源码里写成 calc 形式），不需要登记；
- 只有一处需要理解，而不是一张 16 种取值的字号表。

`--fs-scale` 默认值是 `1`，定义在 `style.css` 的 `:root` 里。这个默认值不能省——如果变量不存在，`calc()` 整体失效，`font-size` 会退化成继承，界面会乱。

### 只放大字，不动间距

字号缩放**不改变** padding / margin / 侧栏宽度。所以选「特大」时，文字会比原来占更多行，个别窄容器（如 220px 的侧栏标签）可能出现截断——这是有意的取舍：

- 如果连间距一起放大，等于整体缩放界面，用户会失去「以为只是字变大」的预期；
- 侧栏标签本来就有 `overflow: hidden` + `text-overflow: ellipsis`，截断是可控的退化，而不是布局崩坏。

档位上限收在 1.16，也是这个原因：再往上调，右侧 330px 的知识库面板会挤到不可用。

### 字体按中文优先排序

四套字体栈的第一顺位都是中文字体，因为中文字形直接决定这个系统的可读性，英文只是附带：

| id | 首选中文字体 | 适合 |
|---|---|---|
| `system` | 系统默认（macOS 苹方 / Windows 雅黑） | 不折腾 |
| `hei` | `PingFang SC` / `Noto Sans SC` / `Source Han Sans SC` | 字形规整，正文默认感 |
| `song` | `Songti SC` / `Noto Serif SC` | 衬线，长文阅读 |
| `mono` | `JetBrains Mono` / `SF Mono` / `Noto Sans Mono CJK SC` | 技术文档，字距均匀 |

每套都带 Linux 与 Windows 兜底。`mono` 在缺少 CJK 等宽字体的机器上，中文会回落到系统黑体，只有西文和数字是等宽的——这是可以接受的降级，不报错。

字体同时写入 `--font-sans` 与 Element Plus 的 `--el-font-family`，否则弹窗、表格、下拉里的文字会留在旧字体上。

### 加一套字体 / 一个字号档

两处同时改，否则后端会 400：

1. `frontend/src/appearance.ts` 的 `FONT_PRESETS`（或 `FONT_SIZE_PRESETS`）
2. `internal/app/brand.go` 的 `appearanceFonts`（或 `appearanceFontSizes`）

后端只存 id，不存字体栈。库里出现不认识的 id 时，读取阶段自动回落到默认，不会渲染成无字体状态。

### 预览行为

设置页点一下立即生效，右侧预览块用当前字体与字号渲染一句真实条款文本。和其他外观项一样，**不保存就离开会自动恢复**。
