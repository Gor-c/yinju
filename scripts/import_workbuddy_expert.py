#!/usr/bin/env python3
"""把 WorkBuddy 专家包里的 agent 定义转换成本项目可上传的 JSON。

用法：
    python3 scripts/import_workbuddy_expert.py <专家目录> [--tools a,b] [--name 中文名] [-o out.json]

设计取舍：
- id 取目录名（已是 kebab-case，符合 `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`）。
- name 优先用 frontmatter 里的 displayName.zh，其次 displayName.en，最后用 id。
  直接上传 md 时 name 会变成英文 id，界面上不好认。
- tools 默认留空并给出警告：本项目只有四个只读工具，留空等于放开全部，
  可能与专家定位冲突；应当由调用方显式指定。
- 正文里指向 `skills/...` 的相对路径在本项目里不存在（那些 references 不会随包导入），
  默认剔除这类引用行，避免提示词指挥模型去读一个读不到的文件。
"""
from __future__ import annotations

import argparse
import json
import pathlib
import re
import sys

VALID_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$")
TOOL_NAMES = ("get_unit_profile", "query_hazards", "audit_history", "vector_search")


def split_front_matter(text: str) -> tuple[dict[str, str], dict[str, str], str]:
    """返回 (顶层字段, 嵌套字段, 正文)。

    只处理形如 `key: value` 与
        key:
          zh: 值
          en: value
    的两类写法，足够覆盖专家包 frontmatter；复杂 YAML 交给人工。
    """
    text = text.replace("\r\n", "\n")
    if not text.startswith("---\n"):
        return {}, {}, text.strip()
    end = text.find("\n---", 4)
    if end < 0:
        return {}, {}, text.strip()
    head = text[4:end].split("\n")
    body = text[end + 4:].strip()

    flat: dict[str, str] = {}
    nested: dict[str, str] = {}
    current = ""
    for raw in head:
        if not raw.strip() or raw.strip().startswith("#"):
            continue
        indented = raw.startswith((" ", "\t"))
        key, sep, value = raw.partition(":")
        if not sep:
            continue
        key, value = key.strip().lower(), value.strip().strip("\"'")
        if indented and current:
            # 嵌套字段按 `父键.子键` 记录，例如 displayname.zh
            nested[f"{current}.{key}"] = value
            continue
        current = key
        flat[key] = value
    return flat, nested, body


def drop_dead_paths(body: str) -> tuple[str, int]:
    """剔除指向不存在资产的引用行（skills/ 与 ../skills/ 开头或内嵌的路径）。"""
    kept, dropped = [], 0
    for line in body.split("\n"):
        if re.search(r"(\.\./)?skills/[A-Za-z0-9_./-]+\.md", line):
            dropped += 1
            continue
        kept.append(line)
    cleaned = re.sub(r"\n{3,}", "\n\n", "\n".join(kept)).strip()
    return cleaned, dropped


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("expert_dir", help="专家目录，例如 ~/.workbuddy/plugins/marketplaces/my-experts/plugins/knowledge-library-ops")
    ap.add_argument("--tools", default="", help="逗号分隔的工具名，留空表示不声明（会放开全部四个）")
    ap.add_argument("--name", default="", help="覆盖显示名，默认取 displayName")
    ap.add_argument("-o", "--out", default="", help="输出文件路径，默认打印到标准输出")
    args = ap.parse_args()

    src_dir = pathlib.Path(args.expert_dir).expanduser().resolve()
    agents_dir = src_dir / "agents"
    if not agents_dir.is_dir():
        print(f"错误：{agents_dir} 不存在", file=sys.stderr)
        return 1
    md_files = sorted(agents_dir.glob("*.md"))
    if not md_files:
        print(f"错误：{agents_dir} 下没有 .md 定义", file=sys.stderr)
        return 1
    if len(md_files) > 1:
        print(f"提示：发现 {len(md_files)} 个定义，只处理 {md_files[0].name}", file=sys.stderr)

    flat, nested, body = split_front_matter(md_files[0].read_text(encoding="utf-8"))
    body, dropped = drop_dead_paths(body)
    if not body:
        print("错误：正文为空，无法作为 system_prompt", file=sys.stderr)
        return 1

    expert_id = src_dir.name
    if not VALID_ID.match(expert_id):
        print(f"错误：目录名 {expert_id!r} 不能作为 id（只能字母数字下划线短横线）", file=sys.stderr)
        return 1

    display = args.name or nested.get("displayname.zh") or nested.get("displayname.en") or expert_id
    tools = [t.strip() for t in args.tools.split(",") if t.strip()]
    unknown = [t for t in tools if t not in TOOL_NAMES]
    if unknown:
        print(f"警告：这些工具不存在，会被服务端静默丢弃：{', '.join(unknown)}", file=sys.stderr)
        tools = [t for t in tools if t in TOOL_NAMES]
    if not tools:
        print("警告：未声明 tools，导入后该专家会看到全部四个工具。"
              f"建议用 --tools 指定，可选：{', '.join(TOOL_NAMES)}", file=sys.stderr)

    payload = {
        "id": expert_id,
        "name": display,
        "description": flat.get("description", ""),
        "system_prompt": body,
        "tools": tools,
    }
    text = json.dumps(payload, ensure_ascii=False, indent=2)
    if args.out:
        pathlib.Path(args.out).write_text(text, encoding="utf-8")
        print(f"已写入 {args.out}：system_prompt {len(body)} 字符，剔除 {dropped} 行失效路径引用", file=sys.stderr)
    else:
        print(text)
        print(f"# system_prompt {len(body)} 字符，剔除 {dropped} 行失效路径引用", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
