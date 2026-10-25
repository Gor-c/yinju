# 贡献指南

## 起环境

```sh
cp .env.example .env          # 填 PG_DSN、模型服务；不要提交这个文件
docker run -d --name kb-chroma -p 8000:8000 -v kb-chroma-data:/data chromadb/chroma:1.5.9
psql "$PG_DSN" -f deploy/schema.sql

go run ./cmd/server            # 8080/8081，自动加载 .env
cd frontend && pnpm install && pnpm dev
```

详细步骤（含 pgvector 模式、k8s Chroma、端口转发坑）见 [`docs/development.md`](docs/development.md)。

## 提交前必跑

```sh
go build ./... && go vet ./... && go test ./...
cd frontend && pnpm exec vue-tsc -b && pnpm run build
```

`go test` 不需要数据库：需要真实 PostgreSQL 的用例在 `PG_INTEGRATION_TEST` 未设置时会自己 `t.Skip`。

## 改动约定

代码规范、模块边界、前端约定都写在 [`docs/development.md`](docs/development.md) 第 9 节。最容易踩的四条：

- 文档列表只有一份实现（`DocumentExplorer`），`scope` 决定删除语义。
- `el-dialog` / `el-drawer` 的 class 挂在根元素自身，本体样式必须用复合选择器。
- 弹窗里只留一个滚动容器（`el-dialog__body` 要显式 flex）。
- 快捷提问只填入输入框，不代发。

## 不要提交

真实档案、`data/uploads/`、`data/instance.env`、`.env`、任何 API Key 或 MCP Token、真实评测集（`eval/questions.json` 已忽略，模板是 `eval/questions.example.json`）。

## Pull Request

- 一个 PR 只做一件事；标题写清「改了什么」，正文写清「为什么」和「怎么验证的」。
- 行为变更请附验证方式：命令输出、接口响应或截图，**不要只写「已测试」**。
- 新增接口/字段要同步 `docs/api.md`；改 schema 要同步 `deploy/schema.sql`。
- 修 bug 请说明触发条件；如果原来的代码「看起来对但语义错了」，把那个陷阱写进注释或文档。
