#!/usr/bin/env python3
"""Regenerate backend/ROUTES.md and backend/TESTS.md from the frozen Python backend.

Stdlib only. Run from anywhere:  python backend/port/inventory.py

Hand-edited columns survive a rerun, keyed by (method, path) for routes and
(python file, python test) for tests:
  ROUTES.md: go, port status, note
  TESTS.md:  go test, status, reason, mutation
Rows that vanish from the Python source are dropped; a warning names any that
carried hand edits.
"""
from __future__ import annotations

import ast
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
PY = ROOT / "backend-python"
ROUTERS = PY / "app" / "api" / "v1" / "routers"
MAIN = PY / "app" / "main.py"
OAUTH = PY / "app" / "mcp" / "oauth.py"
TESTS = PY / "tests"
FRONTEND = ROOT / "frontend" / "src"
ROUTES_MD = ROOT / "backend" / "ROUTES.md"
TESTS_MD = ROOT / "backend" / "TESTS.md"

METHODS = {"get", "post", "put", "patch", "delete"}
AUTH_NAMES = {
    "current_admin": "admin session",
    "verify_email_webhook_token": "email webhook token",
    "verify_webhook_hmac": "webhook hmac",
}
ROUTE_HEAD = ["method", "path", "auth", "status", "python", "frontend", "go", "port status", "note"]
TEST_HEAD = ["python file", "python test", "go test", "status", "reason", "mutation"]


# ---------------------------------------------------------------- helpers

def const_str(node: ast.AST | None) -> str | None:
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        return node.value
    return None


def normalise(path: str) -> str:
    """'/x/{account_id:int}' -> '/x/{account_id}'."""
    return re.sub(r"\{([^}:]+):[^}]*\}", r"{\1}", path)


def router_prefixes(tree: ast.AST) -> dict[str, str]:
    """Variable name -> prefix for every `x = APIRouter(prefix=...)` in the file."""
    out: dict[str, str] = {}
    for node in ast.walk(tree):
        if (
            isinstance(node, ast.Assign)
            and len(node.targets) == 1
            and isinstance(node.targets[0], ast.Name)
            and isinstance(node.value, ast.Call)
            and getattr(node.value.func, "id", None) == "APIRouter"
        ):
            prefix = ""
            for kw in node.value.keywords:
                if kw.arg == "prefix":
                    prefix = const_str(kw.value) or ""
            out[node.targets[0].id] = prefix
    return out


def auth_of(fn: ast.FunctionDef | ast.AsyncFunctionDef) -> str:
    found = []
    args = fn.args
    defaults = list(args.defaults) + [d for d in args.kw_defaults if d is not None]
    for d in defaults:
        if isinstance(d, ast.Call) and getattr(d.func, "id", None) == "Depends" and d.args:
            name = getattr(d.args[0], "id", None)
            if name in AUTH_NAMES:
                found.append(AUTH_NAMES[name])
    return ", ".join(found) if found else "none"


def routes_in(path: Path, base_prefix: str, location: str) -> list[dict]:
    tree = ast.parse(path.read_text(encoding="utf-8"))
    prefixes = router_prefixes(tree)
    rows = []
    for fn in ast.walk(tree):
        if not isinstance(fn, (ast.FunctionDef, ast.AsyncFunctionDef)):
            continue
        for dec in fn.decorator_list:
            if not (
                isinstance(dec, ast.Call)
                and isinstance(dec.func, ast.Attribute)
                and isinstance(dec.func.value, ast.Name)
                and dec.func.value.id in prefixes
                and dec.func.attr in METHODS
            ):
                continue
            sub = const_str(dec.args[0]) if dec.args else None
            if sub is None:
                raise SystemExit(f"{path}:{fn.lineno}: non-literal route path")
            status = "200"
            for kw in dec.keywords:
                if kw.arg == "status_code":
                    v = kw.value
                    if isinstance(v, ast.Constant):
                        status = str(v.value)
                    elif isinstance(v, ast.Attribute):
                        m = re.search(r"HTTP_(\d{3})_", v.attr)
                        status = m.group(1) if m else v.attr
            rows.append(
                {
                    "method": dec.func.attr.upper(),
                    "path": normalise(base_prefix + prefixes[dec.func.value.id] + sub),
                    "auth": auth_of(fn),
                    "status": status,
                    "python": f"{location}:{fn.lineno}",
                }
            )
    return rows


def mounted_prefix(router_name: str) -> str:
    """The include_router prefix main.py uses (they are all `prefix`, '/api/v1')."""
    src = MAIN.read_text(encoding="utf-8")
    m = re.search(r'prefix\s*=\s*f?"([^"]+)"', src)
    if not m:
        raise SystemExit("cannot find the API prefix in main.py")
    return m.group(1)


# ---------------------------------------------------------------- frontend

def frontend_calls() -> set[tuple[str, str]]:
    """(METHOD, normalised path) pairs for every URL-looking literal under frontend/src.

    A literal that is the first argument of `api.post/put/patch/delete(` counts for
    that verb only. Every other literal (api.get, window.open, href, a URL built into a
    variable and passed on) counts as GET.
    """
    calls: set[tuple[str, str]] = set()
    pat = re.compile(r"""(?:'([^'\n]*)'|"([^"\n]*)"|`([^`]*)`)""")
    verb_call = re.compile(r"\bapi\.(get|post|put|patch|delete)\s*(?:<[^()]*>)?\(\s*$")
    for p in FRONTEND.rglob("*"):
        if p.suffix not in {".ts", ".vue", ".js", ".tsx"}:
            continue
        text = p.read_text(encoding="utf-8", errors="replace")
        for m in pat.finditer(text):
            s = next(g for g in m.groups() if g is not None)
            if not s.startswith("/"):
                continue
            s = re.sub(r"\$\{[^}]*\}", "*", s)
            s = s.split("?")[0].rstrip("/") or "/"
            if s.startswith("/api/v1"):
                s = s[len("/api/v1"):] or "/"
            vm = verb_call.search(text[max(0, m.start() - 80):m.start()])
            verb = vm.group(1).upper() if vm else "GET"
            calls.add((verb, s))
    return calls


def used_by_frontend(method: str, path: str, calls: set[tuple[str, str]], api_prefix: str) -> bool:
    rel = path[len(api_prefix):] if path.startswith(api_prefix) else path
    rel = re.sub(r"\{[^}]*\}", "*", rel).rstrip("/") or "/"
    return (method, rel) in calls


# ---------------------------------------------------------------- tests

def parametrize_count(fn: ast.FunctionDef | ast.AsyncFunctionDef) -> int | None:
    total = 1
    for dec in fn.decorator_list:
        if (
            isinstance(dec, ast.Call)
            and isinstance(dec.func, ast.Attribute)
            and dec.func.attr == "parametrize"
            and len(dec.args) >= 2
        ):
            vals = dec.args[1]
            if isinstance(vals, (ast.List, ast.Tuple)):
                total *= len(vals.elts)
            else:
                return None
    return total


def collect_tests() -> tuple[list[tuple[str, str]], int | None]:
    rows: list[tuple[str, str]] = []
    collected: int | None = 0
    for p in sorted(TESTS.rglob("test_*.py")):
        rel = p.relative_to(PY).as_posix()
        tree = ast.parse(p.read_text(encoding="utf-8"))
        for node in tree.body:
            funcs = []
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                funcs.append((node.name, node))
            elif isinstance(node, ast.ClassDef) and node.name.startswith("Test"):
                funcs += [
                    (f"{node.name}::{n.name}", n)
                    for n in node.body
                    if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef))
                ]
            for name, fn in funcs:
                if not fn.name.startswith("test_"):
                    continue
                rows.append((rel, name))
                n = parametrize_count(fn)
                collected = None if (n is None or collected is None) else collected + n
    return rows, collected


# ---------------------------------------------------------------- markdown io

def esc(cell: str) -> str:
    return cell.replace("|", "\\|")


def split_row(line: str) -> list[str]:
    inner = line.strip()[1:-1]
    return [c.strip().replace("\\|", "|") for c in re.split(r"(?<!\\)\|", inner)]


def read_table(path: Path, ncols: int, key_cols: int) -> dict[tuple, list[str]]:
    out: dict[tuple, list[str]] = {}
    if not path.exists():
        return out
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line.startswith("| ") or line.startswith("| ---"):
            continue
        cells = split_row(line)
        if len(cells) != ncols or cells[0] in {"method", "python file"}:
            continue
        out[tuple(cells[:key_cols])] = cells
    return out


def table(head: list[str], rows: list[list[str]]) -> str:
    lines = ["| " + " | ".join(head) + " |", "| " + " | ".join("---" for _ in head) + " |"]
    lines += ["| " + " | ".join(esc(c) for c in r) + " |" for r in rows]
    return "\n".join(lines) + "\n"


def warn_orphans(kind: str, old: dict, seen: set, default_tail: list[str], tail_from: int) -> None:
    for key, cells in old.items():
        if key not in seen and cells[tail_from:] != default_tail:
            print(f"warning: {kind} row no longer in Python but had hand edits: {key}", file=sys.stderr)


# ---------------------------------------------------------------- main

def main() -> None:
    api_prefix = mounted_prefix("")
    calls = frontend_calls()

    api_rows: list[dict] = []
    for p in sorted(ROUTERS.glob("*.py")):
        if p.name == "__init__.py":
            continue
        api_rows += routes_in(p, api_prefix, p.relative_to(PY).as_posix())
    oauth_rows = routes_in(OAUTH, "", OAUTH.relative_to(PY).as_posix())

    mcp_line = next(
        (i + 1 for i, l in enumerate(MAIN.read_text(encoding="utf-8").splitlines()) if "_mcp_slash_redirect" in l and "def " in l),
        0,
    )
    mount_line = next(
        (i + 1 for i, l in enumerate(MAIN.read_text(encoding="utf-8").splitlines()) if 'app.mount("/mcp"' in l), 0
    )
    mcp_rows = [
        {"method": "GET|POST|DELETE", "path": "/mcp", "auth": "none", "status": "307",
         "python": f"app/main.py:{mcp_line}", "note_default": "redirect to /mcp/"},
        {"method": "MOUNT", "path": "/mcp/", "auth": "oauth bearer", "status": "-",
         "python": f"app/main.py:{mount_line}", "note_default": "ASGI mount of the MCP server"},
    ]

    old = read_table(ROUTES_MD, len(ROUTE_HEAD), 2)
    seen: set = set()
    body: list[list[str]] = []

    def emit(rows: list[dict]) -> None:
        for r in rows:
            key = (r["method"], r["path"])
            seen.add(key)
            prev = old.get(key)
            tail = prev[6:9] if prev else ["", "todo", r.get("note_default", "")]
            fe = "yes" if r["path"].startswith(api_prefix) and used_by_frontend(r["method"], r["path"], calls, api_prefix) else "no"
            body.append([r["method"], r["path"], r["auth"], r["status"], r["python"], fe, *tail])

    sections = [("Under /api/v1", api_rows), ("OAuth and discovery", oauth_rows), ("/mcp", mcp_rows)]
    parts = [
        "# Route inventory\n",
        "Generated by `python backend/port/inventory.py` from `backend-python/`. "
        "Columns `go`, `port status` and `note` are hand-edited and survive a rerun "
        "(rows are keyed by method + path). `frontend` is `yes` when the path template "
        "(params as wildcards) is called with the same method under `frontend/src`: the first argument of "
        "`api.<verb>(...)`, or for GET also `window.open`, hrefs and URLs built into a variable "
        "(`${...}` read as a wildcard, query string ignored). Matching is by method + path; "
        "a path built by string concatenation shows `no`.\n",
        f"Totals: {len(api_rows)} under {api_prefix}, {len(oauth_rows)} OAuth and discovery, /mcp\n",
    ]
    for title, rows in sections:
        body = []
        emit(rows)
        parts.append(f"## {title}\n")
        parts.append(table(ROUTE_HEAD, body))
    warn_orphans("route", old, seen, ["", "todo", ""], 6)
    ROUTES_MD.write_text("\n".join(parts), encoding="utf-8", newline="\n")

    tests, collected = collect_tests()
    oldt = read_table(TESTS_MD, len(TEST_HEAD), 2)
    seen_t: set = set()
    trows = []
    for f, t in tests:
        key = (f, t)
        seen_t.add(key)
        prev = oldt.get(key)
        trows.append([f, t, *(prev[2:6] if prev else ["", "todo", "", ""])])
    warn_orphans("test", oldt, seen_t, ["", "todo", "", ""], 2)
    n_param = f"{collected}" if collected is not None else "unknown"
    tparts = [
        "# Python test inventory\n",
        "Generated by `python backend/port/inventory.py`. Columns `go test`, `status`, `reason` and "
        "`mutation` are hand-edited and survive a rerun (rows are keyed by file + test). "
        "`mutation` names the change that made the Go test fail when checked.\n",
        f"Test functions: {len(tests)}. Collected by pytest: {n_param}. "
        "The collected count is higher because `@pytest.mark.parametrize` turns one function into one test per case.\n",
        table(TEST_HEAD, trows),
    ]
    TESTS_MD.write_text("\n".join(tparts), encoding="utf-8", newline="\n")
    print(f"routes: {len(api_rows)} + {len(oauth_rows)} + {len(mcp_rows)}; tests: {len(tests)} (collected {n_param})")


if __name__ == "__main__":
    main()
