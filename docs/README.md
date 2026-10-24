# 文档索引

按「想做什么」找，而不是按文件名找。

## 我要用起来

| 目标 | 看这里 |
|---|---|
| 装起来、跑起来 | 根目录 [`README.md`](../README.md) |
| 改项目的名字、图标、配色 | [`brand.md`](brand.md)（含 7 套配色与图标规格） |
| 改数据库地址、向量库、端口 | [`instance-config.md`](instance-config.md) |
| 调召回条数、阈值、切块尺寸 | 页面「设置 → 检索与记忆 → 检索参数」，原理见 [`api.md`](api.md) 第 6 节 |
| 换个模型（对话 / 向量 / 重排） | 页面「设置 → 能力 → 模型服务」 |

## 我要改代码

| 目标 | 看这里 |
|---|---|
| 搞清分层与依赖方向 | [`development.md`](development.md) 第 1.1 节 |
| 判断一个新配置该放哪 | [`development.md`](development.md) 第 1.1.2 节「配置的两条通道」 |
| 加一路召回 / 加一个工具 | [`development.md`](development.md) 第 1.1.1 节 |
| 查接口契约 | [`api.md`](api.md) |
| 知道哪些坑还没填 | [`development.md`](development.md) 第 10 节「已知限制」 |

## 我要排查问题

| 现象 | 先看 |
|---|---|
| 检索答不上来 | 页面「检索参数」下方试问框 → 看是哪一层掐掉的（召回 / 精排 / 闸门） |
| 改了 `.env` 没生效 | [`instance-config.md`](instance-config.md) 的优先级一节：`instance.env` 优先于 `.env` |
| 服务起不来、连不上数据库 | [`instance-config.md`](instance-config.md) 的「改错了怎么办」 |
| 图标上传了不显示 | 多实例时 `BRAND_DIR` 是否共享；单实例看浏览器是否缓存了旧的 `logo_url` |
| 长对话忘了早先的约束 | 页面「记忆与上下文」：看摘要覆盖到第几条、事实是否被取代 |

## 文档之外还有两处

- **数据库表结构**：`deploy/schema.sql`（Chroma 模式）与 `deploy/schema-pgvector.sql`（pgvector 模式）。老库升级靠启动时的 `ensure*Schema` 自动补列，那些 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` 就是变更记录。
- **前端设计令牌**：`frontend/src/style.css` 顶部的 `:root` 与 `html.dark`。配色模板只改其中一部分，规则见 [`brand.md`](brand.md)。

## 写文档的约定

- **写「为什么」而不是「是什么」**。代码已经说明了是什么，文档要留下当时的取舍，否则半年后没人敢动。
- **给出可执行的动作**。不说「需要重建索引」，说「跑 `go run ./cmd/reindex`」。
- **已知限制如实写**。把没做的列出来，比假装完整有用。
