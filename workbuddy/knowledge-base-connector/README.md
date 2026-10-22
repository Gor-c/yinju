# WorkBuddy 知识库管理连接器

这是 WorkBuddy 的 MCP + Skill 连接器包。它通过远程 Streamable HTTP MCP 接入 Go 服务，提供项目列表、项目概况、隐患、审核历史和档案向量检索工具。

## 接入步骤

1. 部署 `knowledge-base` Go 服务，并为服务配置强随机 `MCP_AUTH_TOKEN`。
2. 在 `mcp.json` 中把 `https://YOUR_KNOWLEDGE_BASE_HOST/mcp` 换成可由 WorkBuddy 访问的 HTTPS 地址。
3. 将此目录作为 WorkBuddy 自定义连接器包导入；用户在连接时填写管理员发放的 MCP token。
4. 确认 WorkBuddy 能发现 `list_units`、`query_hazards`、`audit_history`、`vector_search` 等工具。

服务端 `MCP_AUTH_TOKEN` 和用户填写的 token 必须相同。不要将真实 token 写入连接器包或仓库。

## 本地协议检查

服务端 MCP endpoint 为 `POST /mcp`，采用无会话的 Streamable HTTP JSON 响应。当前实现支持 `2026-07-28` 无状态协议，并兼容使用 `2025-11-25` initialize 握手的客户端。部署时应通过 HTTPS 暴露端点，且确保 MCP 请求在 30 秒内返回。
