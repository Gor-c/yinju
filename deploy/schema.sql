CREATE TABLE IF NOT EXISTS units (
  unit_id text PRIMARY KEY,
  name text NOT NULL,
  -- unit_type 即“项目分类”（如 重点项目、专项项目），保留列名以兼容既有 API。
  unit_type text NOT NULL DEFAULT '',
  industry text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
-- 已有库升级：项目级 Agent 配置（对话选中项目后自动带出）。
ALTER TABLE units ADD COLUMN IF NOT EXISTS expert_id text NOT NULL DEFAULT '';
ALTER TABLE units ADD COLUMN IF NOT EXISTS skill_ids jsonb NOT NULL DEFAULT '[]'::jsonb;
-- 项目显式声明的只读工具；为空时按 Agent 与技能推导。
ALTER TABLE units ADD COLUMN IF NOT EXISTS tool_names jsonb NOT NULL DEFAULT '[]'::jsonb;
-- 项目标签，用于分类筛选、画像与知识关联图。
ALTER TABLE units ADD COLUMN IF NOT EXISTS tags jsonb NOT NULL DEFAULT '[]'::jsonb;

-- Uploaded agent (expert) definitions. Built-in and AGENT_EXPERTS_JSON entries
-- stay as the baseline; rows here override the same id.
CREATE TABLE IF NOT EXISTS agent_experts (
  id text PRIMARY KEY,
  name text NOT NULL DEFAULT '',
  description text NOT NULL DEFAULT '',
  system_prompt text NOT NULL DEFAULT '',
  tools jsonb NOT NULL DEFAULT '[]'::jsonb,
  source text NOT NULL DEFAULT 'upload',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- Uploaded skill definitions. tools 声明该技能需要启用的只读工具。
CREATE TABLE IF NOT EXISTS agent_skills (
  id text PRIMARY KEY,
  name text NOT NULL DEFAULT '',
  description text NOT NULL DEFAULT '',
  instructions text NOT NULL DEFAULT '',
  tools jsonb NOT NULL DEFAULT '[]'::jsonb,
  source text NOT NULL DEFAULT 'upload',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS hazards (
  id bigserial PRIMARY KEY, unit_id text NOT NULL REFERENCES units(unit_id) ON DELETE CASCADE,
  hazard text NOT NULL DEFAULT '', status text NOT NULL DEFAULT '待整改', severity text NOT NULL DEFAULT '',
  source text NOT NULL DEFAULT '', occurred_at timestamptz, created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS audit_records (
  id bigserial PRIMARY KEY, unit_id text NOT NULL REFERENCES units(unit_id) ON DELETE CASCADE,
  title text NOT NULL DEFAULT '', score numeric, risk_level text NOT NULL DEFAULT '',
  payload jsonb NOT NULL DEFAULT '{}'::jsonb, created_at timestamptz NOT NULL DEFAULT now()
);

-- Asynchronous indexing jobs. Stale running rows are recovered on startup;
-- the age threshold prevents one live server instance from stealing another's job.
CREATE TABLE IF NOT EXISTS index_jobs (
  job_id text PRIMARY KEY,
  unit_id text NOT NULL REFERENCES units(unit_id) ON DELETE CASCADE,
  doc_type text NOT NULL DEFAULT '',
  filename text NOT NULL DEFAULT '',
  file_path text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT 'queued',
  progress integer NOT NULL DEFAULT 0,
  message text NOT NULL DEFAULT '',
  chunks integer NOT NULL DEFAULT 0,
  attempts integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
-- 已有库升级：任务队列需要记录源文件路径与重试次数（可重复执行）。
ALTER TABLE index_jobs ADD COLUMN IF NOT EXISTS file_path text NOT NULL DEFAULT '';
ALTER TABLE index_jobs ADD COLUMN IF NOT EXISTS attempts integer NOT NULL DEFAULT 0;
-- 任务阶段：pending（排队）/ parse（解析）/ index（索引）/ done（完成）。
ALTER TABLE index_jobs ADD COLUMN IF NOT EXISTS phase text NOT NULL DEFAULT 'pending';
CREATE INDEX IF NOT EXISTS index_jobs_unit_idx ON index_jobs(unit_id);
CREATE INDEX IF NOT EXISTS index_jobs_created_idx ON index_jobs(created_at DESC);

-- One row per indexed document version. Used for chunk-count replacement,
-- source-file cleanup and archive operations.
-- 文档是独立实体：doc_id 由正文哈希派生（doc-<16hex>），因此同一份内容
-- 只会存一份、只向量化一次。知识库通过 document_units 与之多对多关联，
-- 相当于「带标签的分组」，而不是文档的从属容器。
CREATE TABLE IF NOT EXISTS documents (
  doc_id text PRIMARY KEY,
  filename text NOT NULL DEFAULT '',
  doc_type text NOT NULL DEFAULT '',
  job_id text NOT NULL DEFAULT '',
  chunk_count integer NOT NULL DEFAULT 0,
  content_hash text NOT NULL DEFAULT '',
  file_path text NOT NULL DEFAULT '',
  indexed_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS documents_content_hash_idx ON documents(content_hash) WHERE content_hash <> '';

-- 文档 ↔ 知识库 多对多；从某个知识库移除只删这一行。
CREATE TABLE IF NOT EXISTS document_units (
  doc_id text NOT NULL REFERENCES documents(doc_id) ON DELETE CASCADE,
  unit_id text NOT NULL REFERENCES units(unit_id) ON DELETE CASCADE,
  added_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (doc_id, unit_id)
);
CREATE INDEX IF NOT EXISTS document_units_unit_idx ON document_units(unit_id);

-- Agent run tracing. Runs are best-effort: a failure to write them never
-- blocks a chat response.
CREATE TABLE IF NOT EXISTS agent_runs (
  run_id text PRIMARY KEY,
  unit_id text NOT NULL DEFAULT '',
  expert text NOT NULL DEFAULT '',
  question text NOT NULL DEFAULT '',
  skills jsonb NOT NULL DEFAULT '[]'::jsonb,
  status text NOT NULL DEFAULT 'running',
  steps integer NOT NULL DEFAULT 0,
  prompt_tokens integer NOT NULL DEFAULT 0,
  completion_tokens integer NOT NULL DEFAULT 0,
  error text NOT NULL DEFAULT '',
  started_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz
);
-- 运行详情需要保留触发本次运行的原始用户问题。
ALTER TABLE agent_runs ADD COLUMN IF NOT EXISTS question text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS agent_runs_started_idx ON agent_runs(started_at DESC);
CREATE INDEX IF NOT EXISTS agent_runs_unit_idx ON agent_runs(unit_id);

CREATE TABLE IF NOT EXISTS agent_steps (
  id bigserial PRIMARY KEY,
  run_id text NOT NULL REFERENCES agent_runs(run_id) ON DELETE CASCADE,
  step integer NOT NULL,
  kind text NOT NULL DEFAULT 'tool',
  tool text NOT NULL DEFAULT '',
  arguments jsonb NOT NULL DEFAULT '{}'::jsonb,
  result_preview text NOT NULL DEFAULT '',
  latency_ms bigint NOT NULL DEFAULT 0,
  error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS agent_steps_run_idx ON agent_steps(run_id, step);

-- 对话历史：一次会话对应多轮消息，用于 Agent 页面回看。
CREATE TABLE IF NOT EXISTS agent_conversations (
  conversation_id text PRIMARY KEY,
  unit_id text NOT NULL DEFAULT '',
  expert text NOT NULL DEFAULT '',
  title text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
-- 一次对话可同时关联多个项目；unit_id 保留第一个项目以兼容旧查询。
ALTER TABLE agent_conversations ADD COLUMN IF NOT EXISTS unit_ids jsonb NOT NULL DEFAULT '[]'::jsonb;
CREATE INDEX IF NOT EXISTS agent_conversations_updated_idx ON agent_conversations(updated_at DESC);
CREATE INDEX IF NOT EXISTS agent_conversations_unit_idx ON agent_conversations(unit_id);

CREATE TABLE IF NOT EXISTS agent_messages (
  id bigserial PRIMARY KEY,
  conversation_id text NOT NULL REFERENCES agent_conversations(conversation_id) ON DELETE CASCADE,
  role text NOT NULL DEFAULT 'user',
  content text NOT NULL DEFAULT '',
  citations jsonb NOT NULL DEFAULT '[]'::jsonb,
  run_id text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS agent_messages_conversation_idx ON agent_messages(conversation_id, id);

-- Runtime model API settings edited from the UI (single row, id = 1). Empty
-- values fall back to the EMBED_*/LLM_* environment variables, so existing
-- deployments keep working without touching this table.
CREATE TABLE IF NOT EXISTS model_settings (
  id smallint PRIMARY KEY,
  embed_base_url text NOT NULL DEFAULT '',
  embed_api_key text NOT NULL DEFAULT '',
  embed_model text NOT NULL DEFAULT '',
  llm_base_url text NOT NULL DEFAULT '',
  llm_api_key text NOT NULL DEFAULT '',
  llm_model text NOT NULL DEFAULT '',
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT model_settings_single_row CHECK (id = 1)
);

-- 多来源模型配置：一条记录是一个 OpenAI 兼容端点（可为 Ollama / vLLM / 云服务），
-- 对话模型与向量模型相互独立，可分别留空表示该来源不提供该能力。
-- model_selection 记录每个角色当前选中的来源；provider_id = 0（无行）时回落到
-- model_settings 手工配置，再回落到 EMBED_* / LLM_* 环境变量。
CREATE TABLE IF NOT EXISTS model_providers (
  id bigserial PRIMARY KEY,
  name text NOT NULL DEFAULT '',
  base_url text NOT NULL DEFAULT '',
  api_key text NOT NULL DEFAULT '',
  llm_model text NOT NULL DEFAULT '',
  embed_model text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS model_selection (
  role text PRIMARY KEY,
  provider_id bigint NOT NULL DEFAULT 0,
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT model_selection_role_check CHECK (role IN ('llm','embed'))
);

-- 实体知识图谱（P1：从 chunk 文本抽取，本地 LLM 完成）。
CREATE TABLE IF NOT EXISTS kg_entities (
  unit_id text NOT NULL REFERENCES units(unit_id) ON DELETE CASCADE,
  entity_id text NOT NULL,
  name text NOT NULL DEFAULT '',
  entity_type text NOT NULL DEFAULT '',
  description text NOT NULL DEFAULT '',
  source_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
  weight integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (unit_id, entity_id)
);
CREATE INDEX IF NOT EXISTS kg_entities_unit_idx ON kg_entities(unit_id);

CREATE TABLE IF NOT EXISTS kg_relations (
  unit_id text NOT NULL REFERENCES units(unit_id) ON DELETE CASCADE,
  relation_id text NOT NULL,
  src_id text NOT NULL,
  tgt_id text NOT NULL,
  keywords text NOT NULL DEFAULT '',
  description text NOT NULL DEFAULT '',
  source_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
  weight integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (unit_id, relation_id)
);
CREATE INDEX IF NOT EXISTS kg_relations_unit_idx ON kg_relations(unit_id);

-- 抽取任务状态（每个项目一条）。
CREATE TABLE IF NOT EXISTS kg_jobs (
  unit_id text PRIMARY KEY REFERENCES units(unit_id) ON DELETE CASCADE,
  status text NOT NULL DEFAULT 'idle',
  chunks_total integer NOT NULL DEFAULT 0,
  chunks_done integer NOT NULL DEFAULT 0,
  entities integer NOT NULL DEFAULT 0,
  relations integer NOT NULL DEFAULT 0,
  error text NOT NULL DEFAULT '',
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- 关系证据：抽取时要求模型回填原文片段，校验通过才入图。
ALTER TABLE kg_relations ADD COLUMN IF NOT EXISTS evidence text NOT NULL DEFAULT '';

-- Web search configuration (single row). Stored values override env defaults.
CREATE TABLE IF NOT EXISTS web_search_settings (
  id smallint PRIMARY KEY DEFAULT 1,
  enabled boolean NOT NULL DEFAULT false,
  provider text NOT NULL DEFAULT '',
  api_key text NOT NULL DEFAULT '',
  base_url text NOT NULL DEFAULT '',
  timeout_ms integer NOT NULL DEFAULT 15000,
  cache_ttl_seconds integer NOT NULL DEFAULT 900,
  cache_entries integer NOT NULL DEFAULT 200,
  allow_domains text NOT NULL DEFAULT '',
  deny_domains text NOT NULL DEFAULT '',
  auto_import boolean NOT NULL DEFAULT false,
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- Imported web pages keep their origin URL, both on the job and the document.
ALTER TABLE index_jobs ADD COLUMN IF NOT EXISTS source_url text NOT NULL DEFAULT '';
ALTER TABLE documents ADD COLUMN IF NOT EXISTS source_url text NOT NULL DEFAULT '';
