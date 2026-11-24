#!/usr/bin/env python3
"""Webuddy Knowledge Base & Code Graph CLI Bridge for LoopForge.

Provides deterministic JSON commands:
  - scan --repo <path>
  - overview [--repo <path>]
  - search [--repo <path>] --query <text> [--limit 30]
  - read [--repo <path>] --path <path> [--start 0] [--end 0]
  - verify-evidence --repo <path> --ref <file:line[-end]>
"""
from __future__ import annotations

import argparse
import json
import os
import re
import sys
import time
from pathlib import Path

BASE_DIR = Path(__file__).resolve().parent
if str(BASE_DIR) not in sys.path:
    sys.path.insert(0, str(BASE_DIR))

BUILTIN_KB_DIR = BASE_DIR / "kb"

_FK_TABLE_RE = re.compile(
    r"FOREIGN\s+KEY\s*\(\s*([^)]+)\s*\)\s*REFERENCES\s+([A-Za-z0-9_.\"`\[\]]+)(?:\s*\(\s*([^)]+)\s*\))?",
    re.I,
)
_FK_INLINE_RE = re.compile(
    r"\bREFERENCES\s+([A-Za-z0-9_.\"`\[\]]+)(?:\s*\(\s*([^)]+)\s*\))?",
    re.I,
)
_TABLE_CONSTRAINT_RE = re.compile(
    r"^(?:PRIMARY\s+KEY|UNIQUE|CHECK|CONSTRAINT|FOREIGN\s+KEY|INDEX|KEY)\b", re.I
)


def _strip_ident(raw: str) -> str:
    return (raw or "").strip().strip('"`[]').split(".")[-1]


def _parse_table_ddl(ddl: str, table_name_map: dict[str, str]) -> tuple[list[dict], list[dict]]:
    if not ddl or "(" not in ddl or ")" not in ddl:
        return [], []
    start = ddl.find("(")
    end = ddl.rfind(")")
    if end <= start:
        return [], []
    body = ddl[start + 1 : end]
    raw_items = [ln.strip() for ln in body.splitlines() if ln.strip()]
    if len(raw_items) <= 1:
        raw_items, buf, depth = [], [], 0
        for ch in body:
            if ch == "(":
                depth += 1
            elif ch == ")":
                depth = max(0, depth - 1)
            if ch == "," and depth == 0:
                piece = "".join(buf).strip()
                if piece:
                    raw_items.append(piece)
                buf = []
            else:
                buf.append(ch)
        tail = "".join(buf).strip()
        if tail:
            raw_items.append(tail)

    columns, fks, col_fk_map = [], [], {}
    for raw in raw_items:
        item = raw.rstrip(",").strip()
        if not item or item.startswith("--"):
            continue
        m_tbl_fk = _FK_TABLE_RE.search(item)
        if m_tbl_fk:
            from_col = _strip_ident(m_tbl_fk.group(1).split(",")[0])
            raw_target = _strip_ident(m_tbl_fk.group(2))
            to_table = table_name_map.get(raw_target.lower(), raw_target)
            to_col = _strip_ident((m_tbl_fk.group(3) or "id").split(",")[0]) or "id"
            fk_obj = {"table": to_table, "column": to_col}
            col_fk_map[from_col.lower()] = fk_obj
            fks.append({"from_col": from_col, "to_table": to_table, "to_col": to_col})
            continue
        if _TABLE_CONSTRAINT_RE.match(item):
            continue
        parts = item.split()
        if len(parts) < 2:
            continue
        col_name = _strip_ident(parts[0])
        if not col_name:
            continue
        col_type = parts[1].rstrip(",")
        is_pk = bool(re.search(r"\bPRIMARY\s+KEY\b", item, re.I))
        fk_obj = None
        m_inline = _FK_INLINE_RE.search(item)
        if m_inline:
            raw_target = _strip_ident(m_inline.group(1))
            to_table = table_name_map.get(raw_target.lower(), raw_target)
            to_col = _strip_ident((m_inline.group(2) or "id").split(",")[0]) or "id"
            fk_obj = {"table": to_table, "column": to_col}
            fks.append({"from_col": col_name, "to_table": to_table, "to_col": to_col})
        columns.append({"name": col_name, "type": col_type, "pk": is_pk, "fk": fk_obj})

    for col in columns:
        if not col["fk"] and col["name"].lower() in col_fk_map:
            col["fk"] = col_fk_map[col["name"].lower()]
    return columns, fks


def _builtin_docs() -> list[dict]:
    docs = []
    if not BUILTIN_KB_DIR.is_dir():
        return docs
    for fp in sorted(BUILTIN_KB_DIR.glob("*.md")):
        text = fp.read_text(encoding="utf-8", errors="replace")
        title = fp.stem
        for line in text.splitlines():
            if line.startswith("# "):
                title = line[2:].strip()
                break
        docs.append({
            "source": "builtin",
            "path": f"builtin:{fp.name}",
            "name": fp.name,
            "title": title,
            "size": len(text),
        })
    return docs


def _overview(repo_path: str | None) -> dict:
    builtins = _builtin_docs()
    if not repo_path:
        return {
            "built": False,
            "repoPath": "",
            "domains": [],
            "flows": [],
            "tables": [],
            "erEdges": [],
            "crudMatrix": [],
            "pages": [],
            "builtinDocs": builtins,
        }

    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")

    from tmh import kb
    from tmh.graph import Graph, graph_path

    pages = []
    kb_dir = root / ".tmh" / "knowledge"
    if kb_dir.is_dir():
        for fp in sorted(kb_dir.rglob("*.md")):
            rel = fp.relative_to(root).as_posix()
            pages.append({
                "source": "project",
                "path": rel,
                "name": fp.name,
                "size": fp.stat().st_size,
            })

    gp = graph_path(root)
    if not gp.exists():
        return {
            "built": False,
            "repoPath": str(root),
            "pageCount": len(pages),
            "domains": [],
            "flows": [],
            "tables": [],
            "erEdges": [],
            "crudMatrix": [],
            "pages": pages[:300],
            "builtinDocs": builtins,
        }

    g = Graph(root)
    flows_raw = g.nodes("flow")
    tables_raw = g.nodes("tbl")
    dom_nodes = g.nodes("dom")
    reg = kb.load_registry(root)
    path_to_meta = {}
    page_stats = {"total": 0, "confirmed": 0, "pending": 0, "stale": 0}
    if isinstance(reg, dict):
        for pid, meta in reg.items():
            if not isinstance(meta, dict):
                continue
            try:
                rel_p = kb.page_path(root, pid).relative_to(root).as_posix()
            except Exception:
                rel_p = ""
            st = str(meta.get("status") or "待确认")
            page_stats["total"] += 1
            if st == "已确认":
                page_stats["confirmed"] += 1
            elif st in ("已过期", "stale"):
                page_stats["stale"] += 1
            else:
                page_stats["pending"] += 1
            if rel_p:
                path_to_meta[rel_p] = {
                    "pageId": pid,
                    "title": str(meta.get("title") or pid),
                    "scope": str(meta.get("scope") or ""),
                    "status": st,
                    "summary": str(meta.get("summary") or "")[:180],
                    "updated": str(meta.get("updated") or ""),
                }

    special_labels = [
        ("index.md", "项目知识总览"),
        ("glossary.md", "业务术语表"),
        ("decisions.md", "架构决策记录 (ADR)"),
        ("pitfalls.md", "历史踩坑与避坑"),
        ("open-questions.md", "待澄清问题池"),
    ]
    special_docs = []
    kb_dir = root / ".tmh" / "knowledge"
    if kb_dir.is_dir():
        for fname, label in special_labels:
            fp = kb_dir / fname
            if fp.is_file():
                special_docs.append({
                    "name": fname,
                    "title": label,
                    "path": fp.relative_to(root).as_posix(),
                    "size": fp.stat().st_size,
                })

    for pg in pages:
        m = path_to_meta.get(pg["path"])
        if m:
            pg.update(m)
        else:
            p = pg["path"]
            pg["pageId"] = ""
            pg["title"] = pg["name"].replace(".md", "")
            pg["scope"] = "业务域" if "/domains/" in p else "业务流程" if "/flows/" in p else "数据实体" if "/entities/" in p else "总览与规范"
            pg["status"] = "待确认"
            pg["summary"] = ""
            pg["updated"] = ""

    open_q_count = 0
    try:
        from tmh import kbqa
        qs = kbqa.questions(root)
        open_q_count = sum(1 for q in qs if str(q.get("status") or "").strip() in ("", "open", "待确认", "待澄清"))
    except Exception:
        open_q_count = 0

    dom_title = {}
    for n in dom_nodes:
        dom_title[n["name"]] = n["label"] or n["name"]
        if n["id"].startswith("dom:"):
            dom_title[n["id"][4:]] = n["label"] or n["name"]

    dom_counts: dict[str, int] = {}
    flows = []
    crud_matrix = []
    for f in flows_raw:
        d = f.get("domain") or "（未划分）"
        dom_counts[d] = dom_counts.get(d, 0) + 1
        dd = f.get("data") or {}
        crud = dd.get("crud") or {}
        flows.append({
            "id": f["id"],
            "name": f.get("name") or f["id"],
            "label": f.get("label") or f.get("name") or f["id"],
            "kind": dd.get("kind") or "",
            "trigger": dd.get("trigger") or "",
            "handler": dd.get("handler") or "",
            "file": f.get("file") or "",
            "line": f.get("line") or 0,
            "domain": d,
            "domainTitle": dom_title.get(d, d),
            "crud": crud,
            "pageStatus": (reg.get(f["id"]) or {}).get("status", ""),
        })
        if crud:
            crud_matrix.append({
                "flowId": f["id"],
                "flowName": f.get("name") or f["id"],
                "domain": d,
                "tables": crud,
            })

    table_name_map = {(t.get("name") or "").lower(): (t.get("name") or "") for t in tables_raw}
    tables = []
    er_edges = []
    for t in tables_raw:
        dd = t.get("data") or {}
        ddl = dd.get("ddl") or ""
        cols, fks = _parse_table_ddl(ddl, table_name_map)
        tname = t.get("name") or t["id"]
        tables.append({
            "id": t["id"],
            "name": tname,
            "entity": dd.get("entity") or "",
            "file": t.get("file") or "",
            "line": t.get("line") or 0,
            "columns": cols,
            "users": len(g.inn(t["id"], "uses")),
        })
        for fk in fks:
            er_edges.append({
                "fromTable": tname,
                "fromColumn": fk["from_col"],
                "toTable": fk["to_table"],
                "toColumn": fk["to_col"],
            })

    domains = [
        {"key": k, "title": dom_title.get(k, k), "flowCount": v}
        for k, v in sorted(dom_counts.items(), key=lambda x: -x[1])
    ]

    return {
        "built": True,
        "repoPath": str(root),
        "builtAt": time.strftime("%Y-%m-%d %H:%M", time.localtime(gp.stat().st_mtime)),
        "flowCount": len(flows),
        "tableCount": len(tables),
        "pageCount": len(pages),
        "pageStats": page_stats,
        "specialDocs": special_docs,
        "openQuestionCount": open_q_count,
        "flowsWithTables": len(crud_matrix),
        "coverage": kb.coverage(reg),
        "domains": domains,
        "flows": flows[:300],
        "tables": tables[:200],
        "erEdges": er_edges[:300],
        "crudMatrix": crud_matrix[:200],
        "pages": pages[:500],
        "builtinDocs": builtins,
    }


def _scan(repo_path: str) -> dict:
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")
    from tmh.graph import build

    stats = build(root, use_cache=True)
    ov = _overview(str(root))
    ov["scanStats"] = stats
    return ov


def _search(repo_path: str | None, query: str, limit: int) -> dict:
    q = (query or "").strip()
    if not q:
        return {"query": "", "hits": []}

    hits = []
    q_lower = q.lower()
    terms = [t for t in re.split(r"\s+", q_lower) if t]

    # 1. Search project graph + KB via tmh.context.search
    if repo_path:
        root = Path(repo_path).expanduser().resolve()
        if root.is_dir():
            from tmh.graph import Graph, graph_path
            if graph_path(root).exists():
                from tmh.context import search as ctx_search
                g = Graph(root)
                for score, item in ctx_search(root, q, limit=limit):
                    nid = item.get("id", "")
                    node = g.node(nid) if nid else None
                    file_path = (node or {}).get("file") or item.get("file") or ""
                    line = (node or {}).get("line") or item.get("line") or 0
                    hits.append({
                        "source": "graph",
                        "kind": item.get("type") or nid.split(":")[0],
                        "id": nid,
                        "title": (node or {}).get("label") or (node or {}).get("name") or nid,
                        "path": file_path,
                        "line": line,
                        "snippet": (item.get("summary") or item.get("text") or "")[:280],
                        "score": round(float(score), 2),
                    })

            kb_dir = root / ".tmh" / "knowledge"
            if kb_dir.is_dir():
                pages_json = kb_dir / ".pages.json"
                if pages_json.is_file():
                    try:
                        from tmh import kb as tmh_kb
                        reg = json.loads(pages_json.read_text(encoding="utf-8", errors="replace"))
                        if isinstance(reg, dict):
                            for key, meta in reg.items():
                                if not isinstance(meta, dict):
                                    continue
                                title = str(meta.get("title") or key)
                                summary = str(meta.get("summary") or "")
                                scope = str(meta.get("scope") or "")
                                hay = f"{key} {title} {summary} {scope}".lower()
                                if all(t in hay for t in terms):
                                    try:
                                        rel_p = tmh_kb.page_path(root, key).relative_to(root).as_posix()
                                    except Exception:
                                        rel_p = f".tmh/knowledge/{key}.md"
                                    hits.append({
                                        "source": "project-kb",
                                        "kind": scope or "page",
                                        "id": rel_p,
                                        "title": title,
                                        "path": rel_p,
                                        "line": 1,
                                        "snippet": summary[:240] or title,
                                        "score": 8.5 if q_lower in title.lower() else 8.0,
                                    })
                    except Exception:
                        pass
                else:
                    deadline = time.monotonic() + 2.5
                    for fp in sorted(kb_dir.rglob("*.md"))[:120]:
                        if time.monotonic() > deadline:
                            break
                        text = fp.read_text(encoding="utf-8", errors="replace")
                        low = text.lower()
                        if all(t in low for t in terms):
                            idx = low.find(terms[0])
                            start = max(0, idx - 60)
                            snippet = text[start : start + 240].replace("\n", " ").strip()
                            hits.append({
                                "source": "project-kb",
                                "kind": "page",
                                "id": fp.relative_to(root).as_posix(),
                                "title": fp.stem,
                                "path": fp.relative_to(root).as_posix(),
                                "line": 1,
                                "snippet": snippet,
                                "score": 8.0,
                            })

    # 2. Search built-in domain KB (kb/*.md)
    if BUILTIN_KB_DIR.is_dir():
        for fp in sorted(BUILTIN_KB_DIR.glob("*.md")):
            text = fp.read_text(encoding="utf-8", errors="replace")
            low = text.lower()
            matched = sum(1 for t in terms if t in low)
            if matched > 0:
                idx = low.find(next(t for t in terms if t in low))
                start = max(0, idx - 60)
                snippet = text[start : start + 240].replace("\n", " ").strip()
                title = fp.stem
                for line in text.splitlines():
                    if line.startswith("# "):
                        title = line[2:].strip()
                        break
                hits.append({
                    "source": "builtin",
                    "kind": "domain-kb",
                    "id": f"builtin:{fp.name}",
                    "title": title,
                    "path": f"builtin:{fp.name}",
                    "line": 1,
                    "snippet": snippet,
                    "score": round(5.0 * matched / max(1, len(terms)), 2),
                })

    hits.sort(key=lambda h: -h["score"])
    return {"query": q, "hits": hits[:limit]}


def _read(repo_path: str | None, rel_path: str, start: int, end: int) -> dict:
    if not rel_path:
        raise ValueError("path 不能为空")
    if rel_path.startswith("builtin:"):
        name = Path(rel_path.split(":", 1)[1]).name
        fp = (BUILTIN_KB_DIR / name).resolve()
        if not fp.is_relative_to(BUILTIN_KB_DIR.resolve()) or not fp.is_file():
            raise ValueError(f"内置知识文档不存在: {name}")
        source = "builtin"
    else:
        if not repo_path:
            raise ValueError("读取项目文件需要提供 repoPath")
        root = Path(repo_path).expanduser().resolve()
        candidate = Path(rel_path)
        if candidate.is_absolute() or ".." in candidate.parts:
            raise ValueError("路径必须是仓库内相对路径")
        fp = (root / candidate).resolve()
        if not fp.is_relative_to(root) or not fp.is_file():
            raise ValueError(f"文件不存在: {rel_path}")
        source = "project"

    lines = fp.read_text(encoding="utf-8", errors="replace").splitlines()
    total = len(lines)
    s = max(1, start) if start > 0 else 1
    e = min(total, end) if end > 0 else total
    if e < s:
        e = s
    sliced = "\n".join(lines[s - 1 : e])
    return {
        "source": source,
        "path": rel_path,
        "start": s,
        "end": e,
        "totalLines": total,
        "content": sliced,
    }


def _verify_evidence(repo_path: str, ref: str) -> dict:
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")
    from tmh.kb_structure import evidence

    meta, snippet = evidence(root, ref)
    return {
        "valid": True,
        "where": meta["where"],
        "file": meta["file"],
        "line": meta["line"],
        "end": meta["end"],
        "snippet": snippet,
    }


def _parse_mermaid_states(mermaid_text: str) -> tuple[list[dict], list[dict]]:
    labels: dict[str, str] = {}
    for m in re.finditer(r'state\s+"([^"]+)"\s+as\s+([\w.-]+)', mermaid_text or ""):
        labels[m.group(2)] = m.group(1).strip()
    nodes_order: list[str] = []
    edges: list[dict] = []
    for line in (mermaid_text or "").splitlines():
        s = line.strip()
        m = re.match(r'^(\[\*\]|[\w.-]+)\s*-->\s*(\[\*\]|[\w.-]+)(?:\s*:\s*(.+))?$', s)
        if not m:
            continue
        src, dst, lbl = m.group(1), m.group(2), (m.group(3) or "").strip()
        for sid in (src, dst):
            if sid != "[*]" and sid not in nodes_order:
                nodes_order.append(sid)
        edges.append({"from": src, "to": dst, "label": lbl})
    nodes = [{"id": sid, "label": labels.get(sid, sid)} for sid in nodes_order]
    return nodes, edges


def _statecharts(repo_path: str | None) -> dict:
    if not repo_path:
        return {"repoPath": "", "charts": []}
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")

    charts: list[dict] = []

    # 1. Native .tmh/knowledge/statecharts/*.json
    sc_dir = root / ".tmh" / "knowledge" / "statecharts"
    if sc_dir.is_dir():
        for fp in sorted(sc_dir.glob("*.json")):
            try:
                data = json.loads(fp.read_text(encoding="utf-8", errors="replace"))
            except Exception:
                continue
            mermaid = str(data.get("mermaid") or "")
            nodes, edges = _parse_mermaid_states(mermaid)
            states_meta = data.get("states") if isinstance(data.get("states"), dict) else {}
            for n in nodes:
                meta = states_meta.get(n["id"]) or {}
                if isinstance(meta, dict):
                    n["where"] = str(meta.get("where") or "")
                    n["summary"] = str(meta.get("summary") or "")
            charts.append({
                "id": fp.stem,
                "title": str(data.get("title") or fp.stem),
                "domain": str(data.get("domain") or ""),
                "sourceKind": "native",
                "sourceLabel": "状态图文件 (.tmh/knowledge/statecharts)",
                "status": str(data.get("status") or "待确认"),
                "commit": str(data.get("commit") or ""),
                "mermaid": mermaid,
                "nodes": nodes,
                "edges": edges,
                "rules": data.get("rules") if isinstance(data.get("rules"), list) else [],
                "paths": data.get("paths") if isinstance(data.get("paths"), list) else [],
            })

    # 1.5 Scan top-level .tmh/knowledge/*.md for fenced ```mermaid blocks (bounded to avoid slow iCloud/large-tree I/O)
    kb_dir = root / ".tmh" / "knowledge"
    if kb_dir.is_dir():
        mermaid_re = re.compile(r"```mermaid\s*(.*?)```", re.S | re.I)
        deadline = time.monotonic() + 2.0
        for fp in sorted(kb_dir.glob("*.md"))[:40]:
            if time.monotonic() > deadline:
                break
            text = fp.read_text(encoding="utf-8", errors="replace")
            if "```mermaid" not in text.lower():
                continue
            for idx, m in enumerate(mermaid_re.finditer(text), 1):
                body = m.group(1).strip()
                nodes, edges = _parse_mermaid_states(body)
                if not nodes:
                    continue
                rel = fp.relative_to(root).as_posix()
                charts.append({
                    "id": f"md-{fp.stem}-{idx}",
                    "title": f"{fp.stem} · Mermaid 图 #{idx}",
                    "domain": fp.parent.name,
                    "sourceKind": "native",
                    "sourceLabel": f"知识页 Mermaid ({rel})",
                    "status": "知识页图示",
                    "commit": "",
                    "mermaid": body,
                    "nodes": nodes,
                    "edges": edges,
                    "rules": [],
                    "paths": [],
                })

    # 2. Derive real domain flow-lifecycle statecharts from graph.db
    from tmh.graph import Graph, graph_path
    gp = graph_path(root)
    if gp.exists():
        g = Graph(root)
        flows_raw = g.nodes("flow")
        dom_title = {}
        for dn in g.nodes("dom"):
            dom_title[dn["name"]] = dn["label"] or dn["name"]
            if dn["id"].startswith("dom:"):
                dom_title[dn["id"][4:]] = dn["label"] or dn["name"]
        by_dom: dict[str, list[dict]] = {}
        for f in flows_raw:
            d = f.get("domain") or "（未划分）"
            by_dom.setdefault(d, []).append(f)

        top_doms = sorted(by_dom.items(), key=lambda kv: -len(kv[1]))[:12]
        for dkey, dflows in top_doms:
            # Group flows by lifecycle action (create/submit/query/update/audit/delete)
            stages = [
                ("entry", "入口接入 / 查询", []),
                ("create", "创建 / 发起", []),
                ("process", "业务处理 / 变更", []),
                ("settle", "审批 / 校验 / 终态", []),
            ]
            for f in dflows:
                nm = ((f.get("name") or "") + " " + ((f.get("data") or {}).get("trigger") or "")).lower()
                if any(k in nm for k in ("add", "create", "new", "save", "insert", "init", "新增", "创建", "保存")):
                    stages[1][2].append(f)
                elif any(k in nm for k in ("approve", "audit", "confirm", "finish", "close", "delete", "del", "cancel", "审批", "确认", "审核", "删除", "关闭")):
                    stages[3][2].append(f)
                elif any(k in nm for k in ("update", "edit", "modify", "sync", "pay", "transfer", "submit", "修改", "更新", "同步", "提交", "支付")):
                    stages[2][2].append(f)
                else:
                    stages[0][2].append(f)

            nodes = []
            edges = []
            rules = []
            prev_id = "[*]"
            for sid, slabel, items in stages:
                if not items:
                    continue
                rep = items[0]
                where = f"{rep.get('file')}:{rep.get('line')}" if rep.get("file") and rep.get("line") else ""
                nodes.append({
                    "id": sid,
                    "label": f"{slabel} ({len(items)})",
                    "where": where,
                    "summary": "、".join((x.get("name") or x["id"]) for x in items[:5]),
                    "flowCount": len(items),
                })
                edges.append({"from": prev_id, "to": sid, "label": f"{len(items)} 条入口"})
                prev_id = sid
                for x in items[:2]:
                    if x.get("file") and x.get("line"):
                        crud = (x.get("data") or {}).get("crud") or {}
                        crud_txt = ("，涉及表 " + ", ".join(list(crud.keys())[:3])) if crud else ""
                        rules.append({
                            "text": f"{slabel}：{x.get('name') or x['id']}{crud_txt}",
                            "where": f"{x['file']}:{x['line']}",
                            "holds": True,
                        })
            if nodes:
                charts.append({
                    "id": f"graph-dom-{dkey}",
                    "title": f"{dom_title.get(dkey, dkey)} · 业务流转状态图",
                    "domain": dkey,
                    "sourceKind": "derived",
                    "sourceLabel": "图谱实测推导（可精准定位源码）",
                    "status": "图谱推导",
                    "commit": "",
                    "mermaid": "",
                    "nodes": nodes,
                    "edges": edges,
                    "rules": rules[:10],
                    "paths": [],
                })

    return {"repoPath": str(root), "charts": charts}


def _impact(repo_path: str, target: str, depth: int = 3) -> dict:
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")
    t = (target or "").strip()
    if not t:
        raise ValueError("请输入要分析变更影响的文件路径、符号、表名或接口关键词")
    from tmh import query as tmh_query
    from tmh.graph import Graph, graph_path

    raw_impact = {}
    try:
        raw_impact = tmh_query.impact(root, t, depth=max(1, min(depth or 3, 5)), limit=80) or {}
    except Exception:
        raw_impact = {}

    affected_flows = []
    affected_domains: dict[str, int] = {}
    affected_tables = []
    gp = graph_path(root)
    if gp.exists():
        g = Graph(root)
        tl = t.lower()
        for f in g.nodes("flow"):
            dd = f.get("data") or {}
            crud = dd.get("crud") or {}
            hay = " ".join([
                str(f.get("name") or ""),
                str(f.get("file") or ""),
                str(dd.get("trigger") or ""),
                str(dd.get("handler") or ""),
                " ".join(crud.keys()),
            ]).lower()
            if tl in hay:
                dom = f.get("domain") or "（未划分）"
                affected_domains[dom] = affected_domains.get(dom, 0) + 1
                affected_flows.append({
                    "id": f["id"],
                    "name": f.get("name") or f["id"],
                    "domain": dom,
                    "trigger": dd.get("trigger") or "",
                    "file": f.get("file") or "",
                    "line": f.get("line") or 0,
                    "tables": list(crud.keys()),
                })
        for tbl in g.nodes("tbl"):
            tname = str(tbl.get("name") or "")
            if tl in tname.lower() or tl in str(tbl.get("file") or "").lower():
                affected_tables.append({
                    "name": tname,
                    "file": tbl.get("file") or "",
                    "line": tbl.get("line") or 0,
                    "users": len(g.inn(tbl["id"], "uses")),
                })

    return {
        "repoPath": str(root),
        "target": t,
        "depth": depth,
        "codeImpact": raw_impact,
        "affectedDomains": [
            {"domain": k, "flowCount": v}
            for k, v in sorted(affected_domains.items(), key=lambda kv: -kv[1])
        ],
        "affectedFlows": affected_flows[:60],
        "affectedTables": affected_tables[:30],
    }


def _chain(repo_path: str, query: str, flows: int = 6) -> dict:
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")
    q = (query or "").strip()
    if not q:
        raise ValueError("请输入业务问题或需求描述")
    from tmh import kbqa
    c = kbqa.chain(root, q, budget=24000, flows=max(1, min(flows or 6, 12)))
    text = kbqa.chain_text(root, c)
    return {
        "repoPath": str(root),
        "query": q,
        "chain": c,
        "markdown": text,
    }


def _questions(repo_path: str) -> dict:
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")
    from tmh import kbqa
    rows = kbqa.questions(root)
    return {"repoPath": str(root), "questions": rows}


def _ask_question(repo_path: str, question: str, pages: list[str], who: str) -> dict:
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")
    q = (question or "").strip()
    if not q:
        raise ValueError("问题内容不能为空")
    from tmh import kbqa
    num = kbqa.ask(root, q, [p for p in (pages or []) if p], who=who or "", by=who or "user")
    return {"ok": True, "id": num, "questions": kbqa.questions(root)}


def _answer_question(repo_path: str, num: int, text: str, by: str) -> dict:
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")
    if num <= 0 or not (text or "").strip():
        raise ValueError("请提供有效的问题编号与回答内容")
    from tmh import kbqa
    res = kbqa.answer(root, num, text.strip(), by or "user")
    return {"ok": True, "result": res, "questions": kbqa.questions(root)}


def _confirm_page(repo_path: str, page_id: str, by: str) -> dict:
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")
    pid = (page_id or "").strip()
    if not pid:
        raise ValueError("pageId 不能为空")
    from tmh import kb
    reg = kb.load_registry(root)
    entry = reg.get(pid)
    if not isinstance(entry, dict):
        raise ValueError(f"知识页注册表中未找到: {pid}")
    entry["status"] = "已确认"
    entry["confirmedBy"] = by or "user"
    entry["confirmedAt"] = time.strftime("%Y-%m-%d %H:%M", time.localtime())
    reg[pid] = entry
    kb.save_registry(root, reg, _atomic=True)
    return {"ok": True, "pageId": pid, "status": "已确认", "entry": entry}


def main() -> int:
    parser = argparse.ArgumentParser(description="Webuddy KB & Code Graph Bridge")
    sub = parser.add_subparsers(dest="cmd", required=True)

    p_scan = sub.add_parser("scan")
    p_scan.add_argument("--repo", required=True)

    p_ov = sub.add_parser("overview")
    p_ov.add_argument("--repo", default="")

    p_search = sub.add_parser("search")
    p_search.add_argument("--repo", default="")
    p_search.add_argument("--query", required=True)
    p_search.add_argument("--limit", type=int, default=30)

    p_read = sub.add_parser("read")
    p_read.add_argument("--repo", default="")
    p_read.add_argument("--path", required=True)
    p_read.add_argument("--start", type=int, default=0)
    p_read.add_argument("--end", type=int, default=0)

    p_ev = sub.add_parser("verify-evidence")
    p_ev.add_argument("--repo", required=True)
    p_ev.add_argument("--ref", required=True)

    p_sc = sub.add_parser("statecharts")
    p_sc.add_argument("--repo", default="")

    p_imp = sub.add_parser("impact")
    p_imp.add_argument("--repo", required=True)
    p_imp.add_argument("--target", required=True)
    p_imp.add_argument("--depth", type=int, default=3)

    p_ch = sub.add_parser("chain")
    p_ch.add_argument("--repo", required=True)
    p_ch.add_argument("--query", required=True)
    p_ch.add_argument("--flows", type=int, default=6)

    p_qs = sub.add_parser("questions")
    p_qs.add_argument("--repo", required=True)

    p_ask = sub.add_parser("ask")
    p_ask.add_argument("--repo", required=True)
    p_ask.add_argument("--question", required=True)
    p_ask.add_argument("--pages", default="")
    p_ask.add_argument("--who", default="user")

    p_ans = sub.add_parser("answer")
    p_ans.add_argument("--repo", required=True)
    p_ans.add_argument("--num", type=int, required=True)
    p_ans.add_argument("--text", required=True)
    p_ans.add_argument("--by", default="user")

    p_cf = sub.add_parser("confirm-page")
    p_cf.add_argument("--repo", required=True)
    p_cf.add_argument("--page-id", required=True)
    p_cf.add_argument("--by", default="user")

    args = parser.parse_args()
    try:
        if args.cmd == "scan":
            out = _scan(args.repo)
        elif args.cmd == "overview":
            out = _overview(args.repo or None)
        elif args.cmd == "search":
            out = _search(args.repo or None, args.query, args.limit)
        elif args.cmd == "read":
            out = _read(args.repo or None, args.path, args.start, args.end)
        elif args.cmd == "verify-evidence":
            out = _verify_evidence(args.repo, args.ref)
        elif args.cmd == "statecharts":
            out = _statecharts(args.repo or None)
        elif args.cmd == "impact":
            out = _impact(args.repo, args.target, args.depth)
        elif args.cmd == "chain":
            out = _chain(args.repo, args.query, args.flows)
        elif args.cmd == "questions":
            out = _questions(args.repo)
        elif args.cmd == "ask":
            pgs = [x.strip() for x in (args.pages or "").split(",") if x.strip()]
            out = _ask_question(args.repo, args.question, pgs, args.who)
        elif args.cmd == "answer":
            out = _answer_question(args.repo, args.num, args.text, args.by)
        elif args.cmd == "confirm-page":
            out = _confirm_page(args.repo, args.page_id, args.by)
        else:
            raise ValueError(f"unknown command: {args.cmd}")
        sys.stdout.write(json.dumps(out, ensure_ascii=False))
        return 0
    except Exception as exc:
        sys.stderr.write(json.dumps({"error": str(exc)}, ensure_ascii=False))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
