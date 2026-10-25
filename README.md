# 知识库管理系统

Go + Vue 3 的知识库管理与 Agent 对话入口。Go 负责 API、文件解析、切块、调用 OpenAI-compatible embedding/chat API，以及通过可替换的 pgvector/Chroma provider 持久化向量；内置 Agent 使用只读工具循环并通过 SSE 流式回答。前端提供项目管理（分类、行业、标签、Agent/技能绑定、本地知识关联图谱）、文档上传、项目画像、档案检索、Agent 对话（支持同时选择多个知识库、展示所选知识库画像、历史会话回看）以及 Agent/技能定义上传。

## 启动

需要 Go 1.23+、Node 20+、PostgreSQL 15+，以及 Chroma 或 pgvector 之一作为向量存储。

本地开发：业务表（`units`/`hazards`/`audit_records`/`index_jobs`/`documents`/`agent_runs`/`agent_steps`）始终走 PostgreSQL；向量默认写本地 Chroma（`VECTOR_STORE=chroma`），线上可切换为 pgvector。Chroma 需先启动（本项目跑在 OrbStack k8s，Service 为 ClusterIP，必须先 `kubectl -n knowledge-base port-forward svc/chroma 8000:8000`，否则 Go 服务启动会因连不上向量库而失败）；若使用 pgvector，则安装扩展并额外执行 `deploy/schema-pgvector.sql`。复制 `.env.example` 为 `.env` 并填写配置，然后初始化业务表：

```sh
# psql 需要 shell 里先加载 .env
set -a; . ./.env; set +a
psql "$PG_DSN" -f deploy/schema.sql
# 仅 VECTOR_STORE=pgvector 时执行：
# psql "$PG_DSN" -f deploy/schema-pgvector.sql
cd frontend && pnpm install && pnpm build && cd ..
# Chroma 跑在 OrbStack k8s（knowledge-base 命名空间），Service 是 ClusterIP，需先做端口转发：
# kubectl -n knowledge-base port-forward svc/chroma 8000:8000 &
# curl -s localhost:8000/api/v2/heartbeat
go run ./cmd/server   # 自动加载项目根目录的 .env，无需 source
```

服务启动时会自动读取工作目录下的 `.env`（可用 `ENV_FILE` 指定其他路径）；**已存在的进程环境变量优先，不会被 `.env` 覆盖**，因此 systemd/容器注入的配置始终胜出。

服务默认监听 `127.0.0.1:8080`，`/health` 可检查进程状态。可用 `LISTEN_ADDR` 配置监听地址；远程部署应放在受控的 HTTPS 反向代理后。未配置数据库时，health 仍可用，业务 API 返回明确的依赖错误。端口由 `.env` 的 `PORT` 决定（本仓库 `.env` 为 `8081`），前端 Vite 代理目标必须与之相同，否则页面请求会打到别的进程而出现 `API not found`。

## 代码结构

| 目录 | 职责 | 边界 |
|---|---|---|
| `internal/rag` | 检索编排：切块、嵌入、多路召回、RRF 融合、重排、证据判定 | 只有接口，不读环境变量、不碰数据库、不打日志 |
| `internal/agent` | 工具循环与工具目录（`Catalog`）：消息转录、并发执行、步数上限 | 不感知业务查询与向量检索 |
| `internal/app` | 装配层：HTTP、数据库、模型客户端，把上面两层接起来 | 不写检索排序算法 |
| `internal/vectorstore` | 向量读写边界（pgvector / Chroma v2） | 不决定检索策略 |

两个扩展点，都只改一处：

- **加一路召回**：在 `internal/app/retrieval.go` 的 `Channels` 里 append 一个 `rag.Channel`。
  融合走 RRF 只看排名，新增通道不需要重新调权重；单路失败自动降级。
- **加一个工具**：在 `internal/app/agent_tools.go` 的 `builtinTools` 里加一个元素。
  白名单、展示顺序、提示词里的工具清单、上传定义校验全部由它派生。

`internal/app` 内部按职责分文件，不是一锅。需要改动时先按下表定位：

| 文件 | 职责 | 依赖数据库 |
| --- | --- | --- |
| `app.go` | 装配、路由、启动时的表结构迁移 | 是（迁移） |
| `retrieval.go` | 把 DB/模型客户端翻译成 `rag` 的接口；检索留痕 | 是 |
| `keyword_index.go` | `chunk_text` 镜像表与 pg_trgm 召回 | 是 |
| `memory.go` | 三层记忆：窗口回放、滚动摘要、原子事实与冲突消解 | 是 |
| `runtime_settings.go` | 检索/切块/记忆参数，DB 优先于环境变量 | 是 |
| `outline.go` | 章节抽取（规则 v5）与留痕 | 是 |
| `examples.go` | 用章节生成示例问题，并用真实检索验证 | 是 |
| `tree.go` | 知识库 → 文档 → 章节 → 片段 的目录树 | 是 |
| `brand.go` | 名称、图标、外观主题 | 是 |
| `instance.go` | 数据库/向量库/运行目录 | **否**（故意的，见 `docs/instance-config.md`） |

`documents` 表记录每个文档的 `embedding_model / embedding_dim / chunker`；换模型后未重建索引时，检索直接返回带重建命令的错误，而不是让两套向量静默混算。详见 [`docs/development.md`](docs/development.md)。

## 配置

embedding 和 chat 都使用 OpenAI-compatible API，可在前端「设置 > 模型服务」填写 base_url、模型和 API Key 并保存（写入 PostgreSQL `model_settings`，立即生效，无需重启）；页面配置优先于 `EMBED_*`/`LLM_*` 环境变量，字段留空时回落到环境变量。BGE-M3 模型维度固定为 1024；更换 embedding 模型时必须重建全部索引。结构化业务数据始终存入 PostgreSQL；向量读写经 `internal/vectorstore.Store` 组件接口，`VECTOR_STORE=pgvector` 使用 PostgreSQL pgvector，`VECTOR_STORE=chroma` 使用 Chroma v2 REST adapter，均不需改上传和 Agent handler。服务同时提供 MCP Streamable HTTP endpoint `/mcp`，可供 WorkBuddy 或其他 MCP host 调用项目、隐患、审核和向量检索工具。远程使用时必须配置强随机 `MCP_AUTH_TOKEN` 并通过 HTTPS 暴露。WorkBuddy connector 包位于 `workbuddy/knowledge-base-connector/`，包含 connector metadata、MCP config、token 表单、图标和 Skill。

索引任务是持久化队列：上传只写 `index_jobs(queued)`，由后台 worker 领取执行，启动时只恢复超过单任务超时阈值的陈旧 `running` 任务，避免多实例启动时抢占仍在处理的任务（异常退出后的同一任务最多恢复 3 次；索引处理报错时会直接标记 `failed`，需排查后重新上传）；`documents` 记录每个已索引文档的版本、chunk 数和源文件路径。重新上传同名文件会覆盖对应 chunk 并删除尾部多余 chunk；改名或撤档后可用 `DELETE /api/v1/units/:id/documents/:filename` 清理旧向量和源文件。上传受 `MAX_UPLOAD_MB`（默认 20）和扩展名白名单限制；上传时的项目可以直接选择已有 `unit_id`，也可以填写项目名称，服务端未匹配到时会自动创建项目。

项目详情的“文档索引”页可分别查看源文件提取的完整文本和向量库中的索引片段；PDF 与 DOCX 使用索引时相同的解析流程。

Agent UI 的专家/技能/连接器描述可用 `AGENT_EXPERTS_JSON`、`AGENT_SKILLS_JSON`、`MCP_CONNECTORS_JSON` 提供；也可以在「设置 > Agent 与技能」中上传 JSON 或 Markdown 定义，上传项覆盖同 id 的内置项。设置页还包含「模型服务」和「联网搜索」分类；旧的 `/agents` 地址会重定向到 Agent 与技能设置页。每个项目可绑定一个 Agent 和若干技能，对话页选择项目后自动套用，并按技能声明的 `tools`（Agent 作为上限）带出可用工具。内置 Agent 选择项目后会进入只读工具循环（项目画像、隐患、审核、向量检索，最多 `AGENT_MAX_STEPS` 步），并通过 `/api/v1/agent/stream` 以 SSE 流式返回；`/api/v1/agent/chat` 保留为非流式兼容接口。每次运行写入 `agent_runs`，每个工具调用写入 `agent_steps`；服务启动时仅将超过 Agent 最大运行时限的陈旧 `running` 运行标记为 `interrupted`。外部 connector 目前仍只展示，不参与内置 Agent 调用。

联网搜索为可选能力：服务端开启 `WEB_SEARCH_ENABLED` 并配置 `WEB_SEARCH_PROVIDER`（tavily / bocha / brave）与 API Key 后，Agent 会多出 `web_search` 工具，但仍需用户在本轮对话勾选「联网搜索」才会启用。搜索关键词会离开本机，因此默认关闭；网页来源在引用中单独标记为「网页来源」，与已入库档案区分，且结果会作为不可信内容回传给模型（网页中的指令不执行）。支持站点白/黑名单、结果缓存和调用超时。**配置可在「设置」页完成**（`web_search_settings` 单行表，优先于环境变量，保存即生效，密钥掩码回显），并可一键测试搜索连通性。

搜索到的网页可**一键存入知识库**：服务端抓取正文（仅文本类响应，去掉脚本样式，上限 4MB）后走与上传相同的索引队列，落盘为 `.txt`、保留来源 URL 用于去重与溯源，之后可检索、可预览。抓取有 SSRF 防护（拒绝内网/回环/链路本地/CGNAT 目标，重定向逐跳复检）。WorkBuddy 通过 connector 包独立连接 MCP endpoint。

支持 txt/md/json/html/csv/docx/pdf；PDF 优先调用 `pdftotext`（poppler，可用 `PDFTOTEXT_BIN` 指定路径），未安装时回退到内置纯 Go 解析。项目详情的「查看源文档」提供两个标签页：**原文预览**（PDF、Word DOCX、Excel XLS/XLSX/CSV、Markdown 与文本直接在弹窗内渲染，基于 `@vue-office` 按格式懒加载）和**提取文本**（索引用的纯文本，支持查找、匹配定位和复制）；不支持预览的格式可下载原件。上传通过 PostgreSQL `index_jobs` 持久化排队，由 Go worker 异步处理；服务启动时只恢复超时阈值外的陈旧 `running` 任务；异常退出最多恢复 3 次，索引处理报错直接标记失败。上传原件按项目目录保存。

## 日志

服务使用标准库 `slog` 输出结构化日志，按用途写入 `LOG_DIR`（默认 `./logs`）：

| 文件 | 内容 |
| --- | --- |
| `app.log` | 应用、索引任务、模型调用和数据库错误 |
| `access.log` | API/页面访问日志（方法、路径、状态码、耗时、request_id；静态资源和 `/health` 不计入 INFO） |
| `error.log` | 应用与 HTTP 的 ERROR 级别汇总 |

日志默认同时输出到 stdout（systemd 下进入 journald）；`LOG_LEVEL`、`LOG_FORMAT=json`、`LOG_MAX_SIZE_MB`、`LOG_MAX_BACKUPS` 可配置，超过大小自动轮转为 `app-YYYYMMDD-HHMMSS.mmm.log`。每个响应带 `X-Request-ID`，日志统一携带 `request_id`、`job_id`、`unit_id` 等字段，但不记录 API key、MCP token 或档案正文。

需求基线见 [`docs/requirements.md`](docs/requirements.md)，开发、配置和部署说明（含系统架构图、上传索引流程、Agent 工具循环/SSE 流程及 MCP 调用流程）见 [`docs/development.md`](docs/development.md)，接口契约见 [`docs/api.md`](docs/api.md)。本地开发前端：`cd frontend && pnpm dev`，Vite 将 `/api`、`/health`、`/mcp` 转发到 `localhost:8081`（与 `.env` 的 `PORT` 保持一致）。

## 前端依赖：统一使用 pnpm

前端一律用 pnpm（**不要与 npm 混用**：混用会破坏 `node_modules/.pnpm` 结构，导致依赖解析失败）：

```sh
cd frontend
pnpm install     # 首次安装
pnpm dev         # 开发服务器，默认 5173
pnpm build       # vue-tsc -b + vite build
```

三个必须知道的点：

1. **`@vue-office/*` 的入口由 postinstall 生成**：这三个包发布的是 UMD，`lib/index.js` 由 `postinstall` 脚本按 Vue 版本生成。pnpm 10 默认不执行依赖构建脚本，因此 `package.json` 里用 `pnpm.onlyBuiltDependencies` 显式批准（`@vue-office/docx|excel|pdf`、`esbuild`、`vue-demi`）。出现 `Failed to resolve entry for package "@vue-office/docx"` 时，执行 `pnpm rebuild` 重新生成入口。
2. **`three` 必须显式声明**：`3d-force-graph` 依赖 `three`，npm 会提升传递依赖而 pnpm 不会，不显式安装时 `import('three')` 无法解析，打包报 `Rollup failed to resolve import "three"`。
3. **多平台原生二进制**：`pnpm.supportedArchitectures` 声明 darwin/linux/win32 × arm64/x64，避免锁文件只包含当前平台的 `@rollup/rollup-*` 与 esbuild 二进制（换平台会报 `Cannot find module @rollup/rollup-linux-arm64-gnu`）。

另外这三个 Office 包是 UMD，`vite.config.ts` 用 `optimizeDeps.include` 强制预构建；若改成 `exclude`，dev 下浏览器会直接拿到未转换的 CJS，预览会报 `Cannot read properties of undefined (reading 'defineComponent')`。

## 设置页

侧栏底部只有一个「设置」入口，分类导航在设置页**内部**（左侧分栏，窄屏自动变横向）。导航按「越是日常越靠上、越是底层越靠下」分组：

| 分组 | 分类 | 路由 | 用途 | 生效 |
| --- | --- | --- | --- | --- |
| 外观 | 系统外观 | `/settings/brand` | 项目名称、副标题、图标、7 套配色、3 档圆角 | 名称与配色保存即生效；图标上传即生效 |
| 能力 | 模型服务 | `/settings/models` | Embedding 与对话模型 | 保存即生效 |
| 能力 | 联网搜索 | `/settings/web-search` | 搜索服务、站点范围、缓存限制 | 保存即生效 |
| 检索与记忆 | 检索参数 | `/settings/retrieval` | 召回条数、两道闸门阈值、切块、记忆窗口 | 保存即生效（切块只影响新入库文档） |
| 检索与记忆 | 记忆与上下文 | `/settings/memory` | 查看摘要、约束事实、滑出窗口的消息 | 只读 + 可重建 |
| 检索与记忆 | 知识沉淀 | `/settings/drafts` | 问答候选、整理成笔记、采纳入库 | 采纳后进入索引队列 |
| 扩展 | Agent 与技能 | `/settings/agents` | 上传与管理 Agent、技能定义 | 保存即生效 |
| 部署 | 实例配置 | `/settings/instance` | 数据库、向量库、监听地址、数据目录 | **需重启** |

两类配置的行为差异是有意的：

- **存数据库的**（外观、模型、检索、搜索、Agent 定义）改完立刻生效，且都进 `setting_changes` 留痕。
- **存文件的**（实例配置）决定进程怎么启动，只能重启生效。它刻意**不读数据库**——数据库地址改错时，这个页面仍然能打开、能回滚，详见 [`docs/instance-config.md`](docs/instance-config.md)。

旧地址 `/agents` 会重定向到 `/settings/agents`。外观相关（配色取值、加新主题、图标规格）见 [`docs/brand.md`](docs/brand.md)。
