---
name: knowledge-base
description: Search and summarize project records, hazards, audits, and indexed archive documents through the Knowledge Base MCP connector.
---

# 知识库管理助手

Use the Knowledge Base MCP connector for questions about project records, hazards, audit history, and uploaded documents.

## Tool selection

- `list_units`: identify available projects and their `unit_id` before a scoped query.
- `get_unit_profile`: summarize a unit's type, industry, indexed chunk count, open hazards, and audit count.
- `query_hazards`: inspect findings for a unit. Pass `status` only when the user requests a specific state.
- `audit_history`: retrieve the unit's audit records, newest first.
- `vector_search`: search the unit's indexed documents. Include `unit_id`, a natural-language `query`, and optionally `top_k` from 1 to 20.

## Guidance

- Ask for or look up the unit before querying. Never assume a unit ID.
- Cite the filename returned by `vector_search` when summarizing document evidence. Distinguish retrieved facts from general guidance.
- If no matching records are returned, say the archive has no matching indexed information; do not invent details.
- Hazard status labels come from the source system. Preserve the returned wording when reporting status.
- These tools are read-only. Do not imply that a hazard was changed or an audit was submitted.

## Example requests

- “列出可查询的重点项目。”
- “查询项目 U001 当前未整改的隐患。”
- “搜索项目 U001 的预案中关于夜间值班的要求，并标注文档出处。”
