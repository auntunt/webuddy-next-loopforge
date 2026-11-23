"""Validate a model's proposed directory against repository evidence."""
from __future__ import annotations

import json
import re
import subprocess
from collections import Counter
from difflib import SequenceMatcher
from functools import cache
from pathlib import Path

from .entry_rules import KIND
from .fsutil import decode_source, git, read_text

KINDS = ("接口", "定时任务", "消息监听", "约定入口（待核实）", KIND, "命令")
MODEL = "模型提出"
PARSED = "工具解析"
# Brackets are part of paths such as app/api/[id]/route.ts.
REFERENCE = re.compile(r"((?:[\w./\\\[\]@-]|\([\w-]+\)/)+\.\w+|Dockerfile|Makefile):(\d+)(?:-(\d+))?")
KEY = re.compile(r"[\w-]+")
EXAMPLE = {"flows": [{"id": "collect-company", "name": "采集客户信息", "kind": "接口",
                      "trigger": "POST /api/collect", "entry": "app/api/collect/route.ts:12",
                      "steps": [{"name": "读取客户", "where": "lib/collect.ts:30-35"}],
                      "tables": {"COMPANY": "R"}, "domain": "collect"}],
           "tables": [{"name": "COMPANY", "where": "db/schema.sql:12"}]}


def evidence(root, where, *, allowed=None, relocate=None):
    """Check the same repository-relative reference in a proposal or a Markdown page."""
    m = REFERENCE.fullmatch(where) if isinstance(where, str) else None
    if not m:
        raise ValueError(f"{where}（证据格式应为 文件:行 或 文件:起行-止行）")
    file, first, last = m.groups()
    path = Path(file)
    fp = root / path
    if path.is_absolute() or ".." in path.parts or not fp.resolve().is_relative_to(root.resolve()):
        raise ValueError(f"{where}（须引用项目内文件）")
    if not fp.is_file():
        raise ValueError(f"{where}（文件不存在）")
    if allowed is not None and file not in allowed:
        raise ValueError(f"{where}（不属于索引源码）")
    lines = (read_text(fp) or "").splitlines()
    start, end = int(first), int(last or first)
    if relocate:
        start, end = relocate(file, start, end, lines)
    if not 1 <= start <= end <= len(lines):
        raise ValueError(f"{where}（行号越界或倒序，文件只有 {len(lines)} 行）")
    return {"where": where, "file": file, "line": start, "end": end}, "\n".join(lines[start-1:end])


def validate(root: Path, text: str, parsed_tables, parsed_flows, source_files, previous=None, *, committed=False):
    flows, tables, errors, duplicates = [], [], [], []
    result = flows, tables, errors, duplicates
    try:
        data = json.loads(text)
    except ValueError as exc:
        errors.append(f"structure.json：{exc}")
        return result
    if not isinstance(data, dict) or not all(isinstance(data.get(k), list) for k in ("tables", "flows")):
        errors.append("structure.json：flows 和 tables 必须都是数组")
        return result
    from .kb import page_path
    known = set(parsed_tables)
    page_names = {}
    for name in parsed_tables:
        page_names[page_path(root, "tbl:" + name).name.casefold()] = name
    triggers = {f["trigger"] for f in parsed_flows}
    locations = {(f["file"], f["line"]) for f in parsed_flows}
    parsed_ids = {f["id"] for f in parsed_flows}

    # A clean clone has no previous graph. The commit that supplied the unchanged
    # proposal also supplies its original source coordinates.
    @cache
    def base_commit():
        rev = git(root, "log", "-1", "--format=%H", "--", ".tmh/knowledge/structure.json")
        if rev and git(root, "show", rev + ":.tmh/knowledge/structure.json") == text.strip():
            return rev
        return None

    @cache
    def original(file):
        if committed and (rev := base_commit()):
            r = subprocess.run(["git", "show", rev + ":" + file], cwd=root, capture_output=True)
            if r.returncode == 0:
                return decode_source(r.stdout)
        return None

    changes = {}

    def cite(where, old_refs):
        def relocate(file, start, end, lines):
            old = original(file)
            if old is None:
                if where not in old_refs:
                    return start, end
                ref = old_refs[where]
                start, end = ref["line"], ref["end"]
                old = decode_source(previous.source_bytes(file))
            before = old.splitlines()
            if not 1 <= start <= end <= len(before):
                raise ValueError(f"{where}（原文件行号越界或倒序）")
            if file not in changes:
                changes[file] = [] if before == lines else SequenceMatcher(None, before, lines, autojunk=False).get_opcodes()
            edits = changes[file]
            if not edits:
                return start, end
            spans = []
            for tag, a, b, c, d in edits:
                if b <= start - 1 or a >= end or tag == "delete":
                    continue
                if tag == "equal":
                    spans.append((c + max(start - 1, a) - a, c + min(end, b) - a))
                else:
                    spans.append((c, d))
            if not spans:
                raise ValueError(f"{where}（所引行已删除或超出原文件）")
            return spans[0][0] + 1, spans[-1][1]

        return evidence(root, where, allowed=source_files, relocate=relocate)

    def fields(obj, names):
        if not isinstance(obj, dict):
            raise ValueError("需要 JSON 对象")
        for k in names.split():
            if not isinstance(obj.get(k), str) or not obj[k].strip():
                raise ValueError(f"{k} 必须是非空字符串")

    for group, key in (("tables", "name"), ("flows", "id")):
        counts = Counter(row[key] for row in data[group] if isinstance(row, dict) and isinstance(row.get(key), str))
        used_triggers = set()
        for i, row in enumerate(data[group]):
            label = f"{group}[{i}]"
            try:
                fields(row, key)
                value = row[key]
                label += f" {value}"
                if counts[value] > 1:
                    raise ValueError(f"{key} 重复")
                nid = ("tbl:" if group == "tables" else "flow:") + value
                old = previous.node(nid) if previous else None
                refs = {r["where"]: r for r in old["data"].get("citations", [])} if old else {}
                if group == "tables":
                    fields(row, "where")
                    ref, code = cite(row["where"], refs)
                    if value in known:
                        duplicates.append(f"{label}：已有工具解析的表")
                    else:
                        filename = page_path(root, nid).name.casefold()
                        if filename in page_names:
                            raise ValueError(f"表名与 {page_names[filename]} 的知识页文件名冲突")
                        page_names[filename] = value
                        tables.append((value, ref, code))
                        known.add(value)
                    continue
                fields(row, "name kind trigger entry domain")
                if not KEY.fullmatch(value) or value != value.lower() or not KEY.fullmatch(row["domain"]):
                    raise ValueError("id 和 domain 只允许字母、数字、下划线或连字符，id 使用小写")
                if row["kind"] not in KINDS:
                    raise ValueError("种类非法：" + row["kind"])
                if not isinstance(row.get("steps"), list) or not isinstance(row.get("tables"), dict):
                    raise ValueError("steps 必须是数组，tables 必须是对象")
                entry, body = cite(row["entry"], refs)
                citations, code = [dict(entry, name="入口")], [body]
                for step in row["steps"]:
                    fields(step, "name where")
                    ref, body = cite(step["where"], refs)
                    citations.append(dict(ref, name=step["name"]))
                    code.append(body)
                for table, ops in row["tables"].items():
                    if table not in known:
                        raise ValueError("表不存在：" + table)
                    if not isinstance(ops, str) or not re.fullmatch("[CRUD]+", ops) or len(set(ops)) != len(ops):
                        raise ValueError(f"{table} 操作须为 C、R、U、D 的不重复组合")
                if row["trigger"] in triggers or (entry["file"], entry["line"]) in locations:
                    duplicates.append(f"{label}：已有工具解析的入口 {row['trigger']}")
                elif row["trigger"] in used_triggers or value in parsed_ids:
                    raise ValueError("入口 trigger 重复或 id 与已有业务流冲突")
                else:
                    used_triggers.add(row["trigger"])
                    flows.append((row, citations, code))
            except ValueError as exc:
                errors.append(f"{label}：{exc}")
    return result


class InvalidProposal(ValueError):
    def __init__(self, errors):
        self.errors = errors
        super().__init__("\n".join(errors))
