# HTTP / MCP API 参考

本文记录当前代码已实现的接口。业务目标及未实现能力见[需求说明](requirements.md)，配置、启动和部署见[开发手册](development.md)。

## 1. 通用约定

- 默认 HTTP 地址：`http://localhost:8080`。
- 业务 API 前缀：`/api/v1`。
- JSON 请求使用 `Content-Type: application/json`；上传除外。
- 成功响应为 JSON；常见错误格式为 `{"error":"错误信息"}`。500 只返回 `{"error":"internal server error"}`，具体原因记录在服务端 `error.log`。
- 未知路径：以 `/api/` 开头但未匹配的请求返回 404 `{"error":"API not found","started_at":"2026-01-01T00:00:00Z"}`；`started_at` 是该后端进程的启动时间，用于判断请求是否打到了未重新构建的旧进程。非 `/api/` 路径一律返回前端 `index.html`（SPA 路由，如 `/settings/web-search`）。
- 所有响应带 `X-Request-ID`；请求可自带同名头（≤64 字符），服务端透传，否则生成 16 位十六进制 ID。
- 项目级查询均以 `unit_id` 为数据范围。当前没有用户登录和项目授权机制。

## 2. 健康检查

### `GET /health`

响应：

```json
{"status":"ok","database":true}
```

`database` 表示进程是否配置并创建了数据库连接池，不是实时数据库探活结果。

## 3. 业务 API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/units` | 项目列表（含 Agent、技能绑定） |
| GET | `/api/v1/units/meta` | 已有的项目分类与行业，用于下拉建议 |
| GET | `/api/v1/units/profiles?ids=a,b` | 批量读取项目画像（对话页多选知识库） |
| POST | `/api/v1/units` | 新建项目 |
| PATCH | `/api/v1/units/:id` | 编辑项目名称、分类、行业与 Agent/技能绑定 |
| GET | `/api/v1/units/:id/agent-config` | 项目绑定的 Agent、技能与生效工具 |
| GET | `/api/v1/units/:id/graph` | 本地知识关联图谱（项目 → 标签/分类/行业/类型 → 文档） |
| GET | `/api/v1/units/:id/profile` | 项目摘要和计数 |
| GET | `/api/v1/units/:id/hazards?status=` | 项目隐患，可按状态精确过滤 |
| GET | `/api/v1/units/:id/audits` | 项目审核历史，按时间倒序 |
| GET | `/api/v1/documents?q=&unit_id=&limit=&offset=` | 文档库列表（跨知识库，带引用关系） |
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
| GET | `/api/v1/agent/conversations?unit_id=&limit=` | 历史对话列表 |
| GET | `/api/v1/agent/conversations/:id` | 历史对话详情（含全部消息） |
| DELETE | `/api/v1/agent/conversations/:id` | 删除历史对话 |
| GET | `/api/v1/connectors` | 获取 connector 描述列表 |
| GET | `/api/v1/settings/models` | 读取模型服务配置（密钥掩码） |
| PUT | `/api/v1/settings/models` | 保存模型服务配置并立即生效 |
| POST | `/api/v1/settings/models/test` | 测试 embedding/chat 连通性 |
| POST | `/api/v1/settings/models/list` | 拉取上游可用模型列表 |
| POST | `/api/v1/settings/models/providers` | 新增模型来源 |
| PUT | `/api/v1/settings/models/providers/:id` | 更新模型来源 |
| DELETE | `/api/v1/settings/models/providers/:id` | 删除模型来源（同时清理选择） |
| POST | `/api/v1/settings/models/providers/:id/test` | 测试指定来源（`id=0` 用请求体临时值） |
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
- `total` 是两类之和；`failed` 是失败任务数；`chunks`/`orphans` 只统计已索引文档。
- `DELETE /api/v1/documents/:doc_id` 不受知识库范围限制：删除全部 `document_units` 关联，并清理向量与源文件（文件仍被其他记录引用时保留）。
- 只读列表外的操作（查看片段、源文件）仍走 `/units/:id/documents/:filename/...`，用 `units[0]` 作为上下文即可。

### RAG 问答

`POST /api/v1/ask`

请求：

```json
{"unit_id":"demo-001","question":"最近一次检查发现了什么问题？","top_k":5}
```

`top_k` 范围为 1–20；不在范围内时使用 5。`question` 上限 8000 字符；`unit_id` 不存在返回 404。响应：

```json
{
  "answer":"检索资料显示……",
  "citations":[{"filename":"检查报告.docx","doc_type":"检测报告","content":"……","distance":0.18}]
}
```

`distance` 是当前向量库返回的余弦距离，越小通常越相似。回答质量依赖 embedding 和 chat 服务配置。

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

