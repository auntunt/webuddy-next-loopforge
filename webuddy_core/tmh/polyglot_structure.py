"""Universal polyglot structure extractor for tmh code graphs.

When tmh's built-in Java/Go/Python static parser finds 0 flows/tables
(structure_needed=True), this module deterministically extracts verified
HTTP endpoints, scheduled jobs, message listeners, CLI entries, and database
tables/models across:
  - Node.js / TypeScript (Express, Koa, Fastify, Hono, NestJS, Next.js App/Pages API, Prisma, TypeORM)
  - C# / .NET (ASP.NET Core attributes, Minimal API, EF Core DbSet / [Table])
  - PHP (Laravel Route::*, Symfony #[Route], Eloquent $table / Schema::create)
  - Rust (Actix/Rocket #[get/post], Axum .route, Diesel/SeaORM tables)
  - Ruby (Rails routes.rb / Sinatra, ActiveRecord create_table)
  - Kotlin / Scala (Ktor, Spring/JAX-RS annotations, JPA @Table)
  - C / C++ (Crow, Drogon)
  - Universal SQL (CREATE TABLE across any .sql or source file)

Every extracted flow and table carries an exact repository-relative `file:line`
reference validated by `tmh.graph.build(root, proposal_text=...)`.
"""
from __future__ import annotations

import json
import os
import re
from pathlib import Path

SKIP_DIRS = {
    ".git", ".tmh", "node_modules", "__pycache__", "dist", "build", "target",
    ".venv", "venv", "vendor", "bin", "obj", ".next", ".nuxt", "coverage",
    "public", "static", "assets", "test", "tests", "__tests__", "spec",
}

CODE_EXTS = {
    ".js", ".ts", ".jsx", ".tsx", ".mjs", ".cjs",
    ".cs", ".php", ".rs", ".rb", ".kt", ".kts", ".scala",
    ".c", ".cc", ".cpp", ".h", ".hpp",
    ".sql", ".prisma",
}

# ---- Table / Schema Patterns ----
_SQL_CREATE_TABLE_RE = re.compile(
    r"""\bCREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?[`"'\[]?(?:[A-Za-z0-9_]+\.)?([A-Za-z_][A-Za-z0-9_]*)[`"'\]]?""",
    re.I,
)
_PRISMA_MODEL_RE = re.compile(r"""^\s*model\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{""")
_TYPEORM_ENTITY_RE = re.compile(r"""@Entity\s*\(\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]""")
_CSHARP_TABLE_RE = re.compile(r"""\[Table\s*\(\s*"([A-Za-z_][A-Za-z0-9_]*)"\s*\)\]""")
_CSHARP_DBSET_RE = re.compile(r"""\bDbSet<\s*([A-Za-z_][A-Za-z0-9_]*)\s*>\s+([A-Za-z_][A-Za-z0-9_]*)""")
_PHP_TABLE_RE = re.compile(r"""\$table\s*=\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]""")
_PHP_SCHEMA_CREATE_RE = re.compile(r"""Schema::create\s*\(\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]""")
_RUBY_CREATE_TABLE_RE = re.compile(r"""\bcreate_table\s+[:'"]([A-Za-z_][A-Za-z0-9_]*)['"]?""")
_RUST_SEAORM_TABLE_RE = re.compile(r'table_name\s*=\s*"([A-Za-z_][A-Za-z0-9_]*)"')
_RUST_DIESEL_TABLE_RE = re.compile(r'^\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(\s*[a-z_]+\s*\)\s*\{')
_JPA_TABLE_RE = re.compile(r'@Table\s*\(\s*name\s*=\s*"([A-Za-z_][A-Za-z0-9_]*)"')

# ---- Route / Entry Patterns ----
# Node.js / TS: Express, Koa, Fastify, Hono
_JS_ROUTE_RE = re.compile(
    r"""\b(?:app|router|fastify|server|hono|r)\.(get|post|put|delete|patch|all)\s*\(\s*['"`]([^'"`\n]+)['"`]""",
    re.I,
)
# Native Node.js http.createServer / URL routing:
# e.g. if (req.method === 'POST' && pathname === '/api/orders')
# e.g. if (method === 'POST' && pathname.match(/^\/api\/orders\/([^/]+)\/approve$/))
_JS_NATIVE_ROUTE_EQ_RE = re.compile(
    r"""\b(?:req\.)?method\s*={2,3}\s*['"](GET|POST|PUT|DELETE|PATCH)['"][^;\n]{0,80}?(?:pathname|url|path)\s*={2,3}\s*['"`]([^'"`\n]+)['"`]""",
    re.I,
)
_JS_NATIVE_ROUTE_MATCH_RE = re.compile(
    r"""\b(?:req\.)?method\s*={2,3}\s*['"](GET|POST|PUT|DELETE|PATCH)['"][^;\n]{0,80}?(?:pathname|url|path)\.match\(\s*/\^?([^/\n]+?)\$?/\s*\)""",
    re.I,
)
_JS_URL_MATCH_ONLY_RE = re.compile(
    r"""\b(?:pathname|url)\.(?:startsWith|endsWith)\(\s*['"`](/[^'"`\n]+)['"`]\s*\)""",
)
_JS_FUNC_RE = re.compile(
    r"""^\s*(?:export\s+)?(?:async\s+)?function\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*\(""",
)
# NestJS / Spring-like / Kotlin decorators
_NEST_CTRL_RE = re.compile(r"""@Controller\s*\(\s*['"]([^'"]*)['"]""")
_NEST_METHOD_RE = re.compile(r"""@(Get|Post|Put|Delete|Patch)\s*\(\s*(?:['"]([^'"]*)['"])?\s*\)""")
# Next.js App Router: export async function GET/POST/...
_NEXT_HANDLER_RE = re.compile(r"""\bexport\s+(?:async\s+)?function\s+(GET|POST|PUT|DELETE|PATCH)\b""")
# C# / .NET ASP.NET Core attributes & Minimal API
_CSHARP_ROUTE_PREFIX_RE = re.compile(r"""\[Route\s*\(\s*"([^"]+)"\s*\)\]""")
_CSHARP_HTTP_ATTR_RE = re.compile(r"""\[Http(Get|Post|Put|Delete|Patch)(?:\s*\(\s*"([^"]*)"\s*\))?\]""")
_CSHARP_MINIMAL_RE = re.compile(r'\bMap(Get|Post|Put|Delete|Patch)\s*\(\s*"([^"]+)"')
# PHP Laravel & Symfony
_PHP_LARAVEL_ROUTE_RE = re.compile(
    r"""Route::(get|post|put|delete|patch|any|resource)\s*\(\s*['"]([^'"]+)['"]""",
    re.I,
)
_PHP_SYMFONY_ROUTE_RE = re.compile(r"""#\[Route\s*\(\s*['"]([^'"]+)['"]""")
# Rust Actix / Rocket / Axum
_RUST_ATTR_ROUTE_RE = re.compile(r"""#\[(get|post|put|delete|patch)\s*\(\s*"([^"]+)"\s*\)\]""", re.I)
_RUST_AXUM_ROUTE_RE = re.compile(r"""\.route\s*\(\s*"([^"]+)"\s*,\s*(get|post|put|delete|patch)\s*\(""", re.I)
# Ruby Rails / Sinatra
_RUBY_ROUTE_RE = re.compile(r"""^\s*(get|post|put|delete|patch|resources)\s+['":]([A-Za-z0-9_/:.-]+)['"]?""")
# Kotlin Ktor / Spring
_KTOR_ROUTE_RE = re.compile(r"""^\s*(get|post|put|delete|patch)\s*\(\s*"([^"]+)"\s*\)""")
_JVM_MAPPING_RE = re.compile(r'@(Get|Post|Put|Delete|Patch|Request)Mapping\s*\([^)]*"([^"]+)"')
# C / C++ Crow / Drogon
_CPP_CROW_RE = re.compile(r'CROW_ROUTE\s*\([^,]+,\s*"([^"]+)"\s*\)')
_CPP_DROGON_RE = re.compile(r'ADD_METHOD_TO\s*\([^,]+,\s*"([^"]+)"')
# Scheduled / Cron jobs (cross-language)
_CRON_JOB_RE = re.compile(
    r"""(?:cron\.schedule\s*\(\s*['"]([^'"]+)['"]|@Cron\s*\(\s*['"]([^'"]+)['"]|RecurringJob\.AddOrUpdate\s*\(\s*"([^"]+)")""",
    re.I,
)
# Message listeners (cross-language)
_MQ_LISTENER_RE = re.compile(
    r"""(?:@(?:KafkaListener|RabbitListener|EventPattern|MessagePattern)\s*\([^)]*['"]([^'"]+)['"]|\.subscribe\s*\(\s*['"]([^'"]+)['"])""",
    re.I,
)


def _derive_domain(url_or_name: str, rel_file: str) -> str:
    segs = [s for s in url_or_name.strip("/").split("/") if s and not s.startswith(":") and not s.startswith("{")]
    if segs and segs[0].lower() in ("api", "v1", "v2", "v3") and len(segs) >= 2:
        segs = segs[1:]
    if segs:
        dom = segs[0]
        if dom.lower() in ("admin", "me", "pub", "public", "internal") and len(segs) >= 2:
            dom = f"{dom}-{segs[1]}"
    else:
        dom = Path(rel_file).stem
    dom = re.sub(r"[^a-zA-Z0-9_-]+", "-", dom).strip("-").lower()
    return dom or "core"


def _detect_table_ops(snippet: str, tables_map: dict[str, str]) -> dict[str, str]:
    t_ops: dict[str, str] = {}
    for tname in tables_map:
        if not re.search(rf"\b{re.escape(tname)}\b", snippet, re.I):
            continue
        ops = ""
        if re.search(rf"(?:\bINSERT\b[^;]{{0,80}}\b{re.escape(tname)}\b|\b{re.escape(tname)}\b\.(?:create|insert|save|add)\b)", snippet, re.I):
            ops += "C"
        if re.search(rf"(?:\bSELECT\b[^;]{{0,120}}\b{re.escape(tname)}\b|\b{re.escape(tname)}\b\.(?:find|get|where|select|all|first|query)\b)", snippet, re.I):
            ops += "R"
        if re.search(rf"(?:\bUPDATE\b[^;]{{0,80}}\b{re.escape(tname)}\b|\b{re.escape(tname)}\b\.(?:update|upsert|patch)\b)", snippet, re.I):
            ops += "U"
        if re.search(rf"(?:\bDELETE\b[^;]{{0,80}}\b{re.escape(tname)}\b|\b{re.escape(tname)}\b\.(?:delete|remove|destroy)\b)", snippet, re.I):
            ops += "D"
        t_ops[tname] = ops or "R"
    return t_ops


def extract_polyglot_proposal(root: Path, *, allowed_files: set[str] | None = None) -> dict:
    """Scan repository files and return a tmh structure proposal dict {flows, tables}."""
    root = root.resolve()
    if allowed_files is None:
        try:
            from tmh import index_source
            allowed_files = set(index_source.snapshot(root).keys())
        except Exception:
            allowed_files = None
    tables_map: dict[str, str] = {}
    raw_entries: list[dict] = []

    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in sorted(dirnames) if d not in SKIP_DIRS and not d.startswith(".")]
        for fn in sorted(filenames):
            if fn.startswith(".") or fn in ("package-lock.json", "yarn.lock", "pnpm-lock.yaml"):
                continue
            fp = Path(dirpath) / fn
            ext = fp.suffix.lower()
            if ext not in CODE_EXTS:
                continue
            rel = fp.relative_to(root).as_posix()
            if allowed_files is not None and rel not in allowed_files:
                continue
            try:
                lines = fp.read_text(encoding="utf-8", errors="replace").splitlines()
            except Exception:
                continue
            if not lines:
                continue

            # Track file-level controller prefix (NestJS / C# / Spring)
            ctrl_prefix = ""
            for idx, ln in enumerate(lines, start=1):
                # --- 1. Extract Tables / Models ---
                for tbl_re, group_idx in (
                    (_SQL_CREATE_TABLE_RE, 1),
                    (_PRISMA_MODEL_RE, 1),
                    (_TYPEORM_ENTITY_RE, 1),
                    (_CSHARP_TABLE_RE, 1),
                    (_PHP_TABLE_RE, 1),
                    (_PHP_SCHEMA_CREATE_RE, 1),
                    (_RUBY_CREATE_TABLE_RE, 1),
                    (_RUST_SEAORM_TABLE_RE, 1),
                    (_JPA_TABLE_RE, 1),
                ):
                    m = tbl_re.search(ln)
                    if m:
                        tname = m.group(group_idx).strip()
                        if tname and len(tname) > 1 and tname not in tables_map:
                            tables_map[tname] = f"{rel}:{idx}"
                if ext == ".cs":
                    m_db = _CSHARP_DBSET_RE.search(ln)
                    if m_db:
                        tname = m_db.group(2).strip()
                        if tname and tname not in tables_map:
                            tables_map[tname] = f"{rel}:{idx}"
                if ext == ".rs" and "schema" in rel.lower():
                    m_rs = _RUST_DIESEL_TABLE_RE.match(ln)
                    if m_rs:
                        tname = m_rs.group(1).strip()
                        if tname and tname not in tables_map:
                            tables_map[tname] = f"{rel}:{idx}"

                # --- 2. Track Controller Prefixes ---
                if ext in (".ts", ".js"):
                    mc = _NEST_CTRL_RE.search(ln)
                    if mc:
                        ctrl_prefix = "/" + mc.group(1).strip("/")
                elif ext == ".cs":
                    mc = _CSHARP_ROUTE_PREFIX_RE.search(ln)
                    if mc:
                        ctrl_prefix = "/" + mc.group(1).strip("/").replace("[controller]", Path(rel).stem.replace("Controller", "").lower())

                # --- 3. Extract Routes / Jobs / Listeners ---
                def add_entry(kind: str, method: str, path_or_name: str, line_no: int):
                    end_idx = min(len(lines), line_no + 25)
                    raw_entries.append({
                        "kind": kind,
                        "method": method.upper(),
                        "path": path_or_name.strip(),
                        "file": rel,
                        "line": line_no,
                        "end": end_idx,
                        "snippet": "\n".join(lines[line_no - 1 : end_idx]),
                    })

                if ext in (".js", ".ts", ".jsx", ".tsx", ".mjs", ".cjs"):
                    m_rt = _JS_ROUTE_RE.search(ln)
                    if m_rt:
                        add_entry("接口", m_rt.group(1), m_rt.group(2), idx)
                    m_nat = _JS_NATIVE_ROUTE_EQ_RE.search(ln)
                    if m_nat:
                        add_entry("接口", m_nat.group(1), m_nat.group(2), idx)
                    else:
                        m_natm = _JS_NATIVE_ROUTE_MATCH_RE.search(ln)
                        if m_natm:
                            norm_p = re.sub(r"\([^)]+\)", ":id", m_natm.group(2).replace("\\/", "/"))
                            if not norm_p.startswith("/"):
                                norm_p = "/" + norm_p
                            add_entry("接口", m_natm.group(1), norm_p, idx)
                    m_nest = _NEST_METHOD_RE.search(ln)
                    if m_nest:
                        sub = (m_nest.group(2) or "").strip("/")
                        full_p = (ctrl_prefix + ("/" + sub if sub else "")) or "/"
                        add_entry("接口", m_nest.group(1), full_p, idx)
                    if fn in ("route.ts", "route.js") and "/api/" in ("/" + rel):
                        m_nx = _NEXT_HANDLER_RE.search(ln)
                        if m_nx:
                            api_path = "/" + re.sub(r"^.*?app/", "", str(Path(rel).parent))
                            add_entry("接口", m_nx.group(1), api_path, idx)
                elif ext == ".cs":
                    m_cs = _CSHARP_HTTP_ATTR_RE.search(ln)
                    if m_cs:
                        sub = (m_cs.group(2) or "").strip("/")
                        full_p = (ctrl_prefix + ("/" + sub if sub else "")) or "/"
                        add_entry("接口", m_cs.group(1), full_p, idx)
                    m_min = _CSHARP_MINIMAL_RE.search(ln)
                    if m_min:
                        add_entry("接口", m_min.group(1), m_min.group(2), idx)
                elif ext == ".php":
                    m_pl = _PHP_LARAVEL_ROUTE_RE.search(ln)
                    if m_pl:
                        add_entry("接口", m_pl.group(1), m_pl.group(2), idx)
                    m_ps = _PHP_SYMFONY_ROUTE_RE.search(ln)
                    if m_ps:
                        add_entry("接口", "ROUTE", m_ps.group(1), idx)
                elif ext == ".rs":
                    m_ra = _RUST_ATTR_ROUTE_RE.search(ln)
                    if m_ra:
                        add_entry("接口", m_ra.group(1), m_ra.group(2), idx)
                    m_ax = _RUST_AXUM_ROUTE_RE.search(ln)
                    if m_ax:
                        add_entry("接口", m_ax.group(2), m_ax.group(1), idx)
                elif ext == ".rb":
                    m_rb = _RUBY_ROUTE_RE.match(ln)
                    if m_rb:
                        add_entry("接口", m_rb.group(1), "/" + m_rb.group(2).lstrip("/:"), idx)
                elif ext in (".kt", ".kts", ".scala"):
                    m_kt = _KTOR_ROUTE_RE.match(ln)
                    if m_kt:
                        add_entry("接口", m_kt.group(1), m_kt.group(2), idx)
                    m_jm = _JVM_MAPPING_RE.search(ln)
                    if m_jm:
                        add_entry("接口", m_jm.group(1), m_jm.group(2), idx)
                elif ext in (".c", ".cc", ".cpp", ".h", ".hpp"):
                    m_cr = _CPP_CROW_RE.search(ln) or _CPP_DROGON_RE.search(ln)
                    if m_cr:
                        add_entry("接口", "HTTP", m_cr.group(1), idx)

                m_cron = _CRON_JOB_RE.search(ln)
                if m_cron:
                    expr = next((g for g in m_cron.groups() if g), "cron")
                    add_entry("定时任务", "CRON", f"{Path(rel).stem}:{expr}", idx)
                m_mq = _MQ_LISTENER_RE.search(ln)
                if m_mq:
                    topic = next((g for g in m_mq.groups() if g), "topic")
                    add_entry("消息监听", "MQ", topic, idx)

    tables = [{"name": name, "where": where} for name, where in sorted(tables_map.items())]
    seen_ids: set[str] = set()
    seen_locs: set[tuple[str, int]] = set()
    seen_triggers: set[str] = set()
    flows = []
    for ent in raw_entries[:180]:
        loc = (ent["file"], ent["line"])
        if loc in seen_locs:
            continue
        trigger = f"{ent['method']} {ent['path']}"
        if trigger in seen_triggers:
            continue
        seen_locs.add(loc)
        seen_triggers.add(trigger)
        slug = re.sub(r"[^a-zA-Z0-9]+", "-", f"{ent['method']}-{ent['path']}").strip("-").lower()
        if not slug:
            slug = f"entry-{ent['line']}"
        base_id = slug[:48]
        fid = base_id
        n = 2
        while fid in seen_ids:
            fid = f"{base_id}-{n}"
            n += 1
        seen_ids.add(fid)

        trigger = f"{ent['method']} {ent['path']}" if ent["kind"] == "接口" else f"{ent['method']} {ent['path']}"
        dom = _derive_domain(ent["path"], ent["file"])
        t_ops = _detect_table_ops(ent["snippet"], tables_map)
        flows.append({
            "id": fid,
            "name": trigger,
            "kind": ent["kind"],
            "trigger": trigger,
            "entry": f"{ent['file']}:{ent['line']}",
            "steps": [{"name": f"处理 {trigger}", "where": f"{ent['file']}:{ent['line']}-{ent['end']}"}],
            "tables": t_ops,
            "domain": dom,
        })

    return {"flows": flows, "tables": tables}


def build_polyglot_structure(root: Path) -> dict:
    """Extract polyglot structure and commit it into tmh's verified graph.db.

    Automatically avoids colliding with any flows/tables already parsed by tmh's
    native Java/Go/Python/C# extractors (supports mixed-language monorepos).
    """
    from tmh.graph import Graph, build as graph_build, graph_path

    existing_triggers: set[str] = set()
    existing_ids: set[str] = set()
    existing_tables: set[str] = set()
    if not graph_path(root).exists():
        graph_build(root, use_cache=True)
    if graph_path(root).exists():
        try:
            g = Graph(root)
            for n in g.nodes("flow"):
                if n.get("trigger"):
                    existing_triggers.add(str(n["trigger"]))
                raw_id = str(n.get("id") or "")
                existing_ids.add(raw_id.removeprefix("flow:"))
            for n in g.nodes("tbl"):
                if n.get("name"):
                    existing_tables.add(str(n["name"]).lower())
        except Exception:
            pass

    proposal = extract_polyglot_proposal(root)
    statecharts = extract_and_write_statecharts(root)
    new_flows = [
        f for f in proposal["flows"]
        if f["trigger"] not in existing_triggers and f["id"] not in existing_ids
    ]
    new_tables = [
        t for t in proposal["tables"]
        if t["name"].lower() not in existing_tables
    ]
    core_pages = ensure_tmh_core_pages(root, proposal, statecharts)
    if not new_flows and not new_tables:
        return {
            "applied": False,
            "flows": 0,
            "tables": 0,
            "statecharts": len(statecharts),
            "corePages": core_pages,
            "errors": [],
        }

    proposal_to_apply = {"flows": new_flows, "tables": new_tables}
    proposal_text = json.dumps(proposal_to_apply, ensure_ascii=False, indent=2)
    struct_path = root / ".tmh" / "knowledge" / "structure.json"
    struct_path.parent.mkdir(parents=True, exist_ok=True)
    struct_path.write_text(proposal_text, encoding="utf-8")
    res = graph_build(root, use_cache=False, proposal_text=proposal_text)
    return {
        "applied": not bool(res.get("structure_errors")),
        "flows": len(new_flows),
        "tables": len(new_tables),
        "statecharts": len(statecharts),
        "corePages": core_pages,
        "errors": res.get("structure_errors") or [],
    }


_STATE_ASSIGN_RE = re.compile(
    r"""(?:\b([a-zA-Z_][a-zA-Z0-9_]*)\.)?status\s*(?:=|:)\s*['"]([a-zA-Z][a-zA-Z0-9_-]{1,28})['"]""",
)
_STATE_GUARD_RE = re.compile(
    r"""(?:\b([a-zA-Z_][a-zA-Z0-9_]*)\.)?status\s*(?:===|!==|==|!=)\s*['"]([a-zA-Z][a-zA-Z0-9_-]{1,28})['"]""",
)
_STATE_IGNORED_VALS = {"ok", "error", "err", "true", "false", "null", "none", "success", "fail", "200", "400", "401", "403", "404", "500"}
_FUNC_DEF_ANY_RE = re.compile(
    r"""^\s*(?:export\s+)?(?:async\s+)?(?:function|def|func|public\s+\S+|private\s+\S+)\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*\(""",
)
_AUTH_GUARD_RE = re.compile(r"""(?:401|403|unauthorized|forbidden|role\s*!==|role\s*!=|requireAuth|isAdmin)""", re.I)


def extract_and_write_statecharts(root: Path) -> list[dict]:
    """Deterministically extract code-level status state machines into .tmh/knowledge/statecharts/*.json."""
    sc_dir = root / ".tmh" / "knowledge" / "statecharts"
    all_exts = CODE_EXTS | {".py", ".go", ".java"}
    by_entity: dict[str, dict] = {}

    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS and not d.startswith(".")]
        for fn in filenames:
            ext = Path(fn).suffix.lower()
            if ext not in all_exts:
                continue
            fp = Path(dirpath) / fn
            try:
                rel = fp.relative_to(root).as_posix()
                if fp.stat().st_size > 300_000:
                    continue
                lines = fp.read_text(encoding="utf-8", errors="replace").splitlines()
            except OSError:
                continue

            cur_fn = Path(fn).stem
            recent_guard: tuple[str, str, int] | None = None  # (entity, state, line)
            for idx, ln in enumerate(lines, 1):
                mf = _FUNC_DEF_ANY_RE.match(ln)
                if mf:
                    cur_fn = mf.group(1)
                    recent_guard = None

                mg = _STATE_GUARD_RE.search(ln)
                if mg:
                    ent = (mg.group(1) or Path(fn).stem).lower()
                    st = mg.group(2).strip()
                    if st.lower() not in _STATE_IGNORED_VALS:
                        recent_guard = (ent, st, idx)
                        bucket = by_entity.setdefault(ent, {"states": {}, "edges": [], "rules": []})
                        bucket["states"].setdefault(st, {"where": f"{rel}:{idx}", "summary": f"前置状态校验 ({cur_fn})"})
                        bucket["rules"].append({
                            "text": f"{cur_fn}: 前置状态校验 {ln.strip()[:80]}",
                            "where": f"{rel}:{idx}",
                            "holds": True,
                        })

                ma = _STATE_ASSIGN_RE.search(ln)
                if ma:
                    ent = (ma.group(1) or Path(fn).stem).lower()
                    st = ma.group(2).strip()
                    if st.lower() in _STATE_IGNORED_VALS:
                        continue
                    bucket = by_entity.setdefault(ent, {"states": {}, "edges": [], "rules": []})
                    bucket["states"].setdefault(st, {"where": f"{rel}:{idx}", "summary": f"由 {cur_fn} 写入状态"})
                    if recent_guard and (idx - recent_guard[2]) <= 25 and recent_guard[1] != st:
                        bucket["edges"].append((recent_guard[1], st, cur_fn))
                    elif any(k in cur_fn.lower() for k in ("create", "new", "init", "insert", "add", "submit")):
                        bucket["edges"].append(("[*]", st, cur_fn))
                    elif any(k in st.lower() for k in ("pending", "draft", "init", "created", "open")):
                        bucket["edges"].append(("[*]", st, cur_fn))

    written: list[dict] = []
    for ent, info in by_entity.items():
        if len(info["states"]) < 2:
            continue
        slug = re.sub(r"[^a-zA-Z0-9_-]+", "-", ent).strip("-").lower() or "entity"
        out_path = sc_dir / f"{slug}-lifecycle.json"
        # Preserve human-authored statecharts
        if out_path.exists():
            try:
                existing = json.loads(out_path.read_text(encoding="utf-8", errors="replace"))
                if existing.get("status") == "已确认":
                    written.append(existing)
                    continue
            except Exception:
                pass

        seen_edges: list[tuple[str, str, str]] = []
        for e in info["edges"]:
            if e not in seen_edges:
                seen_edges.append(e)
        # Ensure at least one initial edge if none inferred
        state_names = list(info["states"].keys())
        if not any(e[0] == "[*]" for e in seen_edges) and state_names:
            seen_edges.insert(0, ("[*]", state_names[0], "初始"))
        if len(seen_edges) < 2 and len(state_names) >= 2:
            for i in range(len(state_names) - 1):
                pair = (state_names[i], state_names[i + 1], "流转")
                if pair not in seen_edges:
                    seen_edges.append(pair)

        mermaid_lines = ["stateDiagram-v2"]
        for src_s, dst_s, lbl in seen_edges[:24]:
            mermaid_lines.append(f"  {src_s} --> {dst_s} : {lbl}")

        payload = {
            "title": f"{slug} 状态机流转图",
            "domain": slug,
            "status": "待确认",
            "mermaid": "\n".join(mermaid_lines) + "\n",
            "states": info["states"],
            "rules": info["rules"][:12],
            "paths": [],
        }
        sc_dir.mkdir(parents=True, exist_ok=True)
        out_path.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
        written.append(payload)
    return written


def _is_empty_stub(path: Path) -> bool:
    if not path.exists():
        return True
    try:
        lines = [ln.strip() for ln in path.read_text(encoding="utf-8", errors="replace").splitlines() if ln.strip()]
    except OSError:
        return True
    # Only title + description / empty markdown table header
    meaningful = [ln for ln in lines if not ln.startswith("#") and not ln.startswith("|---") and not ln.startswith("| 业务词 |") and not ln.startswith("| 问题 |")]
    return len(meaningful) <= 1


def ensure_tmh_core_pages(root: Path, proposal: dict, statecharts: list[dict]) -> list[str]:
    """Populate empty TMH core knowledge stubs (glossary, decisions, pitfalls, open-questions) with file:line evidence."""
    kdir = root / ".tmh" / "knowledge"
    kdir.mkdir(parents=True, exist_ok=True)
    updated: list[str] = []

    flows = proposal.get("flows") or []
    tables = proposal.get("tables") or []

    # 1. glossary.md
    gl_path = kdir / "glossary.md"
    if _is_empty_stub(gl_path) and (flows or tables or statecharts):
        rows = ["# 术语表", "", "| 业务词 | 代码 / 表 | 说明 |", "|---|---|---|"]
        for t in tables[:30]:
            rows.append(f"| `{t['name']}` | `{t.get('where', '')}` | 数据表 / 核心实体 |")
        for sc in statecharts[:10]:
            for st_name, meta in (sc.get("states") or {}).items():
                rows.append(f"| `{sc.get('domain', '')}.{st_name}` | `{meta.get('where', '')}` | {meta.get('summary', '业务状态')} |")
        for f in flows[:20]:
            rows.append(f"| `{f.get('trigger', f.get('name', ''))}` | `{f.get('entry', '')}` | 业务域 `{f.get('domain', 'core')}` 入口 |")
        gl_path.write_text("\n".join(rows) + "\n", encoding="utf-8")
        updated.append("glossary.md")

    # 2. decisions.md
    dec_path = kdir / "decisions.md"
    if _is_empty_stub(dec_path):
        stack_notes: list[str] = []
        if (root / "package.json").exists():
            stack_notes.append("- **Node.js / JavaScript 运行时**：检测到 `package.json:1`，采用 Node.js 工程结构。")
        if (root / "go.mod").exists():
            stack_notes.append("- **Go 后端服务**：检测到 `go.mod:1`，采用 Go 模块化构建。")
        if (root / "pyproject.toml").exists() or (root / "requirements.txt").exists():
            stack_notes.append("- **Python 服务**：检测到 Python 依赖清单。")
        if (root / "pom.xml").exists() or (root / "build.gradle").exists():
            stack_notes.append("- **JVM / Spring 服务**：检测到 Maven/Gradle 构建配置。")
        if tables:
            stack_notes.append(f"- **数据存储模型**：共识别 {len(tables)} 张数据表（如 `{tables[0]['name']}` 位于 `{tables[0].get('where', '')}`）。")
        if flows:
            doms = sorted({f.get("domain") or "core" for f in flows})
            stack_notes.append(f"- **业务入口与分域**：共识别 {len(flows)} 条业务入口，归属 {len(doms)} 个业务域（`{', '.join(doms[:8])}`）。")
        if statecharts:
            stack_notes.append(f"- **状态机设计**：识别到 {len(statecharts)} 个实体生命周期状态机，已固化至 `.tmh/knowledge/statecharts/`。")
        if stack_notes:
            dec_path.write_text("# 技术与业务决定\n\n以下为基于源码自动提取的架构与技术决策（含真实 `file:line` 证据，可人工补充原因）：\n\n" + "\n".join(stack_notes) + "\n", encoding="utf-8")
            updated.append("decisions.md")

    # 3. pitfalls.md
    pit_path = kdir / "pitfalls.md"
    if _is_empty_stub(pit_path):
        pits: list[str] = []
        for sc in statecharts:
            for r in (sc.get("rules") or [])[:8]:
                pits.append(f"- **状态前置约束**：{r.get('text', '')}（证据：`{r.get('where', '')}`）")
        for dirpath, dirnames, filenames in os.walk(root):
            dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS and not d.startswith(".")]
            for fn in filenames:
                if Path(fn).suffix.lower() not in (CODE_EXTS | {".py", ".go", ".java"}):
                    continue
                fp = Path(dirpath) / fn
                try:
                    rel = fp.relative_to(root).as_posix()
                    for idx, ln in enumerate(fp.read_text(encoding="utf-8", errors="replace").splitlines(), 1):
                        if _AUTH_GUARD_RE.search(ln) and len(pits) < 16:
                            pits.append(f"- **权限 / 边界拦截**：`{ln.strip()[:90]}`（证据：`{rel}:{idx}`）")
                except OSError:
                    continue
        if pits:
            pit_path.write_text("# 坑与反直觉的地方\n\n以下为源码中的关键状态前置校验与权限拦截边界，改动时请重点核查：\n\n" + "\n".join(pits) + "\n", encoding="utf-8")
            updated.append("pitfalls.md")

    # 4. open-questions.md
    oq_path = kdir / "open-questions.md"
    if _is_empty_stub(oq_path) and (flows or statecharts):
        rows = ["# 待确认问题", "", "| 问题 | 涉及页面 | 要问谁 | 答复 |", "|---|---|---|---|"]
        for sc in statecharts[:5]:
            rows.append(f"| `{sc.get('domain', '')}` 状态机各终态是否允许回退或重复提交？ | `statecharts/{sc.get('domain', '')}-lifecycle.json` | 业务负责人 | 待确认 |")
        for f in flows[:5]:
            if f.get("kind") == "接口":
                rows.append(f"| 入口 `{f.get('trigger', '')}`（`{f.get('entry', '')}`）的权限角色边界与幂等策略是否已对齐线上？ | `modules/` | 接口负责人 | 待确认 |")
        oq_path.write_text("\n".join(rows) + "\n", encoding="utf-8")
        updated.append("open-questions.md")

    return updated
