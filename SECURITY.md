# 安全说明

## 这个项目默认**没有**身份认证

`/api/v1/*` 下的所有接口默认不校验调用方身份，包括 `/api/v1/instance/config`（会返回数据库主机、库名与用户名）。**不要把它直接暴露到公网。**

上线前至少做一件事：

- 在反向代理层加认证（Basic Auth / OAuth / 内网 IP 白名单），或
- 设置 `API_TOKEN` 并保证服务只在内网可达。

MCP endpoint `/mcp` 是另一套鉴权：必须配置强随机 `MCP_AUTH_TOKEN`，且只通过 HTTPS 暴露。没有 token 的请求会被拒绝（401），但这与 `/api/v1/*` 无关。

## 已知的取舍（不是待修的 bug）

| 项 | 现状 | 为什么这样做 |
|---|---|---|
| 实例配置明文存密码 | `data/instance.env` 权限 `0600` | 加密需要密钥，密钥又要存在某处，会让「数据库配错后还能打开页面回滚」这件事失效 |
| 上传只校验扩展名 | 文档上传看后缀 | 品牌图标已按魔数校验；文档侧依赖上游解析器，尚未加魔数 |
| 无多租户 | 单实例、单一数据域 | 见 `docs/development.md` 的「已知限制」 |

## 报告漏洞

请使用 GitHub 的 **Security → Report a vulnerability**（私密 Security Advisory）提交，不要在公开 issue 里贴利用细节或真实数据。

报告时请附上：受影响版本（`/health` 里的 `version`）、复现步骤、影响范围。**不要提交真实的档案内容、公司名或个人信息**，用最小的合成样例即可。

## 不要提交到仓库的东西

- `.env`、`data/instance.env`（含数据库密码）
- `data/uploads/`、`data/brand/`（真实档案与图标）
- 真实的评测集（`eval/questions.json` 已被忽略；公开版本是 `eval/questions.example.json`）
- 任何 API Key、MCP Token、cookie 或抓包内容
