# HTTP / MCP API 参考

本文记录当前代码已实现的接口。业务目标及未实现能力见[需求说明](requirements.md)，配置、启动和部署见[开发手册](development.md)。

## 1. 通用约定

- 默认 HTTP 地址：`http://localhost:8080`。
- 业务 API 前缀：`/api/v1`。
- JSON 请求使用 `Content-Type: application/json`；上传除外。
- 成功响应为 JSON；常见错误格式为 `{"error":"错误信息","request_id":"…"}`。500 只返回 `{"error":"internal server error","request_id":"…"}`，具体原因记录在服务端 `error.log`，用 `request_id` 可以把用户报错与日志对齐。
- 未知路径：以 `/api/` 开头但未匹配的请求返回 404 `{"error":"API not found","started_at":"2026-01-01T00:00:00Z"}`；`started_at` 是该后端进程的启动时间，用于判断请求是否打到了未重新构建的旧进程。非 `/api/` 路径一律返回前端 `index.html`（SPA 路由，如 `/settings/web-search`）。
- 所有响应带 `X-Request-ID`；请求可自带同名头（≤64 字符），服务端透传，否则生成 16 位十六进制 ID。
- 项目级查询均以 `unit_id` 为数据范围。当前没有用户登录和项目授权机制。

## 2. 健康检查

### `GET /health`

响应：

```json
{"status":"ok","database":true,"version":"0b33d40+dirty"}
```

`database` 表示进程是否配置并创建了数据库连接池，不是实时数据库探活结果。
`version` 来自 Go 构建信息里的 `vcs.revision`（取前 7 位），工作区有未提交改动时追加 `+dirty`；不在 git 仓库中构建时返回 `dev`。它不需要额外的构建参数，用于判断「当前进程跑的是不是最新代码」。

## 3. 业务 API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/units` | 项目列表（含 Agent、技能绑定） |
| GET | `/api/v1/units/meta` | 已有的项目分类与行业，用于下拉建议 |
| GET | `/api/v1/units/profiles?ids=a,b` | 批量读取项目画像（对话页多选知识库） |
| POST | `/api/v1/units` | 新建项目 |
| PATCH | `/api/v1/units/:id` | 编辑项目名称、分类、行业与 Agent/技能绑定 |
| GET | `/api/v1/units/:id` | 单个项目详情（字段与列表项一致，含 `created_at`） |
| DELETE | `/api/v1/units/:id` | 删除项目，仅解除文档绑定，不删除全局文档与向量 |
| GET | `/api/v1/units/:id/agent-config` | 项目绑定的 Agent、技能与生效工具 |
| GET | `/api/v1/units/:id/graph` | 本地知识关联图谱（项目 → 标签/分类/行业/类型 → 文档） |
| GET | `/api/v1/units/:id/profile` | 项目摘要和计数 |
| GET | `/api/v1/units/:id/hazards?status=` | 项目隐患，可按状态精确过滤 |
| GET | `/api/v1/units/:id/audits` | 项目审核历史，按时间倒序 |
| GET | `/api/v1/documents?q=&unit_id=&state=&limit=&offset=` | 文档库列表（跨知识库，带引用关系；`state` 可为 `indexed`/`failed`） |
| GET | `/api/v1/documents/:doc_id` | 文档详情与引用它的知识库 |
| DELETE | `/api/v1/documents/:doc_id` | 彻底删除文档（解除全部关联 + 向量 + 源文件） |
| GET | `/api/v1/units/:id/documents` | 知识库关联文档列表（含 `doc_id`） |
| GET | `/api/v1/units/:id/documents/:filename/chunks` | 查看文档已入库的分块内容 |
| GET | `/api/v1/units/:id/documents/:filename/source` | 查看从上传源文件提取的完整文本 |
| DELETE | `/api/v1/units/:id/documents/:filename` | 从知识库移除文档；无其他引用时清理向量与源文件 |
| POST | `/api/v1/units/:id/web-import` | 抓取公开网页正文并作为文档排队索引 |
| POST | `/api/v1/upload` | 上传文档并启动索引任务 |
| GET | `/api/v1/jobs` | 最近索引任务列表（支持状态过滤） |
| GET | `/api/v1/jobs/:id` | 查询索引任务状态（内存 + `index_jobs`） |
| POST | `/api/v1/jobs/:id/retry` | 重新索引失败/已完成的任务（复用已保存的源文件） |
| POST | `/api/v1/ask` | 检索档案并生成回答 |
| GET | `/api/v1/agent/config` | 获取专家、技能和 connector 描述 |
| GET | `/api/v1/agent/definitions` | 获取内置 + 已上传的 Agent 与技能定义 |
| POST | `/api/v1/agent/experts` | 上传 Agent 定义（JSON/Markdown） |
| POST | `/api/v1/agent/skills` | 上传技能定义（JSON/Markdown） |
| DELETE | `/api/v1/agent/experts/:id` | 删除已上传的 Agent 定义 |
| DELETE | `/api/v1/agent/skills/:id` | 删除已上传的技能定义 |
| POST | `/api/v1/agent/chat` | Agent 对话（工具循环，非流式） |
| POST | `/api/v1/agent/stream` | Agent 对话（SSE 流式） |
| GET | `/api/v1/agent/runs/:id` | 运行详情与工具调用时间线 |
| POST | `/api/v1/retrieve` | 只检索不生成（混合检索 + 重排），用于评测与调试 |
| GET | `/api/v1/agent/conversations?unit_id=&limit=` | 历史对话列表 |
| GET | `/api/v1/agent/conversations/:id` | 历史对话详情（含全部消息） |
| DELETE | `/api/v1/agent/conversations/:id` | 删除历史对话 |
| GET | `/api/v1/connectors` | 获取 connector 描述列表 |
| GET | `/api/v1/settings/models` | 读取模型服务配置（密钥掩码） |
| PUT | `/api/v1/settings/models` | 保存模型服务配置并立即生效 |
| POST | `/api/v1/settings/models/test` | 测试 embedding / chat / rerank 连通性 |
| POST | `/api/v1/settings/models/list` | 拉取上游可用模型列表 |
| POST | `/api/v1/settings/models/providers` | 新增模型来源 |
| PUT | `/api/v1/settings/models/providers/:id` | 更新模型来源 |
| DELETE | `/api/v1/settings/models/providers/:id` | 删除模型来源（同时清理选择） |
| POST | `/api/v1/settings/models/providers/:id/test` | 测试指定来源（`id=0` 用请求体临时值） |

模型角色有三个：`llm`（对话）、`embed`（向量）、`rerank`（重排）。`GET /api/v1/settings/models` 返回三段生效配置与一个 `selection`：

```json
{
  "embed":  {"base_url": "...", "model": "BAAI/bge-m3", "provider_id": 4, "provider_name": "siliconflow"},
  "llm":    {"base_url": "...", "model": "deepseek-ai/DeepSeek-V4.1-Flash", "provider_id": 4},
  "rerank": {"base_url": "...", "model": "BAAI/bge-reranker-v2-m3", "provider_id": 4,
             "enabled": true, "min_score": 0.1},
  "selection": {"llm": 4, "embed": 4, "rerank": 4},
  "providers": [{"id": 4, "name": "siliconflow", "llm_model": "...", "embed_model": "...",
                 "rerank_model": "BAAI/bge-reranker-v2-m3", "active_llm": true, "active_rerank": true}],
  "manual": {"embed": {...}, "llm": {...}, "rerank": {...}}
}
```

- 生效顺序：选中的来源 → 手工配置 → 环境变量。
- `PUT /api/v1/settings/models/selection` 的 `role` 可以是 `llm`、`embed`、`rerank`；`provider_id=0` 表示不使用来源。
- 重排未单独配置（既没有来源也没有手工值）时**跟随对话模型的端点与密钥**，模型名默认 `BAAI/bge-reranker-v2-m3`。
- 重排开关与阈值仍来自环境变量：`RERANK_ENABLED=false` 关闭，`RERANK_MIN_SCORE` 调阈值；二者不进数据库，避免切换来源时被覆盖。
| PUT | `/api/v1/settings/models/selection` | 切换对话/向量模型使用的来源 |
| GET | `/api/v1/settings/web-search` | 读取联网搜索配置（密钥掩码） |
| PUT | `/api/v1/settings/web-search` | 保存联网搜索配置并立即重建搜索客户端 |
| POST | `/api/v1/settings/web-search/test` | 用一次真实查询验证搜索配置 |

数据库未配置时，依赖数据库的接口返回 503。项目不存在时，详情类接口返回 404。

### 项目列表

`GET /api/v1/units`

```json
[
  {"unit_id":"demo-001","name":"示例项目","unit_type":"重点项目","industry":"制造业","expert_id":"kb-expert","skill_ids":["kb-search","kb-review"]}
]
```

`unit_type` 即项目分类。`skill_ids` 为空数组时表示沿用全局默认技能。

### 新建与编辑项目

`POST /api/v1/units`：

```json
{"name":"滨江改造工程","unit_type":"重点项目","industry":"市政","tags":["安全","市政"],"expert_id":"kb-expert","skill_ids":["kb-search"],"tool_names":["vector_search"]}
```

`tool_names` 为项目直接声明的只读工具，留空时按 Agent 与技能推导。

`unit_id` 可省略，服务端生成 `unit-<hex>`。`PATCH /api/v1/units/:id` 接受相同字段的任意子集，未传字段保持不变。名称、分类、行业分别限制为 100、60、60 个字符。

`GET /api/v1/units/meta` 返回已使用的分类与行业，供前端提供候选：

```json
{"categories":["重点项目","专项项目"],"industries":["制造业","市政"]}
```

### 读取与删除项目

`GET /api/v1/units/:id` 返回单个项目。查询与列表共用同一份 SQL（`unitSelectSQL`），所以字段与 `/api/v1/units` 的列表项**完全一致**（含 `document_count`、`chunk_count`、`chapter_count`、`chapters`、`doc_names`、`doc_types`），另带 `created_at`。项目不存在返回 404。

`DELETE /api/v1/units/:id` 删除项目本身，**不删除文档**：

- 随库级联删除：`document_units` 的绑定关系、`index_jobs`、`hazards`、`audit_records`、`kg_entities` / `kg_relations` / `kg_jobs`。
- 保留：`documents`、片段、向量库、`doc_outlines`。它们是全局实体，同一份文档可能被多个项目引用；解绑后的文档仍可在「文档库」中看到（未关联任何知识库），可重新绑定或单独删除。
- 会话里残留的项目引用不清空，检索时已验证返回空结果而不是报错。

响应：

```json
{"deleted":true,"unit_id":"unit-xxx","documents_detached":2}
```

`documents_detached` 表示本次解除了多少份文档的绑定，前端用它提示「文档去哪了」。重复删除返回 404。

### 项目 Agent 配置

`GET /api/v1/units/:id/agent-config` 返回对话页选择项目后应套用的配置：

```json
{"unit_id":"demo-001","expert_id":"kb-expert","skill_ids":["kb-search"],"tool_names":["vector_search"],"tools":["vector_search"]}
```

`tool_names` 是项目显式声明的工具，优先于推导；`tools` 为最终生效工具：显式声明为空时，由技能声明的工具与 Agent 的工具范围求交得出，两者都未声明时返回全部只读工具。`POST /api/v1/agent/chat` 与 `/agent/stream` 在请求未显式传 `expert`/`skills` 时会自动使用项目配置。

### 项目摘要

`GET /api/v1/units/:id/profile`

```json
{
  "unit_id":"demo-001",
  "name":"示例项目",
  "unit_type":"重点项目",
  "industry":"制造业",
  "tags":["安全","市政"],
  "expert_id":"kb-expert",
  "skill_ids":["kb-search"],
  "tool_names":["vector_search"],
  "tools":["vector_search"],
  "audit_count":2,
  "open_hazards":3,
  "indexed_chunks":18,
  "document_count":5,
  "doc_types":{"制度":2,"预案":3},
  "last_indexed_at":"2026-10-20T10:00:00Z"
}
```

这不是正式风险画像；当前响应不含附件方案中的评分、风险档位或业务标签。

### 知识关联图谱

`GET /api/v1/units/:id/graph` 默认返回「知识库 → 标签/分类/行业/类型 → 文档」的元数据图；加 `?source=entity` 返回实体知识图谱（`kg_entities` / `kg_relations`），并可加 `&doc_id=<doc_id>` 只看某份文档涉及的实体与关系（按 `source_ids` 中的 `doc-xxx:index` 前缀过滤，兼容旧格式 `unit:filename:index`）：

```bash
curl '/api/v1/units/unit-xxx/graph?source=entity&doc_id=doc-7a6bfb6fad948db6'
```

实体图响应包含 `doc_id` / `doc_name`，前端「文档库」的「知识图谱」按钮即按此跳转。元数据图返回项目节点、标签、分类、行业、文档类型与文档节点：

```json
{
  "unit_id":"demo-001",
  "nodes":[{"id":"unit:demo-001","label":"示例项目","type":"unit"},
           {"id":"tag:安全","label":"安全","type":"tag"},
           {"id":"type:制度","label":"制度","type":"doc_type"},
           {"id":"doc:a.docx","label":"a.docx","type":"document","weight":12}],
  "edges":[{"source":"unit:demo-001","target":"tag:安全","type":"tag"},
           {"source":"unit:demo-001","target":"type:制度","type":"doc_type"},
           {"source":"type:制度","target":"doc:a.docx","type":"document"}],
  "stats":{"documents":5,"doc_types":{"制度":2},"nodes":4,"edges":3}
}
```

图谱完全基于本地元数据生成，不调用外部服务；`weight` 为文档的 chunk 数。

### 隐患与审核

`GET /api/v1/units/:id/hazards?status=待整改` 返回数组，字段为 `id`、`hazard`、`status`、`severity`、`source`、`occurred_at`。不传 `status` 时返回该项目全部隐患。

`GET /api/v1/units/:id/audits` 返回数组，字段为 `id`、`title`、`score`、`risk_level`、`payload`、`created_at`。

### 上传文档

`POST /api/v1/upload` 使用 `multipart/form-data`：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `file` | 文件 | 是 | TXT、MD、MARKDOWN、JSON、HTML、CSV、DOCX、PDF（优先 `pdftotext`，未安装时内置解析） |
| `unit_id` | 字符串 | 二选一 | 已存在的项目 ID；不存在返回 404；与 `unit_name` 同时提供时优先使用 |
| `unit_name` | 字符串 | 二选一 | 项目名称（≤100 字符）；按名称精确匹配，未找到时自动创建项目 |
| `doc_type` | 字符串 | 是 | 文档类型，由调用方传入 |

校验失败返回 400（缺少字段或空文件）、413（超过 `MAX_UPLOAD_MB`，默认 50 MB）或 415（扩展名不在白名单）。

**上传只负责落盘与入队**：保存文件后立即写入 `index_jobs(queued)` 并返回 202，解析与向量化由 worker 异步完成。解析失败（扫描件无文本层、文件损坏、格式不支持）不影响上传结果，任务会标记为 `failed` 并保留源文件与错误原因；修好环境或换用可解析的文件后，调用 `POST /api/v1/jobs/:id/retry` 复用源文件重新索引，无需重新上传。同样的内容重复上传时，任务会识别出已存在的 `doc_id`，只建立知识库关联并复用已有向量。

```json
{"error":"文档解析失败：……","job_id":"1792572743815471000","filename":"预案.pdf","retryable":true}
```

修好解析环境（安装 `pdftotext`、改用可解析的文件）或模型恢复后，调用 `POST /api/v1/jobs/:id/retry` 即可复用源文件重新索引，无需重新上传。解析成功时结果写入一次性缓存，队列任务直接复用，同一个文件只解析一次。

成功返回 HTTP 202：

```json
{"job_id":"1780000000000000000","unit_id":"demo-001","doc_type":"制度","filename":"检查报告.docx","status":"queued","progress":0,"message":"等待索引","chunks":0}
```

### 索引任务

`GET /api/v1/jobs/:id`

```json
{"job_id":"1780000000000000000","unit_id":"demo-001","doc_type":"制度","filename":"检查报告.docx","status":"running","progress":60,"message":"写出向量索引","chunks":8}
```

`GET /api/v1/jobs?status=queued,running&limit=20` 返回任务列表，用于切换页面后仍能查看进度：

```json
{"count":1,"items":[{"job_id":"1780000000000000000","unit_id":"demo-001","doc_type":"预案","filename":"预案.docx","status":"running","progress":60,"message":"写出向量索引","chunks":8,"created_at":"2026-10-20T11:40:12+08:00","updated_at":"2026-10-20T11:40:14+08:00"}]}
```

`status` 可用逗号分隔多个值，非法值忽略；`unit_id` 可按项目过滤（项目档案页使用）；`limit` 默认 20、上限 100；按创建时间倒序。进程内仍在运行的任务优先返回内存状态，避免读到滞后的持久化快照。未配置数据库返回 503。

`status` 为 `queued`、`running`、`done` 或 `failed`。`progress` 为 0–100 的估算进度；`chunks` 是当前已写入的块数。任务记录同时保存在进程内存和 PostgreSQL `index_jobs`：上传只写入 `queued` 行，后台 worker 领取执行；服务启动时会恢复 `queued` 任务，并只重新排队超过索引超时阈值的陈旧 `running` 任务；活跃任务不会被其他实例抢占。异常退出后的同一任务最多恢复 3 次；索引处理报错会直接变为 `failed`，需排查后重新上传。未知 job 返回 404。

### 知识库文档列表

`GET /api/v1/units/:id/documents` 返回该知识库关联的文档（按索引时间倒序）。文档是独立实体，同一份文档可以挂在多个知识库下：

```json
[{"doc_id":"doc-7a6bfb6fad948db6","filename":"制度.docx","doc_type":"制度","chunk_count":18,"job_id":"1780000000000000000","indexed_at":"2026-10-20T11:44:38+08:00"}]
```

`doc_id` 由正文哈希派生（`doc-<16 hex>`），同样的内容永远得到同一个 id。数据来自 `documents` + `document_units`，只包含成功完成的索引；进行中的任务请配合 `GET /api/v1/jobs?unit_id=<id>` 展示。

`GET /api/v1/units/:id/documents/:filename/chunks?offset=0&limit=10` 返回该文档已入库的分块文本，用于核对索引结果：

```json
{"unit_id":"unit-1","filename":"预案.docx","doc_type":"预案","chunk_count":24,"offset":0,"limit":10,"has_more":true,
 "chunks":[{"index":0,"content":"第一块正文…"}]}
```

`limit` 默认 10、上限 50；分块来自向量库（pgvector `doc_chunks.content` 或 Chroma `documents`），按 `chunk_index` 升序；`has_more` 由多取一条判断，不额外计数。未配置向量库返回 503。

`GET /api/v1/units/:id/documents/:filename/source` 返回 `text/plain; charset=utf-8`，内容由已上传源文件重新提取，供项目详情页查看完整源文档文本。DOCX 和 PDF 使用索引时相同的解析器；PDF 需要 `pdftotext`。若源文件已删除或不可用，返回 404 或通用错误。返回内容按纯文本处理，不执行源文件中的 HTML/脚本。

### 联网搜索配置

`GET/PUT /api/v1/settings/web-search` 读写单行配置，语义与模型设置一致：**数据库记录优先于环境变量**，保存后立即失效缓存并重建搜索客户端。PUT 字段缺省表示不修改；`api_key` 空字符串表示清除。

```json
{"enabled":true,"provider":"tavily","api_key":"…","base_url":"","timeout_ms":15000,
 "cache_ttl_seconds":900,"cache_entries":200,"allow_domains":"gov.cn","deny_domains":"","auto_import":false}
```

读取响应额外返回 `api_key_configured`、`api_key_masked`、`active`（客户端是否真正可用）和 `source`（`database` / `env`）。`POST /settings/web-search/test` 用一次真实查询验证配置，返回 `{"ok":true,"count":n,"provider":"tavily"}`。启用时必须同时提供 `provider` 与 `api_key`，否则返回 400。

### 网页入库

`POST /api/v1/units/:id/web-import`，请求体 `{"url":"https://...","doc_type":"网页资料","filename":"可选自定义文件名"}`。服务端抓取页面正文、写入上传目录并**复用上传索引队列**（返回 202 + 任务对象，可用 `/jobs/:id` 轮询）：

```json
{"job_id":"...","unit_id":"unit-1","doc_type":"网页资料","filename":"Example-Domain.txt","status":"queued","source_url":"https://example.com"}
```

约束：

- 必须先启用联网搜索（`WEB_SEARCH_ENABLED` 或设置页），否则返回 400
- 只允许 http/https；**拒绝解析到内网、回环、链路本地与 CGNAT 地址的目标**，重定向逐跳复检，最多 3 跳（SSRF 防护）
- 仅接受文本类响应（HTML/纯文本等），正文上限 4 MB，去掉脚本/样式后落盘为 `.txt`
- 同一 `source_url` 已入库时返回 `{"imported":false,"duplicate":true,"filename":"..."}`，不会重复索引
- 入库记录的 `documents.source_url` 保留来源，档案页可正常预览与检索

`GET /api/v1/units/:id/documents/:filename/file` 返回上传原件，供前端预览组件渲染：PDF、DOCX、XLS/XLSX、CSV、文本与图片按扩展名返回对应 `Content-Type` 并以 `inline` disposition 提供；其它格式为 `application/octet-stream` 附件下载。追加 `?download=1` 可强制下载。HTML 一律按 `text/plain` 返回，避免同源下执行上传文件中的脚本。此接口仅按 `documents` 表定位文件，并校验文件仍位于配置的上传目录中。

前端「原文预览」使用 `@vue-office/pdf`、`@vue-office/docx`、`@vue-office/excel` 渲染 PDF / Word / Excel（按格式懒加载，不进首屏包），Markdown 用 `marked` + `DOMPurify` 渲染，普通文本按纯文本展示；不支持的格式提示下载原件。

### 删除文档

`DELETE /api/v1/units/:id/documents/:filename`

把文档从该知识库移除。若文档仍被其他知识库引用，只解除关联；不再被任何知识库引用时，连带删除向量、`documents` 记录与源文件。`fully_removed` 标识是否彻底删除：

```json
{"unit_id":"demo-001","filename":"旧版预案.pdf","doc_id":"doc-3f2a...","deleted":true,"fully_removed":true}
```

`filename` 需 URL 编码，且只能是 `filepath.Base` 形式（不含路径分隔符）。不存在的文档返回 404。

### 文档库

文档是独立实体，`/api/v1/documents` 提供跨知识库的全局视图（前端「文档库」页）：

```json
{
  "count": 6, "total": 6, "failed": 1, "chunks": 245, "orphans": 0, "limit": 50, "offset": 0,
  "items": [
    {
      "state": "indexed", "doc_id": "doc-7a6bfb6fad948db6", "filename": "制度.docx",
      "doc_type": "制度", "chunk_count": 39, "indexed_at": "2026-10-21T17:26:09+08:00",
      "ref_count": 1, "units": [{"unit_id": "unit-9ea2...", "name": "公司入围模版"}]
    },
    {
      "state": "failed", "job_id": "1792668...", "filename": "扫描件.pdf", "doc_type": "其他",
      "phase": "parse", "chunk_count": 0, "indexed_at": "2026-10-22T19:10:00+08:00",
      "ref_count": 1, "units": [{"unit_id": "unit-9ea2...", "name": "公司入围模版"}]
    }
  ]
}
```

- 列表同时包含**已索引文档**（`state=indexed`）和**索引失败的任务**（`state=failed`，带 `phase` 与 `job_id`）。失败项可直接调用 `POST /api/v1/jobs/:job_id/retry` 重试，或上传新文件覆盖。
- `q` 按文件名模糊搜索（`ILIKE`）；`unit_id` 只看某个知识库关联的文档。
- `state`（可选）在**服务端**过滤：`indexed` 只返回已索引文档、`failed` 只返回失败任务，其他值返回 400。筛选必须落在服务端，否则分页之后前端只能筛当前页，看起来就像「失败文档消失了」。
- 分页用 `limit`（默认 50，上限 200）+ `offset`。`total` 随 `state` 变化：全部时是两类之和，筛选时是对应一类的总数，可以直接驱动分页控件。
- `failed` 是失败任务**总数**（独立 count 查询，不受页大小影响）；`chunks`/`orphans` 只统计已索引文档。响应另带 `state` 回显。
- `DELETE /api/v1/documents/:doc_id` 不受知识库范围限制：删除全部 `document_units` 关联，并清理向量与源文件（文件仍被其他记录引用时保留）。
- 只读列表外的操作（查看片段、源文件）仍走 `/units/:id/documents/:filename/...`，用 `units[0]` 作为上下文即可。
- 「文档库」页的「索引片段」抽屉即调用 `GET /units/:id/documents/:filename/chunks`（`units[0].unit_id` + `filename`），分页 10 条，仅在 `state=indexed` 时可用；失败项没有片段，只能重试任务。

### RAG 问答

`POST /api/v1/ask`

请求：

```json
{"unit_ids":["demo-001"],"question":"最近一次检查发现了什么问题？","top_k":5}
```

`unit_ids` 为数组，可跨项目提问；也兼容单值 `unit_id`（与 `/retrieve` 同一套参数约定）。`top_k` 范围为 1–20；不在范围内时使用 5。`question` 上限 8000 字符；任一 `unit_id` 不存在返回 404。响应：

```json
{
  "answer": "牵引力不得超过光缆允许张力的 80%[1]。",
  "unit_ids": ["demo-001"],
  "citations": [
    {
      "source_type": "document", "unit_id": "unit-9ea2...", "unit_name": "公司入围模版",
      "doc_id": "doc-7a6bfb6fad948db6", "filename": "1企业管理制度（定稿·排版校订版）.docx",
      "doc_type": "制度", "chunk_index": 3, "content": "……", "distance": 0.34, "score": 0.846,
      "index": 1
    }
  ]
}
```

- **引用编号由服务端分配**：`citations[i].index` 从 1 开始，与资料块里的 `[n]` 标记一一对应；模型只能引用这些编号，不能自己数。与 Agent 的 `index` 字段语义一致。
- 文档是全局实体，只有单项目提问时才在 `citations[].unit_id` 标注来源项目，跨项目时该字段省略。
- `chunk_index` 是片段在文档内的序号，前端据此定位原文；`doc_id` 用于跨知识库共享的文档。
- `distance` 是向量余弦距离（越小越相似），`score` 是重排分（0–1，越大越相关）。两者量纲不同，不要互相比较。
- 没有片段达到阈值时返回 `{"answer": "...没有检索到...", "citations": [], "no_evidence": true, "unit_ids": [...]}`，不再调用大模型，避免编造。

### 检索链路（混合检索 + 重排 + 双层拒答）

`/ask`、Agent 的 `vector_search` 工具、MCP 的 `search` 工具、`/retrieve` 共用同一条链路（实现见 `internal/rag`）：

0. **身份校验**：比对 `documents.embedding_model / embedding_dim` 与当前配置。不一致时**不做检索**，直接返回 503 和可操作的重建命令——两套向量混算不会报错，只会让检索质量整体下滑，所以必须在入口拦住。老索引（未登记身份）放行一次并告警。
1. **并行召回**：向量检索取 `RETRIEVAL_VECTOR_K`（默认 30）；关键词检索走 `chunk_text` 表的 `word_similarity`（pg_trgm，中文按字符三元组匹配）取 `RETRIEVAL_KEYWORD_K`（默认 30）。单路失败自动降级，**全部**通道失败才判定检索失败。
2. **RRF 融合**：按 `1/(60+rank)` 累加两路排名，取前 `RETRIEVAL_FUSION_K`（默认 20）。不依赖分数量纲，异构检索天然可合。
3. **重排**：`BAAI/bge-reranker-v2-m3`（`RERANK_MODEL`，默认沿用对话模型的端点与密钥）对候选打分，低于 `RERANK_MIN_SCORE`（默认 0.1）的丢弃。重排**未配置或调用失败**时回退到融合顺序，并按 `RETRIEVAL_MAX_DISTANCE`（默认 0.55）过滤。
4. **证据闸门**：问题与片段的二元组覆盖率低于 `RETRIEVAL_MIN_OVERLAP`（默认 0.28）时丢弃，用于拦下「问的是库里没有的概念，但文本里恰好有同一个作者名」这类误命中。重排分 ≥ 0.5 时不启用该闸门，避免误杀同义改写。

**两层拒答**：

| 层 | 触发条件 | 表现 |
|---|---|---|
| 检索层 | 全部片段被阈值/闸门过滤 | `/ask` 返回 `no_evidence: true`；Agent 工具返回 `matches: []` 与 `note` |
| 生成层 | 有相关片段但资料不足以回答 | 模型按系统提示明确说明「资料中没有提到 X」，不补常识 |

`POST /api/v1/retrieve` 只做上面 1–4 步、不调用大模型，请求 `{"unit_id":"...","query":"...","top_k":5}`，返回 `matches[]`（含 `doc_id`、`chunk_index`、`distance`、`score`）与 `no_evidence`。

### Agent 配置与对话

`GET /api/v1/agent/config` 返回 `experts`、`skills`、`connectors`。可分别通过 `AGENT_EXPERTS_JSON`、`AGENT_SKILLS_JSON`、`MCP_CONNECTORS_JSON` 配置；页面上传的定义会覆盖同 id 的内置项。`GET /api/v1/connectors` 仅返回 connector 描述。

### Agent 与技能定义

`GET /api/v1/agent/definitions` 返回内置与已上传的定义，每项包含 `id`、`name`、`description`、`tools` 和 `source`（`upload` 表示页面上传，可删除）。

`POST /api/v1/agent/experts` 与 `POST /api/v1/agent/skills` 使用 `multipart/form-data`，字段 `file`，支持 `.json` 或 `.md`（YAML front matter）。单文件上限 1MB；同名 `id` 覆盖旧定义；缺少 `instructions`/`system_prompt` 或 id 含非法字符返回 400。

Agent JSON：

```json
{"id":"project-auditor","name":"项目审核员","description":"结合隐患与审核记录给出建议","system_prompt":"你是项目审核助手，只依据工具返回的数据回答。","tools":["query_hazards","audit_history"]}
```

技能 Markdown：

```markdown
---
id: risk-review
name: 风险复核
description: 复核隐患与审核关注点
tools: query_hazards, audit_history
---

先查看隐患清单，再核对审核记录，引用档案时标注文件名。
```

`tools` 只能取 `get_unit_profile`、`query_hazards`、`audit_history`、`vector_search`，未知名称会被忽略。删除使用 `DELETE /api/v1/agent/experts/:id` 与 `DELETE /api/v1/agent/skills/:id`。

`POST /api/v1/agent/chat` 请求：

```json
{
  "unit_ids":["demo-001","demo-002"],
  "message":"这两个项目有哪些未整改问题？",
  "expert":"kb-expert",
  "skills":["kb-search"],
  "history":[{"role":"user","content":"此前的问题"},{"role":"assistant","content":"此前的回答"}],
  "conversation_id":"conv-1a2b3c"
}
```

`unit_ids`（多选知识库）与旧的 `unit_id` 都支持，同时出现时以 `unit_ids` 为准；`expert`、`skills`、`history`、`conversation_id` 可省略；`message` 必填且上限 8000 字符。仅 `user` 和 `assistant` 历史消息会被使用，服务端只取最近 20 条，每条截断到 4000 字符。`skills` 为空或省略时注入全部已配置技能，传入时只注入其中匹配的技能 ID。`unit_ids` 非空时要求数据库和向量库可用，任一项目不存在返回 404。

指定项目时，Agent 会进入只读工具循环（最多 `AGENT_MAX_STEPS` 步，默认 6）。可用工具由**第一个**项目的 Agent 与技能共同决定，候选为 `get_unit_profile`、`query_hazards`、`audit_history`、`vector_search`，两边都未声明时启用全部。工具接受可选 `unit_id`，取值范围限定为本次已选项目；不传时覆盖全部已选项目，检索结果带 `unit_id`/`unit_name` 标注来源。请求未传 `expert`/`skills` 时自动使用项目配置，模型无法查询已选集合之外的项目。`vector_search` 的实际返回片段受约 20KB 工具预算限制，可能少于 `top_k`；`citations` 只包含实际发送给模型的片段。未指定项目时不提供工具，直接通用对话。

请求体可带 `web_search: true`；当服务端也配置了联网搜索（`WEB_SEARCH_ENABLED=true` + provider + API Key）时，额外提供 `web_search` 工具（检索公开网页，返回标题/摘要/链接）。两个条件缺一都不会注册该工具；该工具不受项目 `tool_names` 约束，因为它不属于任何知识库。未指定项目但开启联网时，只注册 `web_search`。搜索关键词会离开本机，因此默认关闭，并写入 `agent_steps.arguments` 用于审计。响应为：

```json
{
  "answer":"……",
  "expert":"kb-expert",
  "skills":["kb-search"],
  "citations":[{"filename":"检查报告.docx","doc_type":"检测报告","content":"……","distance":0.18}],
  "run_id":"f4a53b17da61f167",
  "steps":2
}
```

`citations` 在未关联项目或没有检索结果时为空数组。`run_id` 可用于在 `agent_runs`/`agent_steps` 中追踪工具调用。外部 connector 仍未作为 MCP client 实际调用。

引用通过 `source_type` 区分来源：

| source_type | 字段 |
| --- | --- |
| `document`（默认） | `unit_id`、`unit_name`、`filename`、`doc_type`、`content`、`distance` |
| `web` | `title`、`url`、`site`、`content`（无 `distance`） |

### 历史对话

`/agent/chat` 与 `/agent/stream` 的请求可带 `conversation_id` 续写同一会话，不传则自动新建。非流式响应返回 `conversation_id`，流式响应在 `run_started` 事件中返回。服务端会把每轮用户消息和助手回答（含引用）写入 `agent_conversations` / `agent_messages`，写入失败只记日志，不影响回答。请求未携带 `history` 且提供了 `conversation_id` 时，服务端会从 `agent_messages` 回填最近的消息，因此 curl / MCP 等无状态客户端也能续接上下文。

`GET /api/v1/agent/conversations` 按 `updated_at` 倒序返回最近会话，支持 `unit_id` 过滤与 `limit`（默认 30，上限 100）：

```json
[{"conversation_id":"conv-1a2b3c","unit_id":"demo-001","unit_ids":["demo-001","demo-002"],"expert":"kb-expert","title":"这两个项目有哪些未整改问题？","message_count":4,"preview":"……","created_at":"2026-10-20T10:00:00Z","updated_at":"2026-10-20T10:05:00Z"}]
```

`GET /api/v1/agent/conversations/:id` 额外返回 `messages`，每条包含 `id`、`role`、`content`、`citations`、`run_id`、`created_at`。`DELETE /api/v1/agent/conversations/:id` 会级联删除消息。

错误状态：模型/Chat API 未配置或数据库未配置返回 503；超过 `AGENT_MAX_STEPS` 或模型返回空回答返回 502；运行超时返回 504；客户端取消返回 499；其他内部错误返回 500（通用错误文本，详情见服务端日志）。

### Agent 流式对话（SSE）

`POST /api/v1/agent/stream`，请求体与 `/agent/chat` 相同。校验失败仍返回普通 JSON 4xx；通过校验后响应 `text/event-stream`，每个事件一行 `event:` + 一行 `data:`（JSON）：

| event | data 摘要 |
| --- | --- |
| `run_started` | `{"run_id":"..."}` |
| `step_started` | `{"step":1}` |
| `tool_call` | `{"step":1,"tool":"vector_search","arguments":"{\"query\":\"...\"}"}` |
| `tool_result` | `{"step":1,"tool":"vector_search","result":"预览","latency_ms":12}`，失败时带 `error` |
| `message_delta` | `{"delta":"增量文本"}` |
| `citations` | `{"citations":[{"filename":"...","doc_type":"...","content":"...","distance":0.12}]}` |
| `run_completed` | `{"run_id":"...","answer":"完整回答","citations":[...],"skills":[...],"steps":2}` |
| `error` | `{"message":"可读错误","run_id":"..."}` |

流开始后的模型/工具错误通过 `error` 事件返回，HTTP 状态仍为 200；客户端断开连接会取消当前模型请求。`tool_result.result` 只是预览，完整工具结果已回填给模型。服务端每 15 秒发送 `: keep-alive` 注释帧，避免代理在工具/模型执行期间断开；客户端解析 SSE 时应忽略注释行。

### 模型服务设置

`GET /api/v1/settings/models` 返回当前生效配置。字段可能来自选中的模型来源、数据库（页面保存）、环境变量或内置默认值，`*_from` 标记来源（`provider` / `db` / `env` / `unset`），API Key 只回显掩码：

```json
{
  "embed": {
    "base_url": "https://api.siliconflow.cn/v1",
    "model": "BAAI/bge-m3",
    "api_key_set": true,
    "api_key_hint": "••••abcd",
    "base_url_from": "db",
    "api_key_from": "db",
    "model_from": "db"
  },
  "llm": { "...": "字段同上" },
  "embedding_dimensions": 1024,
  "updated_at": "2026-10-20T11:02:15.903331+08:00"
}
```

`PUT /api/v1/settings/models` 保存配置，请求字段均可省略：

```json
{
  "embed_base_url": "https://api.siliconflow.cn/v1",
  "embed_api_key": "sk-...",
  "embed_model": "BAAI/bge-m3",
  "llm_base_url": "https://api.siliconflow.cn/v1",
  "llm_api_key": "sk-...",
  "llm_model": "Qwen/Qwen2.5-7B-Instruct"
}
```

- `*_api_key` 省略表示保留原值，传空字符串表示清除（清除后回落到环境变量）。
- `base_url` 必须以 `http://` 或 `https://` 开头且含主机名，否则返回 400。填到 `/v1` 这一层即可，服务端分别拼接 `/embeddings` 和 `/chat/completions`；粘贴完整 endpoint（如 `https://host/v1/chat/completions`）会自动去掉后缀后保存。
- 其余字段传空字符串表示回落到环境变量；`model` 最终回落到内置默认值。
- 保存后立即生效并返回与 GET 相同的结构，无需重启；密钥写入 PostgreSQL `model_settings`。
- 未配置数据库返回 503。

`POST /api/v1/settings/models/test` 使用当前配置发最小请求，HTTP 状态固定 200。请求体可传 `{"kind":"embed"}` 或 `{"kind":"llm"}` 只测对应服务；省略则两个都测，只返回被测的部分：

```json
{
  "embedding": {"ok": false, "latency_ms": 0, "error": "embedding 服务未配置：……"},
  "chat": {"ok": true, "latency_ms": 812}
}
```

`embedding.dimensions` 在成功时返回实际维度；不等于 1024 时 `ok` 为 false，提示重建索引。

`POST /api/v1/settings/models/list` 代理上游 `GET {base_url}/models`，用于「先填地址和 key、再选模型」：

```json
{"kind": "llm", "base_url": "https://token.sensenova.cn/v1", "api_key": "sk-..."}
```

- `kind` 为 `embed` 或 `llm`；`base_url`、`api_key` 省略时使用当前生效配置。`base_url` 同样会去掉 `/chat/completions`、`/embeddings` 后缀。
- 成功返回 `{"kind":"llm","count":7,"models":[{"id":"deepseek-v4.1-flash","name":"…","context_length":1048576,"supported_features":["tools","json_mode"],"output_modalities":["text"]}]}`，按 `id` 升序。
- 缺少 base_url 返回 400；远程地址缺少 api_key 返回 400，本地地址（`localhost`、回环地址、内网 IP、`host.docker.internal`、`*.local`）允许留空。
- 传 `provider_id` 可复用已保存来源的地址与密钥，便于编辑来源时重新拉取列表。
- 上游非 2xx 返回 502 并带上游状态码与错误正文（截断到 300 字符）；返回内容不是 `{"data":[...]}` 结构时也返回 502。

#### 多来源（模型服务提供商）

一个来源 = 一个 OpenAI 兼容端点 + 可选的对话模型、可选的向量模型，可指向本地 Ollama / vLLM / LM Studio 或云端服务。`GET /api/v1/settings/models` 额外返回：

```json
{
  "providers": [
    {
      "id": 1, "name": "本地 Ollama", "base_url": "http://localhost:11434/v1",
      "llm_model": "qwen2.5:7b", "embed_model": "bge-m3",
      "api_key_set": false, "api_key_hint": "",
      "active_llm": true, "active_embed": false, "local_endpoint": true
    }
  ],
  "selection": {"llm": 1, "embed": 0},
  "manual": {"embed": {"base_url": "", "model": ""}, "llm": {"base_url": "", "model": ""}}
}
```

- 生效顺序：选中的来源 → 手工配置（`model_settings`）→ `EMBED_*` / `LLM_*` 环境变量 → 内置默认值。
- `selection` 中 `0` 表示不指定来源；`manual` 是未被来源覆盖的手工配置原值，供编辑页回填。
- `POST /api/v1/settings/models/providers` 建来源，必填 `base_url`，且 `llm_model` / `embed_model` 至少填一个：

```json
{"name":"本地 Ollama","base_url":"http://localhost:11434/v1","api_key":"","llm_model":"qwen2.5:7b","embed_model":"bge-m3"}
```

- `PUT /api/v1/settings/models/providers/:id` 字段均可省略，`api_key` 省略表示保留、空字符串表示清除。
- `PUT /api/v1/settings/models/selection` 切换角色来源：`{"role":"llm","provider_id":1}`，`provider_id=0` 表示回落到手工配置。来源缺少对应模型时返回 400（例如只配了对话模型的来源不能作为向量来源）。
- `POST /api/v1/settings/models/providers/:id/test` 用该来源配置发最小请求，`kind` 可省略（两个都测）；`id=0` 时使用请求体里的 `base_url` / `api_key` / 模型，用于保存前试连。
- 删除来源会同时清除指向它的 `model_selection` 记录，相关角色回落到手工配置。
- 建表由服务启动时自动补齐（`model_providers`、`model_selection`），也可用 `deploy/schema.sql` 手工初始化。

## 鉴权（可选）

配置环境变量 `API_TOKEN` 后，所有 `/api/` 请求需要携带 `Authorization: Bearer <token>` 或 `X-API-Token: <token>`，否则返回 401。未配置时不校验，适合本地开发；`/health` 与静态资源始终放行，`/mcp` 使用自己的 `MCP_AUTH_TOKEN`。

## 4. MCP Streamable HTTP

Endpoint：`POST /mcp`。请求必须带：

```http
Authorization: Bearer <MCP_AUTH_TOKEN>
Content-Type: application/json
```

未配置服务端 `MCP_AUTH_TOKEN` 时返回 503；缺失或错误 token 返回 401。服务端接受 `2026-07-28` 协议路径及 `2025-11-25` 兼容路径。GET `/mcp` 当前返回 405。

只读工具：

| 工具 | 必需参数 | 可选参数 | 结果 |
| --- | --- | --- | --- |
| `list_units` | 无 | 无 | 项目列表 |
| `get_unit_profile` | `unit_id` | 无 | 项目摘要和计数 |
| `query_hazards` | `unit_id` | `status` | 隐患列表 |
| `audit_history` | `unit_id` | 无 | 审核历史 |
| `vector_search` | `unit_id`、`query` | `top_k`（1–20，默认 5） | 相关档案片段、文件名、文档类型及距离 |

每个 MCP 工具调用都必须按 `unit_id` 过滤。无数据库或业务错误会作为 MCP tool result 的错误内容返回（依赖细节如数据库/模型错误统一返回 `internal error`，详细原因写入服务端日志）；MCP JSON-RPC 层可能仍是正常 HTTP 响应，客户端应检查工具结果是否为错误。

## 5. 索引参数与存储语义

- 默认 embedding 模型：`BAAI/bge-m3`；必须返回 1024 维向量。
- chunk：1800 Unicode 字符，重叠 240 字符；embedding 请求每批最多 16 段。
- 向量写入经 `internal/vectorstore.Store`；支持 pgvector 和 Chroma v2 REST adapter。Chroma 使用 `CHROMA_URL`、`CHROMA_TENANT`、`CHROMA_DATABASE`、`CHROMA_COLLECTION` 配置；项目过滤写入 metadata，并用于检索和计数。
- pgvector 唯一键 `(unit_id, filename, chunk_index)` 冲突时更新文档类型、内容和向量。
- 同名文件重新索引完成写入后，会删除 `chunk_index >= 新 chunk 数` 的旧记录；Chroma 使用 `unit_id` + `filename`（+ `chunk_index`）metadata filter 执行同样的删除。
- 更换 embedding 模型、维度或语义前必须重建索引；不能在同一索引内混用模型版本。
- Agent 运行追踪不在向量库中：`agent_runs` 记录 run 状态/token/步数，`agent_steps` 记录工具名、参数、结果预览和耗时；服务启动时仅将超过 Agent 最大运行时限的陈旧 `running` 更新为 `interrupted`。


## 6. 内容框架、记忆与运行时配置

以下接口都不在早期文档里，按功能分组。路径前缀同前：`/api/v1`。

### 内容框架与示例问题

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/units/:id/outline` | 该知识库下所有文档的章节结构 |
| `POST` | `/units/:id/outline/extract` | 强制重抽（索引完成时会自动抽一次） |
| `GET` | `/units/:id/examples` | 示例问题，**每条都经过真实检索验证** |
| `GET` | `/units/:id/tree` | 目录树：文档 → 章节 → 片段预览 |

`outline` 的响应：

```json
{
  "total": 161,
  "missing": 0,
  "documents": [
    { "doc_id": "…", "filename": "1企业管理制度.docx", "rule_version": "v5",
      "entries": [{ "title": "总则", "level": 1, "chunk_index": 0 }] }
  ]
}
```

`level` 是层级：1 章、2/3 条。抽取规则见 `outlineRuleVersion`，改规则要同步升版本号，否则老文档不会重抽。

`examples` 的参数 `limit`（默认 4）。每条带 `validated: true`——生成时会用**当前检索参数**实跑一次，`no_evidence` 的直接丢弃。所以示例数量可能少于 `limit`，也可能为空（文档太短或结构太弱）。

`tree` 一次性返回文档、章节和每块 140 字预览；正文按需走 `/units/:id/documents/:filename/chunks`。

### 会话记忆

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/agent/conversations/:id/memory` | 摘要 + 有效事实 + 已失效事实 + 当前窗口参数 |
| `POST` | `/agent/conversations/:id/memory/rebuild` | 清空并按当前窗口重算（会调一次模型，同步等待） |

响应要点：

```json
{
  "summary": "用户最初只看消防…后把范围放宽到质量管理",
  "summary_upto": 132,
  "facts": [{ "id": 9, "kind": "constraint", "topic": "scope", "content": "…",
              "source_message_id": 129, "active": true }],
  "history": [{ "id": 3, "content": "只看消防部分", "superseded_by": 9 }],
  "enabled": true,
  "window": { "messages": 20, "runes": 4000 }
}
```

`facts` 只含 `active=true` 的；`history` 是被同主题新说法取代的旧版本（保留而不是删除，用于解释「模型为什么换了答法」）。`source_message_id` 指回原话，供页面上「第 N 条」跳转。

### 知识沉淀（问答 → 草稿 → 采纳 → 回到检索）

会话记忆解决「别忘了用户说过什么」，知识沉淀解决「这次问答解决了什么，以后还能不能用」。两者是不同层：记忆只进 prompt，沉淀经人工采纳后才进入知识库。

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/kb/drafts?status=&limit=` | 草稿列表；`status` 为 `pending` / `accepted` / `merged` / `dismissed`，空为全部 |
| `POST` | `/kb/drafts/distill` | 把待审核问答整理成一篇主题笔记（手动，无数量门槛） |
| `POST` | `/kb/drafts/:id/accept` | 采纳：写成 Markdown 并进入索引队列 |
| `POST` | `/kb/drafts/:id/dismiss` | 忽略该草稿 |

**什么时候会产生草稿**：`/agent/chat` 与 `/agent/stream` 成功后异步沉淀，且必须同时满足——选了知识库、回答非空、不是拒答、引用里至少一条来自文档（纯网页答案不沉淀）。`content_hash` 按「知识库 + 问题 + 回答」去重，同一问答问多次只留一条。

**状态机**：

```
pending ──采纳──> accepted（生成 draft-<id>-<kind>.md，走 index_jobs 入库）
pending ──忽略──> dismissed
pending ──整理──> merged（原文并入一条 kind=note 的新草稿，原问答不再单独出现）
```

- **采纳才入库**：未采纳的内容永远只是草稿，不参与检索，避免模型输出自我强化。
- 采纳复用现有索引队列：切块、嵌入、向量身份、章节抽取与普通上传完全一致；生成的文档 `doc_type` 为「问答沉淀」，可在文档库中预览、删除。
- 文档头部固定写明「本文由历史问答整理生成，不是原始文档」，并附引用来源清单，便于日后核对。
- 采纳是幂等的：已处理的草稿再采纳返回 409；原知识库已删除时返回 409 并说明。
- 整理（distill）只产生**新草稿**，不自动入库；原问答标记 `merged`，避免同一内容被采纳两次。

**定期整理**：服务每小时检查一次，待审核问答满 8 条才自动整理一次（只产草稿）。手动调用 `/kb/drafts/distill` 无门槛，但当前没有待审核问答时返回 `{"merged":0}`。

**一个已知副作用**：沉淀文档是针对某个问题写的，命中同类问题时往往排在原文之前（实测 `draft-1-qa.md` 得分 1.0，原文 0.968）。这正是需要人工采纳把关的原因；若希望原文始终优先，可在检索层对 `doc_type='问答沉淀'` 的片段降权，目前未做。

### 检索参数与留痕


| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/settings/retrieval` | 当前生效参数 + 取值范围 + 最近变更 |
| `PUT` | `/settings/retrieval` | 保存（只传要改的字段） |
| `GET` | `/retrieval/traces?limit=20` | 每次检索的分层计数 |

`PUT` 的字段：`vector_k` `keyword_k` `fusion_k` `max_distance` `min_overlap` `min_score` `chunk_size` `chunk_overlap` `memory_messages` `memory_runes` `memory_summary`(bool) `outline_enabled`(bool)。

参数存 `retrieval_settings` 单行表，**优先于环境变量**；每次保存与旧值逐项比对，写入 `setting_changes`。切块参数只影响新入库文档。

`traces` 每行记录 `doc_count / vector_hits / keyword_hits / fused / reranked / kept / no_evidence / degraded / duration_ms`。它的用途是回答「为什么这次没答上来」：召回为 0 是检索问题，`reranked=0` 是精排阈值问题，`kept=0` 而 `reranked>0` 是覆盖率闸门问题。

`/retrieve` 的响应里也带同一组 `stats`，所以参数页可以不查留痕表就地试算。

### 品牌与外观

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/brand` | 名称、副标题、浏览器标题、图标、主题、圆角 |
| `PUT` | `/brand` | 保存（只传要改的字段） |
| `POST` | `/brand/logo` | 上传图标（multipart，字段名 `file`） |
| `GET` | `/brand/logo` | 返回自定义图标，URL 带内容哈希 |

`PUT` 可传 `app_name` `app_subtitle` `browser_title` `logo_source`(传 `builtin` 即恢复默认) `theme` `radius`。

- `theme` 白名单：`graphite` `indigo` `azure` `pine` `amber` `violet` `rose`
- `radius` 白名单：`compact` `standard` `soft`

非法值返回 400。图标只接受 PNG / JPEG / WebP（按**魔数**判断，不看扩展名），上限 1 MB；**拒绝 SVG**——可内嵌脚本且该地址能被直接打开。文件名带 SHA-256 前缀，换图必换 URL，因此响应可长期缓存。

`logo_url` 为空表示使用内置矢量标记。

### 实例配置

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/instance/config` | 当前配置 + 每项来源 + 待重启项 |
| `PUT` | `/instance/config` | 写入 `data/instance.env` |
| `POST` | `/instance/config/test-db` | 用给定参数试连 PostgreSQL |
| `POST` | `/instance/config/test-vector` | chroma 心跳 / pgvector 走数据库 |
| `POST` | `/instance/config/rollback` | 用 `.bak` 覆盖当前配置 |

**这组接口不依赖数据库连接**：`PG_DSN` 改错时，它们仍然可用，这是唯一的自救入口。见 [`instance-config.md`](instance-config.md)。

`GET` 响应：

```json
{
  "config_file": "./data/instance.env", "env_file": ".env",
  "saved": true, "has_backup": true,
  "database": { "host": "127.0.0.1", "port": "5433", "database": "fire_archive",
                "user": "postgres", "sslmode": "disable",
                "password_set": true,
                "dsn_masked": "postgresql://postgres:***@127.0.0.1:5433/fire_archive?sslmode=disable" },
  "vector": { "store": "chroma", "url": "…", "tenant": "…", "database": "…", "collection": "…" },
  "runtime": { "listen_addr": "127.0.0.1:8081", "upload_dir": "./data/uploads", "brand_dir": "./data/brand" },
  "sources": { "PG_DSN": "instance", "VECTOR_STORE": "env_file", "BRAND_DIR": "default" },
  "pending": ["UPLOAD_DIR"], "locked": []
}
```

`sources` 取值 `instance`（页面保存）/ `env_file`（.env）/ `default`（代码默认）/ `external`（进程环境变量注入，页面改不动）。`pending` 是「已写入文件但当前进程还没用上」的键。`locked` 里的键页面会禁用输入。

`PUT` 的密码语义特殊：**留空表示不修改**。因为明文从不回传，这是唯一不会把密码清空的表达方式。

`PUT` 默认会先试连数据库，失败返回 400 且带 `can_force: true`；确认要写入时重发并带 `"force": true`。

### 其他

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/ingest/config` | 上传页展示用：`max_upload_mb` `chunk_size` `chunk_overlap` `extensions` |
| `GET` | `/units/:id/kg/status` | 图谱抽取状态 |
| `POST` | `/units/:id/kg/extract` | 触发图谱抽取（耗时操作，异步） |

`/ingest/config` 让前端不再自己维护一份切块与大小限制，避免页面文案与后端实现脱节。
