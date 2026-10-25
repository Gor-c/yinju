# 开发与部署手册

## 1. 技术栈与模块边界

### 1.1 系统架构图

> 端到端的七条流程（总览 / 入库 / 问答 / 检索五层 / 记忆 / 沉淀闭环 / 配置生效）见 [完整流程图](flows.html)。它是一个零依赖的静态页面，离线可开、可直接打印；本节只保留三张架构图，因为架构与流程是两个维度。

三张图分开画：**包与依赖**、**两条链路**、**记忆注入**。合并成一张会把三个不同维度的信息压在同一平面上，反而看不出重点。图里只画已上线的通路，未接线的扩展点在 [1.1.1](#111-分层与扩展点) 说明。

#### 总览：包边界与依赖方向

依赖是单向的：`app → rag → vectorstore`、`app → agent`。这个方向的收益是替换成本，不是好看——详见 1.1.1。

```mermaid
flowchart TB
    subgraph clients["使用方"]
        Browser["浏览器 · Vue 3"]
        Host["WorkBuddy / 外部 Agent"]
    end

    subgraph svc["Go 服务 · 单二进制"]
        subgraph app["internal/app 装配层"]
            API["Gin /api/v1"]
            MCP["MCP endpoint /mcp"]
            Worker["索引 Worker"]
            Runtime["Agent 运行时 · SSE"]
            Cfg["配置与留痕"]
        end
        subgraph core["核心层"]
            Pipe["rag.Pipeline"]
            Loop["agent.Loop + Catalog"]
        end
        VS["vectorstore.Store"]
    end

    subgraph ext["存储与模型"]
        PG[("PostgreSQL")]
        Vec[("Chroma / pgvector")]
        Model["对话 / 嵌入 / 重排"]
        Files[("原件 · 图标 · instance.env")]
    end

    Browser --> API
    Host --> MCP
    API --> Worker
    API --> Runtime
    API --> Cfg
    Runtime --> Loop
    Loop --> Pipe
    Pipe --> VS
    VS --> Vec
    Pipe --> PG
    Pipe --> Model
    Loop --> Model
    Worker --> Pipe
    Worker --> PG
    Worker --> Files
    Cfg --> PG
```

#### 两条链路：入库与问答共用一套嵌入与身份

两条链路看起来是两件事，实际上共用同一个 `Embedder` 与同一份身份三元组（`embedding_model / embedding_dim / chunker`）。这是整套分层里**唯一的硬约束**：一旦两侧不一致，检索到的向量就不在同一个空间里，而系统不会报错，只会慢慢变差。

```mermaid
flowchart LR
    subgraph ingest["入库链路 · 离线、可重建"]
        direction LR
        U["上传 / 网页导入"] --> P["解析 · 切块 1800/240"]
        P --> E["嵌入"]
        E --> W[("向量库")]
        P --> O[("doc_outlines")]
    end

    subgraph ask["问答链路 · 在线"]
        direction LR
        Q["提问"] --> R1["向量召回"]
        Q --> R2["关键词召回 pg_trgm"]
        R1 --> F["RRF 融合"]
        R2 --> F
        F --> RR["精排 + 分数阈值"]
        RR --> G["覆盖率闸门"]
        G --> C["注入上下文 · 服务端编号"]
        G -.->|"全空"| N["拒答：资料未提及"]
    end

    E -.->|"同一 Embedder、同一身份"| R1
```

召回只有 `vector` 与 `keyword` 两路。知识图谱（`/units/:id/graph`）是另一条独立抽取管线，产物用于浏览与排查，**不参与检索**；联网搜索是工具，也不是召回通道。这两点容易被误当成"第三路召回"。

#### 记忆注入：只改上下文，不改证据

```mermaid
flowchart LR
    L1["L1 窗口原文"] -->|"滑出后异步压缩"| L2["L2 滚动摘要 summary_upto"]
    L2 -->|"同一次调用产出"| L3["L3 原子事实 topic/active"]
    L1 --> M["记忆块 · 只用于理解上下文"]
    L2 --> M
    L3 --> M
    M --> P["prompt"]
    R["检索片段 1..n · 服务端分配编号"] --> P
    P --> X["模型"]
```

记忆块在 system prompt 的 10 条工具规则**之后**单独成块追加，理由写在 `agent_runtime.go` 的注释里：它既有要遵守的约束，也有不能当证据的摘要，两者必须让模型分得清。**摘要与事实永远不带引用编号。**

三张图之外，还有几条贯穿全局、画不进图里的约定：

`vectorstore.Store` 是向量库替换边界（pgvector / Chroma v2）。文档是独立实体：`documents(doc_id)` + `document_units(doc_id, unit_id)` 表达「一份文档可被多个知识库引用」，向量只按 `doc_id` 存一份。检索时先把知识库集合翻译成 `doc_id` 集合，再过滤向量库。模型配置支持多来源（`model_providers` + `model_selection`），**对话、向量、重排**三个角色可分别指向不同端点（本地 Ollama 或云端）；重排未单独配置时跟随对话来源。内置 Agent 与 MCP 复用同一组工具实现，但分别走 `/agent/*` 与 `/mcp`。

图里有三处值得单独说明，因为它们决定了「改动落在哪」：

- **检索是一条独立的流水线**（`rag.Pipeline`）。上传侧的嵌入与检索侧的查询向量走的是同一个 `Embedder`、同一份 `Identity`（模型 + 维度）；两者不一致会直接拒绝检索，而不是让两套向量静默混算。
- **会话记忆不参与检索**。它只在组装 prompt 时注入，作用是「别忘掉用户说过什么」；摘要与事实被明确标注为不可作为回答依据，也不进引用编号。
- **配置分两条通道**：数据库里的随保存生效，`instance.env` 里的要重启（且 `PG_DSN` 这类引导期配置只能放文件，否则改错就再也打不开页面）。见 [1.1.2](#112-配置的两条通道)。

图外还有四条已上线但没画进来的通路，它们都在 `internal/app` 里，画进去会让总览失焦：

| 通路 | 入口 | 说明 |
|---|---|---|
| 内容框架 / 目录树 | `/units/:id/{outline,tree,examples}` | 从切块反推章节结构，规则版本落 `doc_outlines` |
| 知识图谱 | `/units/:id/graph`、`/kg/extract` | 独立抽取，用于浏览；不进检索 |
| 联网搜索 | 工具 `web_search` | 仅在知识库无答案时用，出处另计 |
| 评测 | `cmd/eval` + `eval/questions.json`（模板见 `questions.example.json`） | 离线跑，不进服务进程 |

### 1.1.1 分层与扩展点

代码按「一个包一件事」切分，依赖方向单向：`app → rag → vectorstore`、`app → agent`。

| 包 | 职责 | 不该出现的东西 |
|---|---|---|
| `internal/rag` | 检索编排：切块、嵌入、多路召回、RRF 融合、重排、证据判定 | SQL、HTTP、环境变量、日志 |
| `internal/agent` | 工具循环与工具目录：消息转录、并发执行、步数上限、`Catalog` | 业务查询、向量检索 |
| `internal/app` | 装配层：HTTP、数据库、模型客户端，把上面两层接起来 | 检索排序算法 |
| `internal/vectorstore` | 向量读写边界（pgvector / Chroma） | 检索策略与阈值 |

分层的实际收益是**替换成本**，不是好看。检索链路的四个可替换点：

| 接口 | 方法 | 换掉它意味着 | 现有实现 |
|---|---|---|---|
| `rag.Embedder` | `Embed(texts) ([][]float64, error)` | 换嵌入模型或服务商 | `Server.Embed` → OpenAI-compatible `/embeddings` |
| `rag.Channel` | `Fetch(ctx, Request) ([]Match, error)` | 加/减一路召回 | `vector`（向量）、`keyword`（pg_trgm） |
| `rag.Reranker` | `Rerank(query, docs) ([]float64, error)` | 换精排模型 | `httpReranker` → `/rerank` |
| `rag.Chunker` | `Chunk(text) []Chunk` | 换切块策略（按段落、按条款） | `FixedChunker`（1800 / 240） |

加一路召回不改编排逻辑，在 `internal/app/retrieval.go` 的 `Channels` 里 append 一项即可。下面是**示意**：`graph` 这一路目前还没有接线（知识图谱是独立抽取产物，见 [1.1](#11-系统架构图)），`s.graphSearch` 需要自行实现，签名可参照 `s.keywordSearch`。

```go
{
    Name: "graph",
    Fetch: func(ctx context.Context, req rag.Request) ([]rag.Match, error) {
        return s.graphSearch(ctx, req.DocIDs, req.Query, tuning.KeywordK)
    },
},
```

权重不用调：融合走 RRF（只看排名，不看分数量纲），新增一路不会把原有排序整体推偏。
单路失败自动降级，只有**全部**通道失败才判定检索失败。

工具的插拔点在 `internal/app/agent_tools.go`：

- 内置工具在 `builtinTools` 里声明一次（元数据 `spec` + 构造函数 `build`），
  白名单、展示顺序、上传定义校验全部由它派生，**不维护第二份清单**。
  新增内置工具 = 往 `builtinTools` 加一个元素。
- 外部来源（MCP 连接器、租户自定义工具）在 `toolCatalog` 里注册即可：
  `Add` 受技能白名单裁剪，`AddAlways` 不受裁剪（用于用户本轮显式开启的运行时能力，如联网搜索）。
- 提示词里的「本次可用工具」清单与实际注册集合同源，不会出现「提示词说有、调用时没有」。

**向量空间身份**是这套分层里唯一的硬约束：`documents` 记录
`embedding_model / embedding_dim / chunker`。检索前比对，不一致直接返回
`rag.IdentityMismatch`（错误信息带重建命令），而不是让两套向量静默混算；
重新上传同名文档时也先比对身份，不一致走重建而非复用旧向量。
老索引（未登记身份）放行一次并告警，重建后自动受保护。

### 1.1.2 配置的两条通道

系统里的配置分两类，分界线不是「重不重要」，而是**改动能不能热生效**——而这又取决于它存在哪。

| | 存数据库 | 存文件 |
|---|---|---|
| 载体 | `brand_settings`、`model_settings`、`retrieval_settings`、`web_search_settings`、`agent_experts/skills` | `data/instance.env`（页面写入）、`.env`（人工维护） |
| 内容 | 外观、模型、检索参数、联网搜索、Agent 定义 | 数据库地址、向量库类型、监听地址、数据目录 |
| 生效 | 保存即生效（缓存 5 秒或主动失效） | 需重启进程 |
| 留痕 | `setting_changes`（旧值 → 新值） | 文件本身 + `.bak` 备份 |
| 入口 | 对应设置页 | 设置 → 部署 → 实例配置 |

**为什么数据库地址不能存在数据库里。** 如果 `PG_DSN` 存在 PG 里，改错一次就连不上；连不上就打不开页面；打不开页面就只能 ssh 到服务器改文件。所以「引导期配置」（bootstrap）必须落在文件里，而且**读取它的 handler 不能依赖数据库连接**——`internal/app/instance.go` 的全部接口都遵守这条，这不是顺手做的好意，而是功能成立的前提。

新增一项配置时，用两个问题定位置：

1. **它影响的是「怎么用」还是「怎么起」？** 检索行为、提示词、外观属于前者 → 数据库；连接哪个库、听哪个端口、文件放哪属于后者 → 文件。
2. **改错了会不会让服务起不来？** 会 → 文件 + 页面标注「需重启」+ 保存前实测一次；不会 → 数据库，保存即生效。

文件配置的优先级链（`loadEnvFile` 对已存在的变量跳过，所以先加载的赢）：

```
进程环境变量（systemd / docker -e）> data/instance.env（页面写入）> .env（人工维护）> 代码默认值
```

这条链决定了页面上的「来源」标注：一个键如果被外部环境变量占住，页面会把它标成「环境变量」并禁用输入框——因为改了也不会生效。

### 1.2 端到端流程总览（配置 → 入库 → 问答）

```mermaid
flowchart LR
    subgraph config[配置：三个模型角色]
        C1[选中的来源<br/>llm / embed / rerank] --> C2[手工配置]
        C2 --> C3[环境变量]
        C3 --> C4[默认值]
    end

    subgraph ingest[入库：只收文件，异步处理]
        U1[POST /upload] -->|202 + job_id| U2[(index_jobs<br/>queued / pending)]
        U2 --> U3[worker: phase=parse<br/>提取文本]
        U3 -->|无文本层| UF[failed phase=parse<br/>保留源文件]
        U3 --> U4{doc_id 已存在}
        U4 -->|是| U5[只建立知识库关联<br/>复用已有向量]
        U4 -->|否| U6[phase=index<br/>切块 + embedding]
        U6 --> U7[(向量库按 doc_id<br/>+ chunk_text 关键词镜像)]
        U6 -->|失败| UI[failed phase=index<br/>可重试]
    end

    subgraph qa[问答：身份校验 + 两路召回 + 精排 + 双层拒答]
        Q1[用户问题] --> Q0{向量身份一致?}
        Q0 -->|否| QE[503 + 重建索引命令]
        Q0 -->|是| Q2[向量召回 top30]
        Q1 --> Q3[关键词召回 top30<br/>pg_trgm]
        Q2 --> Q4[RRF 融合取 20]
        Q3 --> Q4
        Q4 --> Q5[rerank 打分<br/>bge-reranker-v2-m3]
        Q5 --> Q6{有片段达到阈值}
        Q6 -->|否| Q7[检索层拒答<br/>no_evidence，不调模型]
        Q6 -->|是| Q8[生成回答 + 引用]
        Q8 -->|资料不足| Q9[生成层拒答<br/>明确说没有提到]
        Q8 --> Q10[引用定位到第 N 块]
    end

    config -.提供模型.-> ingest
    config -.提供模型.-> qa
    U7 -.已索引片段.-> qa
```

三个阶段各自回答一个问题：

| 阶段 | 回答的问题 | 关键产物 |
|---|---|---|
| 配置 | 用哪个模型、从哪来 | `model_providers` / `model_selection` / `model_settings`，三层回落 |
| 入库 | 这份文件能不能用 | `index_jobs.phase`、`documents`、向量库、`chunk_text` |
| 问答 | 有没有依据、依据在哪 | `citations`（含 `doc_id`、`chunk_index`、`score`） |

三条入口（`/ask`、Agent 对话、MCP）共用同一套检索与拒答，区别只在调用方式：

| 入口 | 差异 |
|---|---|
| `POST /api/v1/ask` | 单轮检索 + 一次生成，同步返回 |
| Agent 对话（SSE） | 工具循环，`vector_search` 可多次调用，最多 6 步；最后一步禁用工具强制收尾 |
| MCP | 与 Agent 同一组工具实现，供外部客户端调用 |

关键参数（都有默认值，改完跑 `go run ./cmd/eval` 验证）：

| 参数 | 默认 | 作用 |
|---|---|---|
| 切块 / 重叠 | 1800 / 240 | 切块粒度 |
| 批量 embedding | 16 条/批 | 索引吞吐 |
| `RETRIEVAL_VECTOR_K` / `RETRIEVAL_KEYWORD_K` | 30 / 30 | 两路召回条数 |
| `RETRIEVAL_FUSION_K` | 20 | 融合后送入重排的条数 |
| `RERANK_MIN_SCORE` | 0.1 | 低于即丢弃 |
| `RETRIEVAL_MIN_OVERLAP` | 0.28 | 二元组覆盖率闸门 |
| `RETRIEVAL_MAX_DISTANCE` | 0.55 | 重排不可用时的距离兜底 |
| `AGENT_MAX_STEPS` | 6 | 工具循环上限 |

降级与失败路径（均已实测）：

| 故障 | 行为 |
|---|---|
| rerank 接口不可达 | 回退融合顺序，按距离 ≤ 0.55 过滤，回答照常 |
| embedding 失败、关键词成功 | 用关键词结果继续 |
| 所有片段被阈值过滤 | 判为无依据，直接拒答 |
| 模型跑满工具步数 | 最后一步禁用工具，给出部分结论而非报错 |
| 模型 429 / 5xx | 1s→3s→9s 退避重试；已推送增量则不重试 |
| 解析或索引失败 | `phase` 停在出错阶段，源文件保留，可重试 |

### 1.3 上传与索引流程（上传 → 解析 → 索引）

```mermaid
flowchart TD
    A[POST /api/v1/upload] -->|扩展名 415 / 大小 413 / 知识库 404| Rej[拒绝]
    A --> B[保存源文件到上传目录]
    B --> C[(index_jobs<br/>status=queued<br/>phase=pending)]
    C -->|202 + job_id| UI[前端轮询任务状态]
    C --> W[Worker 领取<br/>FOR UPDATE SKIP LOCKED]
    W --> P[phase=parse<br/>提取文本]
    P -->|无文本层 / 文件损坏| F1[failed phase=parse<br/>保留源文件]
    P -->|提取成功| H{doc_id 已存在<br/>且 chunk_count 大于 0?}
    H -->|是| R[复用已有向量<br/>只建立知识库关联<br/>done]
    H -->|否| I[phase=index<br/>切块 1800 / overlap 240]
    I --> E[批量 embedding 16 条/批]
    E -->|429 / 5xx 重试 3 次后仍失败| F2[failed phase=index<br/>保留源文件]
    E --> S[写向量 doc_id + documents + document_units]
    S --> D[done 100%]
    F1 --> RT[POST /jobs/:id/retry]
    F2 --> RT
    RT -->|status=queued, phase=pending| C
```

任务分三个阶段，`index_jobs.phase` 记录当前阶段，失败时停留在出错阶段（前端显示「解析失败」或「索引失败」）：

| phase | progress | 说明 |
|---|---|---|
| `pending` | 0 | 已上传、排队等待 worker |
| `parse` | 5–10 | 解析文本（PDF 优先 `pdftotext`，回退纯 Go；扫描件无文本层则失败） |
| `index` | 25–95 | 切块、批量 embedding、写向量与文档记录 |
| `done` | 100 | 完成 |
| `failed` | 保留 | 保留出错阶段与原因，源文件不删，可重试 |

上传本身**不做解析**，任何白名单格式都返回 202；解析与索引失败只体现在任务状态里。同样的正文（`content_hash` 派生 `doc_id`）重复上传时，任务会识别出已存在的文档，只建立 `document_units` 关联并复用向量，不重复解析与 embedding。同一知识库下同名但内容不同时，旧版本会被自动解绑并在无其他引用时清理。

队列语义：worker 每 2 秒轮询 + 上传后 `wakeWorker()` 立即触发；`attempts` 限制同一任务被领取的次数，进程重启只回收超过「任务超时 + 1 分钟」的陈旧 `running` 任务，避免多实例互抢；同一 `unit_id + filename` 由进程内文件锁串行处理。`INDEX_CONCURRENCY` 可调并发（默认 1，上限 4）。

### 1.4 Agent 工具循环与流式回答

```mermaid
sequenceDiagram
    actor User as 业务人员
    participant UI as Vue Agent 页
    participant API as Go Agent Handler
    participant Runtime as agent.Runtime
    participant Tools as 只读工具
    participant Embed as BGE-M3 API
    participant Store as vectorstore.Store
    participant VectorDB as Chroma 或 pgvector
    participant DB as PostgreSQL
    participant LLM as Chat Completion API

    User->>UI: 输入问题并选择项目
    UI->>API: POST /api/v1/agent/stream (SSE)
    API->>DB: 创建 agent_runs
    API->>Runtime: system + history + tool definitions
    loop 最多 AGENT_MAX_STEPS 步
        Runtime->>LLM: messages + tools (stream)
        LLM-->>Runtime: text delta / tool_calls
        Runtime-->>UI: message_delta / tool_call
        Runtime->>Tools: 并行执行只读工具
        Tools->>DB: 项目画像 / 隐患 / 审核
        Tools->>Embed: query embedding
        Tools->>Store: 按 doc_id 集合检索
        Store->>VectorDB: 相似度检索
        VectorDB-->>Store: 档案片段
        Store-->>Tools: 文件名、片段、距离
        Tools-->>Runtime: 结果（按调用顺序回填）
        Runtime->>DB: 写入 agent_steps
        Runtime-->>UI: tool_result
    end
    Runtime-->>API: answer + citations + usage
    API->>DB: 更新 agent_runs
    API-->>UI: citations / run_completed
```

- `/api/v1/agent/chat` 与 `/api/v1/agent/stream` 共用同一个运行时；前者聚合结果返回 JSON，后者通过 SSE 推送 `run_started`、`step_started`、`tool_call`、`tool_result`、`message_delta`、`citations`、`run_completed`、`error` 事件。SSE 每 15 秒发送 `: keep-alive` 注释帧，避免反向代理在工具/模型执行期间断开。
- 选择项目时，运行时按**第一个**项目的 Agent/技能/工具绑定裁剪只读工具：候选为 `get_unit_profile`、`query_hazards`、`audit_history`、`vector_search`；项目显式声明的 `tool_names` 优先，未声明时由技能声明所需工具、Agent 作为上限，两边都未声明时全部启用。对话支持同时选择多个项目，工具的 `unit_id` 参数枚举限定在已选集合内，不传时覆盖全部已选项目，结果带 `unit_id`/`unit_name`；模型无法查询集合外的项目。
- 每步工具调用先做参数校验，再并行执行，结果按模型请求顺序回填；工具错误作为 tool result 返回给模型，而不是中断整个对话。模型因输出长度被截断时（`finish_reason=length`），该批 tool call 全部判为失败且不执行——流式拼接出的参数可能"看着合法但实际不完整"。
- `web_search` 是可选的外部工具，需要「服务端配置联网搜索」+「本轮请求 `web_search: true`」同时满足；不受项目 `tool_names` 约束。搜索结果可通过 `POST /units/:id/web-import` 归档进项目：抓取正文（`internal/websearch/fetch.go`，SSRF 防护：仅公共 http(s)、拨号时复检 IP、重定向逐跳复检、4MB 上限、仅文本类响应）后写入上传目录并复用 `index_jobs` 队列，因此与上传共享重试、进度和重启续跑语义。实现在 `internal/websearch`（provider 可插拔：tavily / bocha / brave），带结果缓存、站点白/黑名单、超时与查询长度限制；结果以 `notice` 明确标注为不可信外部内容，引用以 `source_type: "web"` 单独呈现，与已入库档案分开。
- `vector_search` 在工具内部按约 20KB 预算裁剪片段，citations 只包含实际发送给模型的片段；Agent Loop 的兜底截断为 24KB，用于其他可能较大的工具结果。
- 模型流式调用支持 tool_calls；若 provider 拒绝流式工具调用（400/422），自动回退到非流式请求。模型返回空文本且无工具调用时按失败处理，不返回空回答。
- 运行状态写入 `agent_runs`：`completed`、`failed`、`cancelled`、`timeout`、`max_steps`；服务启动时仅把超过 Agent 最大运行时限的陈旧 `running` 标记为 `interrupted`，避免误改其他实例正在运行的记录。工具明细写入 `agent_steps`，写失败只记日志。
- 未选择项目时不暴露工具，直接进行通用对话。
- `/api/v1/ask` 仍是单轮检索 + 回答的兼容接口，不进入工具循环。
- 远程 connector 目前仍只描述/展示，尚未实现外部 MCP client 工具调用。

### 1.5 WorkBuddy / 外部 Agent 的 MCP 调用流程

```mermaid
sequenceDiagram
    actor User as 用户
    participant Agent as WorkBuddy / MCP Host
    participant MCP as Go 服务 POST /mcp
    participant DB as PostgreSQL
    participant Embed as BGE-M3 API
    participant Store as vectorstore.Store
    participant VectorDB as Chroma 或 pgvector

    User->>Agent: 提出项目档案问题
    Agent->>MCP: tools/list (Bearer Token)
    MCP-->>Agent: 五个只读工具及参数 schema
    Agent->>MCP: tools/call(list_units)
    MCP->>DB: 查询项目
    DB-->>MCP: 项目列表
    MCP-->>Agent: 工具结果
    Agent->>MCP: tools/call(vector_search, unit_id, query, top_k)
    MCP->>Embed: 对 query 生成向量
    Embed-->>MCP: query vector
    MCP->>Store: 按 doc_id 集合检索
    Store->>VectorDB: 按 doc_id 过滤并做向量检索
    VectorDB-->>Store: 档案片段
    Store-->>MCP: 文件名、片段、距离
    MCP-->>Agent: 工具结果
    Agent-->>User: 基于工具结果组织回答并引用出处
```

生产接入应在 MCP endpoint 前使用 HTTPS，并为调用方配置有效 Bearer Token。MCP 工具当前只读；工具结果的最终回答由外部 Agent 生成。

```text
Vue 3 / Vite
  └─ /api/v1/* ── Go + Gin (cmd/server, internal/app)
                    ├─ pgx ── PostgreSQL (units / hazards / audit_records)
                    ├─ vectorstore.Store ── pgvector / Chroma adapters (document vectors)
                    ├─ OpenAI-compatible API ── BGE-M3 embeddings / chat model
                    └─ POST /mcp ── WorkBuddy / other MCP hosts
```

- `cmd/server`：读取端口并启动 HTTP 服务。
- `internal/app`：HTTP API、上传任务与 `index_jobs` 持久化、文档提取与切块、模型调用、Agent HTTP/SSE 适配和 MCP 工具。
- `internal/agent`：与 HTTP/数据库无关的 Agent 运行时，包括工具注册、并行工具循环、事件流和 OpenAI-compatible 流式工具客户端。
- `internal/logging`：`slog` 初始化、`app.log`/`access.log`/`error.log` 分流与按大小轮转。
- `internal/vectorstore`：`Store` 接口、provider 选择、pgvector 与 Chroma adapters。
- `frontend/src`：Vue 页面；生产静态资源从 `frontend/dist` 提供。
- `deploy/schema.sql`：PostgreSQL 业务表 schema，不依赖 pgvector 扩展。
- `deploy/schema-pgvector.sql`：仅选择 `VECTOR_STORE=pgvector` 时创建向量表。
- `workbuddy/knowledge-base-connector`：WorkBuddy connector 源包；zip 在同目录父级。

### 1.6 检索与拒答（混合检索 + 重排）

```mermaid
flowchart TD
    Q[用户问题] --> P[并行召回]
    P --> V[向量检索<br/>RETRIEVAL_VECTOR_K=30]
    P --> K[关键词检索<br/>chunk_text + pg_trgm<br/>word_similarity ≥ 0.12]
    V --> R[RRF 融合<br/>1/(60+rank) 取前 20]
    K --> R
    R --> RR[bge-reranker-v2-m3 重排]
    RR -->|接口失败| FB[回退融合顺序<br/>按 distance ≤ 0.55 过滤]
    RR --> G{score ≥ RERANK_MIN_SCORE?}
    G -->|否| E[丢弃片段]
    G -->|是| O{二元组覆盖率<br/>≥ RETRIEVAL_MIN_OVERLAP?}
    O -->|否且 score < 0.5| E
    O -->|是| OUT[进入上下文 + 引用]
    E --> N{还有候选?}
    N -->|否| NE[no_evidence<br/>明确拒答]
```

三个设计取舍：

1. **关键词索引放在 PG，不放向量库**：`chunk_text` 是向量库的镜像表（`doc_id + chunk_index + content`），写入与向量同批、删除同步。这样 Chroma / pgvector 两种实现共用一套中文关键词召回；`pg_trgm` 按字符三元组匹配，不需要中文分词扩展。
2. **RRF 而不是分数加权**：向量距离与 trigram 相似度量纲不可比，加权需要标定；RRF 只用排名，参数只有一个平滑常数。
3. **两道证据闸门**：重排过滤「不相关」，二元组覆盖率过滤「话题相同但概念不同」（例如问《伦理学》而库里只有《第一哲学沉思集》，人名命中会骗过重排）。覆盖率只在重排分 < 0.5 时启用，避免误杀同义改写。

拒答分两层：检索层零命中直接 `no_evidence`（不调用大模型）；有相关片段但资料不足时由系统提示约束模型明确说明「资料中没有提到」，不得用常识补全。

**评测**：评测集与真实档案绑定，不进公开仓库——`eval/questions.example.json` 是占位模板，把它复制成 `eval/questions.json` 再换成自己的用例（后者已被 `.gitignore` 忽略）。一条用例可以只断言命中文件，也可以断言 `expect_content` 关键词、`expect_no_evidence`（检索层应零命中）或 `expect_answer_refusal`（生成层应说明资料中没有）。

```bash
go run ./cmd/eval            # 只跑检索（毫秒级），判定命中文件与关键词
go run ./cmd/eval -answer    # 额外调用 /ask 验证生成层拒答
```

当前基线：检索 12/12、拒答 4/4 通过，平均 500ms/条。调检索参数前后各跑一次即可对比。

### 1.7 数据模型关系（文档中心）

```mermaid
erDiagram
    units ||--o{ document_units : "被引用"
    documents ||--o{ document_units : "关联知识库"
    documents ||--o{ doc_chunks : "向量块"

    units {
        text unit_id PK
        text name
        jsonb tags
        text expert_id
    }
    documents {
        text doc_id PK "由正文哈希派生"
        text filename
        text doc_type
        text content_hash UK "唯一"
        int chunk_count
        text file_path
        timestamptz indexed_at
    }
    document_units {
        text doc_id PK
        text unit_id PK
    }
    doc_chunks {
        text doc_id PK
        int chunk_index PK
        text content
        vector embedding
    }
```

- `documents` 以正文哈希派生 `doc_id`，`content_hash` 上有唯一索引，同样的内容只存一份。
- `document_units` 是多对多：从知识库删除文档只删这一行；不再被任何知识库引用时才清理向量与源文件。
- `doc_chunks`（pgvector 模式）或 Chroma metadata 以 `doc_id` 归属，文件名与类型来自 `documents`。
- `index_jobs` 记录任务与源文件路径，供重试与重索引使用。

### 前端设置导航

全局侧栏底部的「设置」进入设置布局，设置页内部按「日常在上、底层在下」分五组：

| 分组 | 分类 | 路由 | 用途 | 生效方式 |
| --- | --- | --- | --- | --- |
| 外观 | 系统外观 | `/settings/brand` | 名称、副标题、图标、7 套配色、3 档圆角 | 保存即生效 |
| 能力 | 模型服务 | `/settings/models` | 分别配置向量模型与对话模型 | 保存即生效 |
| 能力 | 联网搜索 | `/settings/web-search` | 搜索服务、站点范围与缓存限制 | 保存即生效 |
| 检索与记忆 | 检索参数 | `/settings/retrieval` | 召回、两道闸门、切块、记忆窗口 | 保存即生效 |
| 检索与记忆 | 记忆与上下文 | `/settings/memory` | 摘要、约束事实、滑出窗口的消息 | 只读 + 可重建 |
| 检索与记忆 | 知识沉淀 | `/settings/drafts` | 问答候选、整理成笔记、采纳入库 | 采纳后进入索引队列 |
| 扩展 | Agent 与技能 | `/settings/agents` | 上传与管理 Agent、技能定义 | 保存即生效 |
| 部署 | 实例配置 | `/settings/instance` | 数据库、向量库、监听地址、数据目录 | **需重启** |

Agent 与技能管理不再作为全局侧栏一级菜单；原 `/agents` 路由保留为到 `/settings/agents` 的重定向。设置布局在宽屏下显示固定的左侧分类导航和独立滚动的右侧内容面板，在窄屏下将分类导航切换为顶部横向菜单。模型服务、联网搜索的页内 Tab 在右侧内容滚动时保持可见。

当前代码中 `VECTOR_STORE` 支持 `pgvector` 和 `chroma`。Chroma adapter 连接时会确保目标 collection 存在；通过 `CHROMA_URL`、`CHROMA_TENANT`、`CHROMA_DATABASE`、`CHROMA_COLLECTION` 配置。Chroma 存储由应用侧生成的 BGE-M3 embeddings，使用 `unit_id` metadata filter 做隔离检索。新增 provider 时实现 `Upsert`、`Search`、`Count`、`DeleteFrom`，并在 provider factory 中注册，不能把特定数据库逻辑泄漏到 API handler。

## 2. 本地开发环境

需要 Go 1.25+（见 `go.mod`）、Node.js 20+、pnpm 10+ 和 PostgreSQL 15+。

数据落点：

- 业务数据 `units`、`hazards`、`audit_records`、`index_jobs`、`documents` 始终通过 `PG_DSN` 存入 PostgreSQL。
- 向量写入由 `VECTOR_STORE` 决定：本地开发用 `chroma`，需先启动 Chroma（默认 `http://127.0.0.1:8000`，启动时 `get_or_create` collection，Chroma 不可用会导致服务启动失败）；线上可改为 `pgvector`，此时在同一个 PostgreSQL 中创建 `doc_chunks`，需安装 pgvector 扩展并执行 `deploy/schema-pgvector.sql`。
- 两种 provider 使用相同 embedding 模型和 1024 维向量，切换 provider 时必须重建向量索引。

PDF 无需额外依赖：优先用 `pdftotext`（排版与中文更准），没有时走内置纯 Go 解析。复制 `.env.example` 到 `.env` 并填写连接信息和模型凭据；不要提交 `.env`。

```sh
psql "$PG_DSN" -f deploy/schema.sql
# 仅 VECTOR_STORE=pgvector 时额外执行：
# psql "$PG_DSN" -f deploy/schema-pgvector.sql
cd frontend && pnpm install
```

### 启动 Chroma

本地开发默认 `VECTOR_STORE=chroma`，需要一个 Chroma server（v2 API）。

**Docker（最快）**

```sh
docker run -d --name kb-chroma -p 8000:8000 -v kb-chroma-data:/data chromadb/chroma:1.5.9
curl -s localhost:8000/api/v2/heartbeat     # 应返回 {"nanosecond heartbeat":...}
```

**k8s（如果已经有一套集群）**

Service 是 ClusterIP 时集群外连不上，必须做端口转发；**转发进程一断，`127.0.0.1:8000` 就不通，Go 服务启动会直接失败**并报 `initialize Chroma collection ... connection refused`。

```sh
kubectl -n <namespace> port-forward svc/chroma 8000:8000
```

两个部署细节是踩过的：

- Deployment 要设 `enableServiceLinks: false`。Service 名叫 `chroma` 时 k8s 会注入 `CHROMA_PORT=tcp://...`，与 Chroma 自身的 `PORT` 冲突，容器启动直接 panic。
- 单副本 + `strategy: Recreate`。Chroma server 模式写路径是单实例，同卷多挂载会出问题。

后端可能跑在三个位置，地址不同：

| 后端在哪跑 | 要不要转发 | `CHROMA_URL` |
| --- | --- | --- |
| 宿主机（本机） | 要 | `http://127.0.0.1:8000` |
| 同一 Docker 网络里的容器 | 不要 | `http://<容器名或服务名>:8000` |
| k8s 集群内 | 不要 | `http://chroma.<namespace>.svc:8000` |
| 容器里但走宿主机转发 | 要，且加 `--address 0.0.0.0` | `http://host.docker.internal:8000` |

后端跑在容器里时，宿主机上的转发默认只监听 `127.0.0.1`，容器访问不到——这是最容易卡住的一步，必须加 `--address 0.0.0.0`。

终端 A 启动后端：

```sh
go run ./cmd/server   # 自动加载工作目录下的 .env，无需 source
```

启动器会在初始化日志前读取 `.env`：已存在的进程环境变量优先，不会被文件覆盖；文件不存在时静默跳过（线上由 systemd `EnvironmentFile` 注入即可）。需要指定其他文件时设置 `ENV_FILE=/path/to/env`。

终端 B 启动前端开发服务器：

```sh
cd frontend
pnpm dev
```

Vite 将 `/api`、`/health`、`/mcp` 代理到 `http://localhost:8081`（`vite.config.ts` 的 `proxy` 目标必须与 `.env` 里的 `PORT` 一致）。生产构建和启动：

```sh
cd frontend && pnpm build && cd ..
go run ./cmd/server
```

前端依赖统一用 pnpm，不要与 npm 混用。三个 pnpm 特有的坑：

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| `Failed to resolve entry for package "@vue-office/docx"` | 这三个包的 `lib/index.js` 由 `postinstall` 生成，pnpm 默认不执行依赖构建脚本 | `package.json` 的 `pnpm.onlyBuiltDependencies` 已批准相关包；仍报错时执行 `pnpm rebuild` |
| 打包报 `Rollup failed to resolve import "three"` | pnpm 不提升传递依赖，`3d-force-graph` 的 `three` 不在顶层 | `three` 已在 `dependencies` 显式声明 |
| `Cannot find module @rollup/rollup-linux-arm64-gnu` | 锁文件只含当前平台的原生二进制 | `pnpm.supportedArchitectures` 已声明多平台；换平台后执行 `pnpm install` |

`@vue-office/*` 是 UMD 包，`vite.config.ts` 用 `optimizeDeps.include` 强制预构建；若改为 `exclude`，dev 下会直接加载未转换的 CJS，预览报 `defineComponent of undefined`。

Go 服务默认监听 `127.0.0.1:${PORT}`，默认 8080；可通过 `LISTEN_ADDR` 覆盖。远程部署应经受控 HTTPS 反向代理访问。确保进程工作目录能访问 `frontend/dist`。

## 3. 环境变量

| 变量 | 用途 | 默认/要求 |
| --- | --- | --- |
| `ENV_FILE` | 启动时加载的环境文件路径 | `.env`；不存在则跳过 |
| `INSTANCE_ENV_FILE` | 页面写入的实例配置，**先于** `ENV_FILE` 加载 | `data/instance.env`；不存在则跳过 |
| `PORT` | HTTP 监听端口（未设置 `LISTEN_ADDR` 时生效） | `8080` |
| `LISTEN_ADDR` | HTTP 监听地址 | `127.0.0.1:${PORT}`；远程部署时按需设为 `0.0.0.0:8080` |
| `PG_DSN` | PostgreSQL 连接串 | 业务数据必填（Chroma 与 pgvector 模式都需要） |
| `VECTOR_STORE` | 向量 provider | `chroma`（本地 `.env`）或 `pgvector`（线上）；代码默认 `pgvector` |
| `CHROMA_URL` | Chroma v2 HTTP API 根地址 | 使用 Chroma 时默认 `http://localhost:8000` |
| `CHROMA_TENANT` | Chroma tenant | `default_tenant` |
| `CHROMA_DATABASE` | Chroma database | `default_database` |
| `CHROMA_COLLECTION` | 档案向量 collection | `knowledge_base` |
| `UPLOAD_DIR` | 原始上传文件目录 | `./data/uploads` |
| `BRAND_DIR` | 自定义图标目录 | `./data/brand`；多实例部署需共享卷 |
| `MAX_UPLOAD_MB` | 单文件上传大小上限 | `20` |
| `INDEX_TIMEOUT_MINUTES` | 单个索引任务超时 | `15` |
| `EMBED_BASE_URL` | OpenAI-compatible embedding API 根地址 | 必填，例如服务商 `/v1` 根地址；页面保存值优先 |
| `EMBED_API_KEY` | embedding API 凭据 | 必填；页面保存值优先 |
| `EMBED_MODEL` | embedding 模型名 | `BAAI/bge-m3`；维度固定 1024 |
| `LLM_BASE_URL` | OpenAI-compatible chat API 根地址 | Agent/ask 必填；页面保存值优先 |
| `LLM_API_KEY` | chat API 凭据 | Agent/ask 必填；页面保存值优先 |
| `LLM_MODEL` | chat 模型 | `Qwen/Qwen2.5-7B-Instruct` |
| `MCP_AUTH_TOKEN` | MCP Bearer 凭据 | 访问 MCP 时必须配置；空值会禁用 endpoint |
| `AGENT_MAX_STEPS` | 单次 Agent 运行最多工具步数 | `6`（允许 1–12） |
| `WEB_SEARCH_ENABLED` | 是否启用联网搜索（服务端开关） | `false` |
| `WEB_SEARCH_PROVIDER` | 搜索服务：`tavily` / `bocha` / `brave` | 空（启用时必填） |
| `WEB_SEARCH_API_KEY` | 搜索服务密钥 | 空（启用时必填，不落日志） |
| `WEB_SEARCH_BASE_URL` | 自建或代理地址 | 空（用各服务默认地址） |
| `WEB_SEARCH_TIMEOUT_MS` | 单次搜索超时 | `15000` |
| `WEB_SEARCH_CACHE_TTL_SECONDS` / `WEB_SEARCH_CACHE_ENTRIES` | 结果缓存时长与条数 | `900` / `200` |
| `WEB_SEARCH_ALLOW_DOMAINS` / `WEB_SEARCH_DENY_DOMAINS` | 站点白名单/黑名单（逗号分隔） | 空（不限制站点） |
| `AGENT_EXPERTS_JSON` | 专家定义数组（基线） | 可选；页面上传的定义覆盖同 id 项，格式见下文 |
| `AGENT_SKILLS_JSON` | 技能定义数组（基线） | 可选；页面上传的定义覆盖同 id 项，格式见下文 |
| `MCP_CONNECTORS_JSON` | Agent UI 展示的 connector 描述数组 | 可选；目前仅描述，不会被 chat handler 调用 |
| `LOG_DIR` | 日志目录 | `./logs`；生产建议 `/var/log/knowledge-base` |
| `LOG_LEVEL` | 日志级别 | `debug`/`info`/`warn`/`error`，默认 `info` |
| `LOG_FORMAT` | 日志格式 | `text`（默认）或 `json` |
| `LOG_CONSOLE` | 是否同时输出到 stdout | `true`；systemd 下进入 journald |
| `LOG_MAX_SIZE_MB` | 单文件轮转阈值 | `50`；`0` 表示不轮转 |
| `LOG_MAX_BACKUPS` | 每类日志保留的轮转文件数 | `10` |

优先级：**进程环境变量 > `data/instance.env` > `.env` > 代码默认值**。`loadEnvFile` 对已存在的变量跳过，所以先加载的赢；这也是「页面上改过某项、`.env` 里再改它就不生效」的原因。见 [1.1.2 配置的两条通道](#112-配置的两条通道)。

示例（写入 `.env` 时必须用单引号包裹，否则 `source .env` 时 shell 会对 JSON 中的花括号/逗号做展开；直接作为进程环境变量时可省略引号）：

```sh
AGENT_EXPERTS_JSON='[{"id":"kb-expert","name":"知识库专家","description":"知识库问答","system_prompt":"依据档案回答，缺少证据时说明。","tools":["get_unit_profile","query_hazards","audit_history","vector_search"]}]'
AGENT_SKILLS_JSON='[{"id":"archive-search","name":"档案检索","description":"检索项目资料","instructions":"引用检索到的文件名。","tools":["vector_search"]}]'
MCP_CONNECTORS_JSON='[{"name":"kb-mcp","url":"https://kb.example.com/mcp","enabled":true}]'
```

### 3.1 日志规则

日志统一由 `internal/logging` 初始化，使用标准库 `log/slog`，不直接调用 `log.Printf`。

| 文件 | 写入内容 | 级别 |
| --- | --- | --- |
| `app.log` | 启动/停止、索引任务阶段与耗时、Agent run 完成（`run_id`/`steps`/`citations`）、数据库和模型错误、MCP 工具错误 | `LOG_LEVEL` 及以上 |
| `access.log` | 每个 API/页面请求一行：`request_id`、`method`、`path`、`status`、`latency_ms`、`ip`、字节数；`/assets/*` 静态资源不记录，`/health` 仅在 DEBUG 级别记录 | `LOG_LEVEL` 及以上 |
| `error.log` | `app.log` 与 `access.log` 中的 ERROR 记录汇总 | 仅 ERROR |

约定：

- 每个响应返回 `X-Request-ID`；请求可自带该头，服务会透传，否则生成 16 位十六进制 ID。
- 业务日志按场景绑定字段：索引任务带 `job_id`/`unit_id`/`filename`/`doc_type`/`duration_ms`/`chunks`，Agent 带 `run_id`/`unit_id`/`expert`/`skills`/`steps`/`citations`，MCP 带 `tool`/`unit_id`/`duration_ms`，HTTP 带 `request_id`/`status`。工具级 trace 写入 `agent_steps`，不只依赖日志。
- 记录查询字符串以外的请求路径，不记录 query string；禁止记录 API key、MCP token、密码和档案正文。
- `LOG_FORMAT=json` 用于日志采集；默认 `text` 便于本地阅读。文件超过 `LOG_MAX_SIZE_MB` 轮转为 `<name>-YYYYMMDD-HHMMSS.mmm.log`，保留 `LOG_MAX_BACKUPS` 个。
- 控制台输出由 `LOG_CONSOLE` 控制；日志目录初始化失败时进程回退到 stderr，不会因日志目录不可写而退出。

## 4. 数据库初始化与数据模型

```sh
psql "$PG_DSN" -f deploy/schema.sql
# 仅 VECTOR_STORE=pgvector 时额外执行：
psql "$PG_DSN" -f deploy/schema-pgvector.sql
```

`schema.sql` 会创建以下业务表；本地 Chroma 模式只需要执行它，无需 PostgreSQL 向量扩展。使用 pgvector 时，`schema-pgvector.sql` 会创建 `doc_chunks` 向量表及索引：

- `units`：项目主数据；`unit_type` 为项目分类、`industry` 为行业，`tags` 保存项目标签（用于筛选、画像与关联图谱），`expert_id`/`skill_ids`/`tool_names` 保存项目级 Agent、技能与显式工具（对话时自动套用，`tool_names` 优先于按技能推导）。上传文档前必须存在对应 `unit_id`。
- `agent_experts` / `agent_skills`：页面上传的 Agent 与技能定义（id、名称、说明、system_prompt/instructions、tools、来源、时间）。内置定义与环境变量作为基线，同 id 的上传记录优先；`tools` 只能取四个只读工具，用于裁剪对话时的工具集。
- `doc_chunks`（pgvector 可选）：文档 `doc_id`、chunk 序号、文本及 `vector(1024)`；主键为 `(doc_id, chunk_index)`。文件名与类型来自 `documents`，一份文档被多个知识库引用时只存一份向量。
- `hazards`：项目隐患和状态。
- `audit_records`：项目审核记录、分数、风险级别及 JSON payload。
- `index_jobs`：持久化索引任务队列（`job_id`、项目、文件名、源文件路径 `file_path`、状态、进度、chunk 数、重试次数 `attempts`、时间戳）。上传只写入 `queued` 行，worker 通过 `FOR UPDATE SKIP LOCKED` 领取并执行；服务启动时仅把超过索引超时阈值的陈旧 `running` 任务重新置为 `queued`，避免另一个活跃实例的任务被抢占。
- `documents`：文档一等实体（`doc_id` 由正文哈希派生、文档类型、`job_id`、chunk 数、内容哈希、源文件路径、索引时间）；`content_hash` 上有唯一索引，同内容只存一份。
- `document_units`：文档 ↔ 知识库的多对多关系；知识库相当于带标签的分组，从知识库移除只删这一行。
- `web_search_settings`：联网搜索单行配置（开关、provider、密钥、超时、缓存、站点白/黑名单、自动入库开关）。数据库记录优先于 `WEB_SEARCH_*` 环境变量，保存后缓存失效并重建搜索客户端，密钥只掩码回显。
- `documents.source_url` / `index_jobs.source_url`：网页入库的来源 URL，用于去重与溯源；普通上传为空字符串。
- `agent_runs`：Agent 运行记录（`run_id`、项目、专家、用户原始问题、技能、状态、步数、token、错误、起止时间）。旧记录缺少问题时，运行详情接口会按 `run_id` 从关联的用户消息回填。
- `agent_steps`：工具调用明细（`run_id`、步号、工具名、参数、结果预览、耗时、错误）。写入失败只记日志，不影响对话。
- `agent_conversations` / `agent_messages`：对话历史。会话记录项目（`unit_ids` 保存多选集合，`unit_id` 冗余第一个项目）、Agent、标题与更新时间；消息记录角色、内容、引用（jsonb）和 `run_id`。删除会话级联删除消息，历史写入失败同样只记日志。
- `model_settings`：模型服务配置单行表（`id=1`），保存 embedding/chat 的 `base_url`、`api_key`、`model`。字段为空时回落到 `EMBED_*`/`LLM_*` 环境变量，因此已有部署无需初始化该表。API Key 与其它数据同样以明文存储在该表中，读取接口只返回掩码；生产环境应限制数据库访问权限。

原附件还提到 `findings`、`ledger_results` 以及 Python 线 A/线 B 脚本，但这些对象当前没有随本仓库实现。接入旧数据前，应先核实源 SQL、字段和迁移规则；不要假设当前 schema 与既有脚本完全兼容。

项目样例可按需人工插入：

```sql
INSERT INTO units(unit_id,name,unit_type,industry)
VALUES ('demo-001','示例项目','重点项目','制造业')
ON CONFLICT (unit_id) DO NOTHING;
```

## 5. 文档索引流程

```text
POST /api/v1/upload
  → 校验数据库、unit_id/unit_name、doc_type、文件字段、扩展名和大小上限
  → unit_name 未匹配到项目时自动创建（unit-<hex>）
  → 保存原文件到 UPLOAD_DIR/<unit_id>/<job_id>_<filename>
  → 写入 index_jobs(queued)，返回 202 + job_id，后台进程内 goroutine 执行
  → 提取纯文本 (DOCX 解包 XML；PDF 优先 pdftotext，否则内置 Go 解析)
  → 按 1800 字符切块、240 字符重叠
  → 每批最多 16 段请求 EMBED_MODEL，校验返回数量与每个向量是否为 1024 维
  → vectorstore.Store.Upsert 写入并更新 index_jobs 进度
  → DeleteFrom(unit_id, filename, chunk_count) 删除上一版本尾部 chunk
  → 写入 documents 版本记录
```

当前切块按 Unicode rune 计数，不理解 PDF 页码、DOCX 标题或表格结构；DOCX 表格内容是否完整取决于 XML 文本顺序。PDF 扫描件不会自动 OCR。任务状态持久化在 `index_jobs`，服务启动时超过索引超时阈值的陈旧 `running` 任务会重新排队；单任务最多尝试 3 次，处理错误会直接失败。领取任务使用 `SKIP LOCKED`，启动恢复也会避开未超时的任务；同一文档的文件锁仍是进程级，多副本同时重建同一文件时仍需分布式锁。

上传时先按正文哈希判断文档是否已存在：已存在则跳过解析与向量化，只建立 `document_units` 关联（响应直接返回 `done`）；不存在才走完整索引。同一知识库下同名但内容不同时，旧版本会被自动解绑并清理。索引结构变更后可用 `go run ./cmd/reindex` 重建全部向量。chunk 写入与 `documents` 记录不在同一事务内；`documents` 写失败会将任务标记为 `failed`，但已写入的向量可能部分覆盖旧版本。完整解决需要版本化向量与发布切换机制。

## 6. HTTP API

详细契约见 [API 参考](api.md)。关键行为：

- API 基础路径为 `/api/v1`；上传为 multipart。
- `/api/v1/ask` 和 `/api/v1/agent/chat` 使用 embedding 与 chat 服务，均返回 `citations`。
- `/api/v1/agent/stream` 使用同一 Agent 运行时，以 SSE 推送工具调用、文本增量和引用；详细事件见 `docs/api.md`。
- `/health` 的 `database` 字段只反映是否配置了 PG pool，不代表数据库当前连通性探测成功。
- 错误响应通常为 `{"error":"..."}`；上传任务状态查询对未知 job 返回 404。
- `DELETE /api/v1/units/:id/documents/:filename` 删除一份文档的向量、`documents` 记录和源文件。

## 7. MCP 与 WorkBuddy

服务端 MCP endpoint 为 `POST /mcp`，使用 Bearer token。实现了 `2026-07-28` 无状态协议路径及 `2025-11-25` 兼容路径；工具均只读：

| Tool | 用途 |
| --- | --- |
| `list_units` | 列出可用项目 |
| `get_unit_profile` | 返回项目基础信息及审核、开放隐患、索引 chunk 数 |
| `query_hazards` | 查询项目隐患，可按状态过滤 |
| `audit_history` | 读取项目审核历史 |
| `vector_search` | BGE-M3 编码问题并返回项目档案相似片段 |

内置 Agent 复用同一组数据查询函数（`unitProfile`、`queryHazards`、`auditHistory`、`searchChunks`），但 Agent 工具 Schema 不包含 `unit_id`（由服务端固定），MCP 工具仍要求调用方显式传入 `unit_id`。

本地可以将 connector 包里的 `mcp.json` URL 改为已部署服务地址；远程生产环境只用 HTTPS。服务端 `MCP_AUTH_TOKEN` 必须与连接器用户配置一致。连接器 token 不应写入打包文件或仓库。

本地 Agent 页面的 `MCP_CONNECTORS_JSON` 当前用于描述/展示 connector。它不代表后端已经实现任意 MCP client、工具发现、工具调用和结果回传；该执行能力需作为后续功能独立开发和验收。

## 8. Ubuntu / systemd 部署

1. 安装 Go、Node 与 pnpm 10+（仅构建前端时需要）、PostgreSQL 客户端（`pdftotext` 可选，装了 PDF 解析更准）。
2. 将源码部署到固定目录，用 `cd frontend && pnpm install && pnpm build` 构建 `frontend/dist`，再构建 Go 二进制（跨平台部署时交叉编译，例如 `GOOS=linux GOARCH=arm64 go build -o bin/knowledge-base ./cmd/server`）。
3. 从安全渠道写入 `/etc/knowledge-base/env`，限制为仅服务用户可读（例如 `0600`）。至少配置 `PG_DSN`、embedding/chat 密钥和 MCP token。
4. 确认服务用户有 `UPLOAD_DIR` 和 `LOG_DIR` 写权限，例如 `install -d -o knowledgebase -g knowledgebase /var/log/knowledge-base` 并在 env 中设置 `LOG_DIR=/var/log/knowledge-base`；`deploy/knowledge-base.service` 已把该目录加入 `ReadWritePaths`。执行 schema 迁移并确保 PG 网络可达。
5. 按部署路径调整 `deploy/knowledge-base.service` 中的 `WorkingDirectory` 和二进制路径，再启用服务。
6. 通过反向代理提供 HTTPS；开放 MCP 给 WorkBuddy 前测试 token、网络 ACL 和项目数据访问范围。
7. 验收 `/health`、项目查询、一次脱敏文件上传、向量检索、Agent SSE 工具循环和 WorkBuddy 工具调用。

## 9. 开发规范与变更约束

- 新 API 在 `internal/app` 注册路由，并同步更新 `docs/api.md`。
- 新增只读能力时，优先实现为 `internal/app` 的工具（`agentTool`），并在 `agentRegistry` 中暴露给 Agent；需要外部复用时同步加入 MCP `mcpTools`，保持两处参数语义一致。
- 改 schema 时同步更新 `deploy/schema.sql`、此文档和迁移/备份说明；生产库变更应使用可回滚迁移，不能仅依赖 `CREATE TABLE IF NOT EXISTS`。
- 新向量数据库实现 `vectorstore.Store`，覆盖 provider 选择和 provider 配置说明；相似度排序、项目过滤、维度校验语义需一致。
- 改 embedding 模型、维度、归一化或切块参数时，应通过版本化索引重建，不能混写不兼容向量。
- 新增日志统一使用 `internal/logging.App()` / `internal/logging.HTTP()` 和 `slog` 结构化字段，不新增 `log.Printf`；需要文件分流或轮转规则时改 `internal/logging`，不要在业务代码里自行打开文件。
- 不把密钥、真实档案、个人信息写入日志、前端 bundle、示例配置或 connector zip。
- 新增功能需补充自动化测试。仓库现有单元测试覆盖切块、文档提取、上传校验、任务状态、Agent 工具循环/并行顺序/最大步数/空回答、向量工具预算与引用对齐、OpenAI 流式解析/非流式回退、MCP 鉴权/协议以及 Chroma adapter（基于 httptest 的伪服务）；数据库查询路径仍以集成测试为主，需在真实 PostgreSQL 上补充覆盖。

前端（Vue 3 + Element Plus）四条约定，都是踩过才写下来的：

- **文档列表不要再复制一份。** 统一用 `frontend/src/components/DocumentExplorer.vue`，数据源是 `/documents`（支持 `q` / `state` / 分页），行为由两个 prop 决定：`scope="unit"` 走「只解绑」（`?detach_only=1`，不动全局文档与向量），`scope="global"` 走彻底删除；`previewMode="emit"` 把预览交回父页面（详情页的预览带全文检索与高亮，组件内建的 iframe 做不到）。确认文案必须跟着 `scope` 变——说错一句就会让人以为删掉了别的库还在用的文档。
- **`el-dialog` / `el-drawer` 上的 class 挂在组件根元素自身。** 组件本体的样式要用复合选择器 `.source-dialog.el-dialog`；写成后代选择器 `.source-dialog .el-dialog` 永远匹配不到（三处弹窗的 `max-height` 都因此静默失效，长内容直接压到页脚上）。`__body` / `__footer` 这类子元素才用后代选择器。
- **弹窗里只留一个滚动容器。** `el-dialog__body` 默认 `display: block`，里面的 `flex: 1` 不生效；要滚就得把 body 改成 flex 列、逐层 `min-height: 0`，并用 `flex: 1 1 auto` 而不是 `1 1 0`（对话框高度由内容撑起，基准设 0 会让弹窗塌成一条）。
- **点一下就发送要克制。** 示例问题、章节、追问都只把文字填进输入框（`fillComposer`），发不发送由用户决定；`/agent?q=` 深链同理。误触不该消耗一次模型调用，用户也常常要先补一句限定条件。

```sh
go test ./...
go vet ./...
```

Chroma adapter 的本地集成检查（会创建并在结束时删除唯一命名的临时 collection）：

```sh
CHROMA_URL=http://localhost:8000 CHROMA_INTEGRATION_TEST=1 go test ./internal/vectorstore -run TestChromaIntegration -v
```

## 10. 已知限制

按「会不会让人踩坑」排序，不是按工作量。

### 10.1 正确性相关

1. **切块与元数据写入没有事务。** 向量在向量库、`documents` 在 PG，跨存储写入没有两阶段提交。中途失败会留下「向量已写、文档未记」的状态；目前已做的兜底是任务可重试 + 复用前校验向量库实际 `Count`，但没有原子性。
2. **上传只校验扩展名。** 品牌图标已经按魔数判断（`internal/app/brand_test.go` 锁住了），但文档上传仍看后缀。理论上可以把任意文件塞进解析流程。
3. **大纲抽取是规则驱动的。** 制度类文档靠「第X章 / 第X条」能抽准（v5 规则），扫描件、纯表格、无条款结构的文档抽不出来——页面会如实显示「没有可识别的章节结构」，不硬凑。这是能力上限，不是 bug。

### 10.2 安全与多租户

4. **没有身份认证。** 所有 `/api/v1/*` 默认不校验身份，包括 `/instance/config`（返回数据库主机、库名、用户名）。**不要把服务直接暴露到公网**；要么在反向代理层加认证，要么设置 `API_TOKEN` 并确保只在内网可达。
5. **实例配置明文存密码。** `data/instance.env` 权限 `0600`，与 `.env` 现状一致，磁盘未加密时可见。这是有意的取舍：加密需要密钥，而密钥又要存在某处，会让「自救」这件事变得更不可靠。
6. **单实例假设。** `UPLOAD_DIR` 与 `BRAND_DIR` 里的文件不进数据库，多实例部署必须挂共享卷，否则各实例看到的原件与图标不一致。

### 10.3 能力边界

7. **记忆只在会话内。** 摘要与事实绑定 `conversation_id`，换个会话就归零。做跨会话长期记忆需要引入「用户」维度，而系统目前没有账号体系——得先定事实是跟人走还是跟知识库走。
8. **评测面窄。** 16 条检索用例（5 条零命中、2 条生成层拒答），阈值 0.28 / 0.1 是针对这批调的。扩到 30+ 条、换一批文档，结论才站得住。Agent 轨迹与记忆层没有评测。
9. **画像与风险档位是占位实现。** 分数来自规则而非审核数据，`audit_history` 的表结构留了扩展位但没接真实审核流。
10. **OCR 缺失。** 扫描版 PDF 依赖 `pdftotext`，没有文本层就是空的。

### 10.4 扩展性欠账

11. **外部 MCP client 未接。** `Catalog` 的 `Add` 扩展点已就位，`connectorList` 只把 `MCP_CONNECTORS_JSON` 读给前端展示，不参与工具装配。需要一个真实 connector 才能验证。
12. **技能带不了实现。** `skill.Tools` 只是工具名列表，实现固定在 `builtinTools`。加新能力要改代码，写技能只是换提示词。
13. **定义是全局的，绑定才是项目级。** `agent_experts` / `agent_skills` 是全局单表，`units` 只是引用。单业务线够用，第二条业务线会出现「A 项目的专家出现在 B 的下拉里」。

### 10.5 已经做完、不必再列进 TODO 的

早期文档里这几条已经实现，留个说明避免重复投入：hybrid 检索（向量 + pg_trgm，RRF 融合）、cross-encoder 精排、双层拒答（阈值 + 二元组覆盖率闸门）、内联引用角标、检索分层留痕、Agent 工具循环与并发执行、持久化索引队列、内容框架与目录树、示例问题生成与验证、三层会话记忆与事实冲突消解、品牌与外观模板、实例配置页与回滚。

基础功能补齐（2026-10-25）：单库 `GET`/`DELETE`（删库只解绑、不删全局文档，见 [api.md](api.md) 的删除语义）、文档库服务端分页与 `state` 筛选、前端 404 兜底、错误体带 `request_id`、`/health` 带构建版本、`/ask` 与 `/retrieve` 参数统一为 `unit_ids` 且引用编号由服务端分配。知识库资源查询已收敛为 `unitSelectSQL` + `unitPayload`，列表与详情共用，新增展示字段只改一处。

**知识沉淀闭环（2026-10-25）**：问答不再只用于拼接 prompt。`runAgent` 成功后把「有文档依据的回答」异步写成 `kb_drafts` 候选（`content_hash` 去重，纯网页答案与拒答不沉淀）；每小时检查一次，待审核问答满 8 条自动整理成主题笔记（`kind=note`，原问答标 `merged`）；**只有人工采纳才会写入知识库**——采纳时生成 Markdown 并复用现有 `index_jobs` 队列，切块、嵌入、向量身份、章节抽取与普通上传完全一致。未采纳的草稿不参与检索，避免模型输出自我强化。详见 [api.md](api.md) 的知识沉淀一节。

**前端交互与布局（2026-10-25）**：

- 文档列表抽成共用组件 `DocumentExplorer`，知识库详情与文档库用同一份实现，差异由 `scope`（移除绑定 / 彻底删除）和 `previewMode`（内建 iframe / 交回父页面）承接；详情页因此删掉了重复的表格、片段抽屉与删除逻辑（约 200 行）。
- 文档移除新增 `?detach_only=1`：知识库详情页管的是**绑定关系**，不应因为某个库解绑就销毁全局文档与向量。语义与响应字段见 [api.md](api.md) 的「删除文档」。
- 索引片段修正两处：接口返回的是 `chunks` / `chunk_count` / `has_more`（原按 `items` / `total` 读，点开抽屉直接抛错）；序号统一为 1-based（后端 `index` 是 0-based）。
- 预览弹窗两处叠加的布局问题：对话框 class 必须写成复合选择器 `.source-dialog.el-dialog` 才拿得到 `max-height`；`el-dialog__body` 要显式改成 flex 列容器并逐层 `min-height: 0`，长文档才会在弹窗内滚动，而不是压到页脚按钮上。
- 对话页的示例问题、章节与追问改为「只填入、不代发」，`/agent?q=` 深链同理。
