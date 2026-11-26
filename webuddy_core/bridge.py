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
        open_q_count = sum(1 for q in qs if bool(q.get("open", False)))
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

    source_files = []
    readme_excerpt = ""
    skip_dirs = {".git", ".tmh", "node_modules", "__pycache__", "dist", "build", "target", ".venv", "venv"}
    try:
        for dirpath, dirnames, filenames in os.walk(root):
            dirnames[:] = [d for d in sorted(dirnames) if d not in skip_dirs and not d.startswith(".")]
            for fn in sorted(filenames):
                if fn.startswith("."):
                    continue
                fp = Path(dirpath) / fn
                rel = fp.relative_to(root).as_posix()
                source_files.append(rel)
                if not readme_excerpt and fn.lower() in ("readme.md", "readme.txt", "readme"):
                    try:
                        readme_excerpt = fp.read_text(encoding="utf-8", errors="replace")[:1500]
                    except Exception:
                        pass
                if len(source_files) >= 200:
                    break
            if len(source_files) >= 200:
                break
    except Exception:
        pass

    return {
        "built": True,
        "repoPath": str(root),
        "builtAt": time.strftime("%Y-%m-%d %H:%M", time.localtime(gp.stat().st_mtime)),
        "flowCount": len(flows),
        "tableCount": len(tables),
        "pageCount": len(pages),
        "sourceFileCount": len(source_files),
        "sourceFiles": source_files[:80],
        "readmeExcerpt": readme_excerpt,
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


def _read_build_head(root: Path) -> str:
    p = root / ".tmh" / "cache" / "build_head.txt"
    try:
        if p.exists():
            return p.read_text(encoding="utf-8").strip()
    except Exception:
        pass
    return ""


def _write_build_head(root: Path, head: str) -> None:
    if not head:
        return
    try:
        p = root / ".tmh" / "cache" / "build_head.txt"
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(head.strip() + "\n", encoding="utf-8")
    except Exception:
        pass


def _scan(repo_path: str) -> dict:
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")
    from tmh import kb
    from tmh.graph import build
    from tmh.polyglot_structure import build_polyglot_structure

    stats = build(root, use_cache=True)
    if kb.needs_structure(root):
        try:
            poly = build_polyglot_structure(root)
            if poly.get("applied"):
                stats["polyglot"] = poly
        except Exception:
            pass
    try:
        kb_stats = kb.build(root)
        stats["kbBuild"] = kb_stats
    except Exception:
        pass
    _write_build_head(root, _kb_git_head(root))
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

    from tmh.graph import Graph, graph_path
    gp = graph_path(root)
    dom_title: dict[str, str] = {}
    if gp.exists():
        g_pre = Graph(root)
        for dn in g_pre.nodes("dom"):
            t_lbl = dn.get("label") or dn.get("name") or ""
            if dn.get("name"):
                dom_title[dn["name"]] = t_lbl
            if dn["id"].startswith("dom:"):
                dom_title[dn["id"][4:]] = t_lbl

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
            dom_key = str(data.get("domain") or fp.stem)
            raw_title = str(data.get("title") or fp.stem)
            cn_title = dom_title.get(dom_key) or dom_title.get(fp.stem) or ""
            display_title = f"{cn_title} ({raw_title})" if cn_title and cn_title != raw_title else raw_title
            charts.append({
                "id": fp.stem,
                "title": display_title,
                "domain": dom_key,
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


_ZH_STOP_PHRASES = (
    "请问", "帮我", "看一下", "查看", "怎么", "如何", "为什么", "什么", "哪里", "在哪",
    "一下", "这个", "那个", "哪些", "有没有", "是否", "可以", "应该", "需要", "逻辑",
)

_ZH_EN_SYNONYMS: dict[str, list[str]] = {
    "发票": ["invoice", "invoices", "receipt", "ocr", "ofd", "pdf"],
    "报销": ["claim", "claims", "reimburse", "expense", "me"],
    "提交": ["submit", "post", "create", "claim", "claims", "upload"],
    "薪酬": ["payroll", "salary", "pay"],
    "工资": ["payroll", "salary", "pay"],
    "发薪": ["payroll", "salary"],
    "核算": ["payroll", "calc", "calculate", "month"],
    "审批": ["approve", "approval", "review", "audit"],
    "审核": ["approve", "review", "audit", "verify"],
    "核验": ["verify", "validate", "check", "invoice"],
    "校验": ["verify", "validate", "check"],
    "员工": ["employee", "employees", "staff", "user"],
    "登录": ["login", "auth", "oauth", "session"],
    "飞书": ["feishu", "lark"],
    "导出": ["export", "xlsx", "excel"],
    "上传": ["upload", "multipart", "file", "files"],
    "替票": ["substitute", "ticket", "invoice"],
    "外派": ["dispatch", "site", "allowance"],
    "项目": ["project", "projects"],
    "成本": ["cost", "token", "effort"],
    "精力": ["effort", "allocation"],
    "考勤": ["attendance", "leave", "late"],
    "请假": ["leave", "attendance"],
    "考试": ["exam", "score"],
    "提成": ["commission", "bonus"],
    "奖金": ["bonus", "commission"],
    "通知": ["notify", "notification", "message"],
    "管理员": ["admin", "manager"],
    "后台": ["admin", "dashboard"],
    "权限": ["role", "auth", "rbac", "admin"],
    "租户": ["tenant", "workspace"],
    "订单": ["order", "orders"],
    "支付": ["pay", "payment"],
    "删除": ["delete", "remove"],
    "修改": ["update", "edit", "put", "patch"],
    "查询": ["get", "list", "query", "search"],
    "配置": ["config", "setting", "env"],
}


def _extract_query_terms(query: str) -> list[str]:
    raw = (query or "").strip()
    if not raw:
        return []
    terms: list[str] = []
    seen: set[str] = set()

    def add(tok: str) -> None:
        t = tok.strip().lower()
        if len(t) >= 2 and t not in seen:
            seen.add(t)
            terms.append(t)

    # 1. ASCII tokens / paths / identifiers
    for m in re.findall(r"[A-Za-z0-9_./-]{2,}", raw):
        add(m)
        for part in re.split(r"[_./-]+", m):
            if len(part) >= 2:
                add(part)

    # 2. Chinese segments after stripping common question fillers
    cleaned = raw
    for sp in _ZH_STOP_PHRASES:
        cleaned = cleaned.replace(sp, " ")
    for seg in re.findall(r"[\u4e00-\u9fff]+", cleaned):
        seg = seg.strip("的了呢吗吧呀啊")
        if len(seg) >= 2:
            add(seg)
            # Extract 2-char keywords for compound Chinese queries (e.g. "提交发票" -> "提交", "发票")
            if len(seg) > 2:
                for i in range(len(seg) - 1):
                    bg = seg[i : i + 2]
                    if bg not in _ZH_STOP_PHRASES:
                        add(bg)

    # 3. Expand matched Chinese domain terms to English code equivalents
    for zh, en_list in _ZH_EN_SYNONYMS.items():
        if zh in raw:
            add(zh)
            for en in en_list:
                add(en)

    return terms[:32]


def _impact(repo_path: str, target: str, depth: int = 3) -> dict:
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")
    t = (target or "").strip()
    if not t:
        raise ValueError("请输入要分析变更影响的文件路径、符号、表名或接口关键词")
    from tmh import query as tmh_query
    from tmh.graph import Graph, graph_path

    terms = _extract_query_terms(t)
    raw_impact = {}
    try:
        raw_impact = tmh_query.impact(root, t, depth=max(1, min(depth or 3, 5)), limit=80) or {}
    except Exception:
        raw_impact = {}

    # Collect caller file paths from raw_impact so we can link them back to graph flows/domains/tables
    caller_files: set[str] = set()
    for c_item in (raw_impact.get("callers") or []):
        cf = str(c_item.get("file") or "").strip().lower()
        if cf:
            caller_files.add(cf)

    affected_flows = []
    affected_domains: dict[str, int] = {}
    affected_tables_map: dict[str, dict] = {}
    gp = graph_path(root)
    if gp.exists():
        g = Graph(root)
        tl = t.lower()
        tbl_index = {str(tb.get("name") or "").lower(): tb for tb in g.nodes("tbl")}
        for f in g.nodes("flow"):
            dd = f.get("data") or {}
            crud = dd.get("crud") or {}
            ffile = str(f.get("file") or "").lower()
            hay = " ".join([
                str(f.get("name") or ""),
                ffile,
                str(f.get("domain") or ""),
                str(dd.get("trigger") or ""),
                str(dd.get("handler") or ""),
                " ".join(crud.keys()),
            ]).lower()
            matched = (tl in hay) or (ffile in caller_files) or any(tok in hay for tok in terms)
            if matched:
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
                for tk in crud.keys():
                    tkl = tk.lower()
                    if tkl not in affected_tables_map:
                        tb_node = tbl_index.get(tkl)
                        affected_tables_map[tkl] = {
                            "name": tk,
                            "file": (tb_node.get("file") or "") if tb_node else "",
                            "line": (tb_node.get("line") or 0) if tb_node else 0,
                            "users": len(g.inn(tb_node["id"], "uses")) if tb_node else 1,
                        }
        for tbl in g.nodes("tbl"):
            tname = str(tbl.get("name") or "")
            tfile = str(tbl.get("file") or "").lower()
            thay = (tname + " " + tfile).lower()
            if tl in thay or tfile in caller_files or any(tok in thay for tok in terms):
                tkl = tname.lower()
                if tkl not in affected_tables_map:
                    affected_tables_map[tkl] = {
                        "name": tname,
                        "file": tbl.get("file") or "",
                        "line": tbl.get("line") or 0,
                        "users": len(g.inn(tbl["id"], "uses")),
                    }

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
        "affectedTables": list(affected_tables_map.values())[:30],
    }


def _chain(repo_path: str, query: str, flows: int = 6) -> dict:
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")
    q = (query or "").strip()
    if not q:
        raise ValueError("请输入业务问题或需求描述")
    from tmh import kbqa
    from tmh.graph import Graph, graph_path

    max_flows = max(1, min(flows or 6, 12))
    c = kbqa.chain(root, q, budget=24000, flows=max_flows)
    text = kbqa.chain_text(root, c)

    # Bilingual semantic fallback when tmh exact-string chain finds no pages/flows
    has_hits = bool(
        (isinstance(c, dict) and (c.get("pages") or c.get("flows") or c.get("hits")))
        and "没有找到相关页面" not in (text or "")
    )
    if not has_hits:
        terms = _extract_query_terms(q)
        scored_pages: list[tuple[int, str, str]] = []
        modules_dir = root / ".tmh" / "knowledge" / "modules"
        if modules_dir.is_dir():
            for md_file in sorted(modules_dir.glob("*.md")):
                try:
                    content = md_file.read_text(encoding="utf-8", errors="ignore")
                except Exception:
                    continue
                low = (md_file.name + "\n" + content).lower()
                score = sum(3 if tok in md_file.name.lower() else 1 for tok in terms if tok in low)
                if score > 0:
                    rel = md_file.relative_to(root).as_posix()
                    scored_pages.append((score, rel, content[:1800].strip()))
        scored_pages.sort(key=lambda x: -x[0])

        scored_flows: list[tuple[int, dict]] = []
        matched_files: list[str] = []
        gp = graph_path(root)
        if gp.exists():
            g = Graph(root)
            for f in g.nodes("flow"):
                dd = f.get("data") or {}
                crud = dd.get("crud") or {}
                hay = " ".join([
                    str(f.get("name") or ""),
                    str(f.get("file") or ""),
                    str(f.get("domain") or ""),
                    str(dd.get("trigger") or ""),
                    str(dd.get("handler") or ""),
                    " ".join(crud.keys()),
                ]).lower()
                score = sum(2 if tok in str(f.get("name") or "").lower() or tok in str(dd.get("trigger") or "").lower() else 1 for tok in terms if tok in hay)
                if score > 0:
                    scored_flows.append((score, {
                        "name": f.get("name") or f["id"],
                        "domain": f.get("domain") or "未划分",
                        "trigger": dd.get("trigger") or "",
                        "file": f.get("file") or "",
                        "line": f.get("line") or 0,
                        "tables": list(crud.keys()),
                    }))
            scored_flows.sort(key=lambda x: -x[0])

        sections: list[str] = [f"## 与「{q}」相关的业务链路与源码证据", f"（已自动扩展关键词：{', '.join(terms[:10])}）"]
        if scored_flows:
            sections.append("### 命中的业务流程与接口")
            for _, fl in scored_flows[:max_flows]:
                loc = f"{fl['file']}:{fl['line']}" if fl["file"] else ""
                tbls = f" | 数据表: {', '.join(fl['tables'])}" if fl["tables"] else ""
                sections.append(f"- **{fl['name']}**（域：{fl['domain']}） `{loc}`{tbls}")
                if fl["file"] and fl["file"] not in matched_files:
                    matched_files.append(fl["file"])

        if scored_pages:
            sections.append("### 命中的知识库模块文档")
            for _, rel, snippet in scored_pages[:3]:
                sections.append(f"#### {rel}\n{snippet}")

        # Extract concise source evidence lines from top matched files
        code_hits: list[str] = []
        for mf in matched_files[:4]:
            fp = root / mf
            if not fp.is_file():
                continue
            try:
                lines = fp.read_text(encoding="utf-8", errors="ignore").splitlines()
            except Exception:
                continue
            for idx, ln in enumerate(lines, 1):
                ll = ln.lower()
                if any(tok in ll for tok in terms[:12]):
                    code_hits.append(f"- `{mf}:{idx}` `{ln.strip()[:140]}`")
                    if len(code_hits) >= 12:
                        break
            if len(code_hits) >= 12:
                break
        if code_hits:
            sections.append("### 命中源码行号证据")
            sections.extend(code_hits)

        if scored_flows or scored_pages or code_hits:
            text = "\n\n".join(sections)
            c = {
                "query": q,
                "terms": terms[:12],
                "flows": [fl for _, fl in scored_flows[:max_flows]],
                "pages": [rel for _, rel, _ in scored_pages[:4]],
            }

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


from contextlib import contextmanager


@contextmanager
def _repo_write_lock(root: Path):
    from tmh import kb
    lock_dir = root / ".tmh" / "cache"
    lock_dir.mkdir(parents=True, exist_ok=True)
    with kb._Lock(lock_dir / "kb.lock"):
        yield


def _ask_question(repo_path: str, question: str, pages: list[str], who: str) -> dict:
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")
    q = (question or "").strip()
    if not q:
        raise ValueError("问题内容不能为空")
    from tmh import kbqa
    with _repo_write_lock(root):
        num = kbqa.ask(root, q, [p for p in (pages or []) if p], who=who or "", by=who or "user")
        qs = kbqa.questions(root)
    return {"ok": True, "id": num, "questions": qs}


def _answer_question(repo_path: str, num: int, text: str, by: str) -> dict:
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")
    if num <= 0 or not (text or "").strip():
        raise ValueError("请提供有效的问题编号与回答内容")
    from tmh import kbqa
    with _repo_write_lock(root):
        res = kbqa.answer(root, num, text.strip(), by or "user")
        qs = kbqa.questions(root)
    return {"ok": True, "result": res, "questions": qs}


def _confirm_page(repo_path: str, page_id: str, by: str) -> dict:
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")
    pid = (page_id or "").strip()
    if not pid:
        raise ValueError("pageId 不能为空")
    from tmh import kb
    with _repo_write_lock(root):
        if hasattr(kb, "confirm"):
            kb.confirm(root, pid, who=by or "user")
            reg = kb.load_registry(root)
            entry = reg.get(pid) or {"status": "已确认"}
            return {"ok": True, "pageId": pid, "status": entry.get("status", "已确认"), "entry": entry}
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


def _kb_coverage_summary(root: Path, reg: dict) -> dict:
    from tmh import kb
    cov = kb.coverage(reg)
    tot_map = cov.get("total") or {}
    confirmed = int(tot_map.get("已确认", 0))
    draft = int(tot_map.get("待确认", 0))
    pending = int(tot_map.get("未分析", 0))
    stale = int(tot_map.get("过期", 0))
    facts_only = int(tot_map.get("仅事实", 0))
    total = sum(int(v) for v in tot_map.values())
    analyzed = confirmed + draft
    return {
        "total": total,
        "analyzed": analyzed,
        "confirmed": confirmed,
        "draft": draft,
        "pending": pending,
        "stale": stale,
        "factsOnly": facts_only,
        "raw": tot_map,
    }


def _kb_git_head(root: Path) -> str:
    try:
        import subprocess
        out = subprocess.check_output(
            ["git", "rev-parse", "--short=10", "HEAD"],
            cwd=str(root),
            stderr=subprocess.DEVNULL,
            timeout=5,
        )
        return out.decode("utf-8", errors="replace").strip()
    except Exception:
        return ""


def _bootstrap_generic_kb_pages(root: Path, reg: dict) -> dict:
    """When tmh static parser finds 0 pages (e.g. Node.js/TypeScript/PHP/script repos),
    auto-seed module knowledge pages grouped by source directory so KB update & browsing work."""
    if reg:
        return reg
    from tmh import kb
    skip_dirs = {".git", ".tmh", "node_modules", "__pycache__", "dist", "build", "target", ".venv", "venv"}
    code_exts = {".js", ".ts", ".jsx", ".tsx", ".mjs", ".cjs", ".py", ".go", ".java", ".kt", ".rb", ".php", ".cs", ".rs", ".sql", ".sh", ".yml", ".yaml", ".md"}
    groups: dict[str, list[tuple[str, int]]] = {}
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in sorted(dirnames) if d not in skip_dirs and not d.startswith(".")]
        for fn in sorted(filenames):
            if fn.startswith(".") or fn == "package-lock.json":
                continue
            fp = Path(dirpath) / fn
            if fp.suffix.lower() not in code_exts:
                continue
            rel = fp.relative_to(root).as_posix()
            parts = rel.split("/")
            if len(parts) >= 3:
                grp = f"{parts[0]}/{parts[1]}"
            elif len(parts) == 2:
                grp = parts[0]
            else:
                grp = "root"
            try:
                lines = len(fp.read_text(encoding="utf-8", errors="replace").splitlines())
            except Exception:
                lines = 1
            groups.setdefault(grp, []).append((rel, max(1, lines)))
    if not groups:
        return reg
    with _repo_write_lock(root):
        reg = kb.load_registry(root)
        if reg:
            return reg
        for grp, flist in sorted(groups.items())[:24]:
            slug = re.sub(r"[^a-zA-Z0-9_-]+", "-", grp).strip("-") or "root"
            pid = f"mod:{slug}"
            p = kb.page_path(root, pid)
            facts_lines = [f"- `{rel}:1-{min(lines, 120)}`（共 {lines} 行）" for rel, lines in flist[:25]]
            facts_md = "## 模块源码清单\n" + "\n".join(facts_lines)
            meta = {"id": pid, "title": f"模块：{grp}", "summary": "", "status": "未分析"}
            body = kb.skeleton("mod")
            p.parent.mkdir(parents=True, exist_ok=True)
            kb._atomic_text(p, kb.render_page(pid, meta, facts_md, body))
            reg[pid] = {
                "scope": "自动模块",
                "status": "未分析",
                "title": f"模块：{grp}",
                "summary": "",
                "files": [rel for rel, _ in flist[:25]],
            }
        kb.save_registry(root, reg, _atomic=True)
    return reg


def _kb_status(repo_path: str) -> dict:
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")
    from tmh import kb
    reg = _bootstrap_generic_kb_pages(root, kb.load_registry(root))
    cov = _kb_coverage_summary(root, reg)
    recent = []
    for pid, meta in (reg or {}).items():
        if not isinstance(meta, dict) or meta.get("orphan"):
            continue
        upd = str(meta.get("updated") or "").strip()
        if not upd:
            continue
        try:
            rel_p = kb.page_path(root, pid).relative_to(root).as_posix()
        except Exception:
            rel_p = ""
        recent.append({
            "pageId": pid,
            "title": str(meta.get("title") or pid),
            "summary": str(meta.get("summary") or "")[:140],
            "status": str(meta.get("status") or ""),
            "commit": str(meta.get("commit") or ""),
            "updated": upd,
            "by": str(meta.get("by") or ""),
            "relPath": rel_p,
        })
    recent.sort(key=lambda x: x["updated"], reverse=True)
    git_head = _kb_git_head(root)
    build_head = _read_build_head(root)
    return {
        "ok": True,
        "repoPath": str(root),
        "gitHead": git_head,
        "buildHead": build_head,
        "headDrifted": bool(git_head and build_head != git_head),
        "coverage": cov,
        "recentUpdated": recent[:8],
    }


def _kb_next(repo_path: str, limit: int, query: str, who: str) -> dict:
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")
    limit = max(1, min(int(limit or 3), 5))
    from tmh import kb
    from tmh.graph import build as graph_build, graph_path
    from tmh.polyglot_structure import build_polyglot_structure

    git_head = _kb_git_head(root)
    build_head = _read_build_head(root)
    if not graph_path(root).exists() or (git_head and build_head != git_head):
        try:
            graph_build(root, use_cache=True)
            if kb.needs_structure(root):
                build_polyglot_structure(root)
            kb.build(root)
            _write_build_head(root, git_head)
        except Exception:
            pass
    reg = _bootstrap_generic_kb_pages(root, kb.load_registry(root))
    cov = _kb_coverage_summary(root, reg)
    queue = [pid for pid in kb.pending(root, reg) if pid != "structure"]
    if not queue and reg:
        queue = [pid for pid, r in reg.items() if isinstance(r, dict) and r.get("status") == "未分析"]
    q_low = (query or "").strip().lower()
    picked_pids: list[str] = []
    if q_low:
        tokens = [t for t in re.split(r"[\s,，、/]+", q_low) if t]
        for pid in queue:
            if pid == "structure":
                continue
            meta = reg.get(pid) if isinstance(reg.get(pid), dict) else {}
            hay = " ".join([
                pid,
                str(meta.get("title") or ""),
                str(meta.get("summary") or ""),
            ]).lower()
            if any(tok in hay for tok in tokens):
                picked_pids.append(pid)
                if len(picked_pids) >= limit:
                    break
    if len(picked_pids) < limit:
        for pid in queue:
            if pid == "structure" or pid in picked_pids:
                continue
            picked_pids.append(pid)
            if len(picked_pids) >= limit:
                break

    jobs = []
    for pid in picked_pids:
        kind = kb.page_kind(pid)
        p = kb.page_path(root, pid)
        rel_p = p.relative_to(root).as_posix() if p.is_relative_to(root) else str(p)
        meta = reg.get(pid) if isinstance(reg.get(pid), dict) else {}
        pack_txt = kb.pack(root, pid)
        if len(pack_txt.strip()) < 300 and isinstance(meta.get("files"), list):
            parts = [pack_txt.strip()] if pack_txt.strip() else []
            for rel_f in meta.get("files")[:6]:
                fp = root / rel_f
                if not fp.is_file():
                    continue
                try:
                    lines = fp.read_text(encoding="utf-8", errors="replace").splitlines()[:90]
                    numbered = "\n".join(f"{i+1}: {ln}" for i, ln in enumerate(lines))
                    parts.append(f"### 源码文件 `{rel_f}` (1-{len(lines)})\n```\n{numbered}\n```")
                except Exception:
                    pass
            pack_txt = "\n\n".join(parts)
        if len(pack_txt) > 10000:
            pack_txt = pack_txt[:10000] + "\n...(后续上下文已截断)"
        jobs.append({
            "pageId": pid,
            "kind": kind,
            "relPath": rel_p,
            "currentTitle": str(meta.get("title") or pid),
            "requiredSections": list(kb.SECTIONS.get(kind, [])),
            "instructions": kb.instructions(root, pid),
            "packText": pack_txt,
        })
    return {
        "ok": True,
        "repoPath": str(root),
        "gitHead": _kb_git_head(root),
        "coverage": cov,
        "remaining": len(queue),
        "jobs": jobs,
    }


def _extract_human_answer_section(md_body: str) -> str:
    if not md_body or "## 业务方答复" not in md_body:
        return ""
    idx = md_body.find("## 业务方答复")
    rest = md_body[idx:]
    m = re.search(r"\n##\s+(?!业务方答复)", rest)
    if m:
        return rest[:m.start()].strip()
    return rest.strip()


def _kb_commit(repo_path: str, page_id: str, title: str, summary: str, body: str, who: str) -> dict:
    root = Path(repo_path).expanduser().resolve()
    if not root.is_dir():
        raise ValueError(f"仓库目录不存在: {repo_path}")
    pid = (page_id or "").strip()
    if not pid:
        raise ValueError("pageId 不能为空")
    from tmh import kb, kb_structure
    p = kb.page_path(root, pid)
    if not p.exists():
        raise ValueError(f"知识页文件不存在: {p}")

    with _repo_write_lock(root):
        reg_before = kb.load_registry(root)
        entry_before = reg_before.get(pid) if isinstance(reg_before.get(pid), dict) else {}
        if entry_before.get("status") == "已确认" and not entry_before.get("stale") and not entry_before.get("revise"):
            return {
                "ok": False,
                "pageId": pid,
                "errors": ["该知识页已由人工确认且代码未过期，跳过覆盖"],
            }

        existing = p.read_text(encoding="utf-8")
        pg = kb.parse_page(existing)
        meta = dict(pg.get("meta") or {})
        facts = pg.get("facts") or ""
        old_body = pg.get("body") or ""
        preserved_qa = _extract_human_answer_section(old_body)

        clean_body = (body or "").strip()
        if preserved_qa and "## 业务方答复" not in clean_body:
            clean_body = clean_body + "\n\n" + preserved_qa

        clean_title = (title or "").strip()
        clean_summary = (summary or "").strip()
        if clean_title:
            meta["title"] = clean_title
        if clean_summary:
            meta["summary"] = clean_summary[:115]
        candidate = kb.render_page(pid, meta, facts, clean_body)
        errs, info = kb.validate_content(root, pid, candidate)
        if errs:
            return {
                "ok": False,
                "pageId": pid,
                "errors": errs,
            }

        kb._atomic_text(p, candidate)
        ok, done_errs = kb.done(root, pid, who=who or "webuddy-ai")
        if not ok:
            kb._atomic_text(p, existing)
            return {
                "ok": False,
                "pageId": pid,
                "errors": done_errs,
            }
        reg = kb.load_registry(root)
        entry = reg.get(pid) if isinstance(reg.get(pid), dict) else {}
        cov = _kb_coverage_summary(root, reg)

    refs = []
    seen_refs = set()
    for m in kb_structure.REFERENCE.finditer(clean_body):
        ref_str = m.group()
        if ref_str not in seen_refs:
            seen_refs.add(ref_str)
            refs.append(ref_str)
            if len(refs) >= 6:
                break

    rel_p = p.relative_to(root).as_posix() if p.is_relative_to(root) else str(p)
    return {
        "ok": True,
        "pageId": pid,
        "title": str(entry.get("title") or info.get("title") or clean_title or pid),
        "summary": str(entry.get("summary") or info.get("summary") or clean_summary),
        "status": str(entry.get("status") or "待确认"),
        "commit": str(entry.get("commit") or _kb_git_head(root)),
        "updated": str(entry.get("updated") or time.strftime("%Y-%m-%d %H:%M")),
        "by": str(entry.get("by") or who or "webuddy-ai"),
        "relPath": rel_p,
        "evidenceRefs": refs,
        "coverage": cov,
    }


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

    p_kbs = sub.add_parser("kb-status")
    p_kbs.add_argument("--repo", required=True)

    p_kbn = sub.add_parser("kb-next")
    p_kbn.add_argument("--repo", required=True)
    p_kbn.add_argument("--limit", type=int, default=3)
    p_kbn.add_argument("--query", default="")
    p_kbn.add_argument("--who", default="webuddy-ai")

    p_kbc = sub.add_parser("kb-commit")
    p_kbc.add_argument("--repo", required=True)
    p_kbc.add_argument("--page-id", required=True)
    p_kbc.add_argument("--title", required=True)
    p_kbc.add_argument("--summary", required=True)
    p_kbc.add_argument("--who", default="webuddy-ai")

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
        elif args.cmd == "kb-status":
            out = _kb_status(args.repo)
        elif args.cmd == "kb-next":
            out = _kb_next(args.repo, args.limit, args.query, args.who)
        elif args.cmd == "kb-commit":
            body_text = sys.stdin.read()
            out = _kb_commit(args.repo, args.page_id, args.title, args.summary, body_text, args.who)
        else:
            raise ValueError(f"unknown command: {args.cmd}")
        sys.stdout.write(json.dumps(out, ensure_ascii=False))
        return 0
    except Exception as exc:
        sys.stderr.write(json.dumps({"error": str(exc)}, ensure_ascii=False))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
