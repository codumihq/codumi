"""Codumi Python extractor.

Static resolution with the standard library `ast`: names are followed
through imports (absolute and relative), module attributes, `self.method`,
`cls.method` and `Class.method`. Calls on objects whose type is unknown are
not followed. Emits one JSON object per line. Run by the Go binary.
"""
import ast
import json
import os
import re
import sys

ROOT = os.path.abspath(sys.argv[1] if len(sys.argv) > 1 else ".")
ONLY = None
if len(sys.argv) > 2:
    with open(sys.argv[2], encoding="utf-8") as fh:
        ONLY = {l.strip() for l in fh if l.strip()}

SKIP = {".git", ".venv", "venv", "env", "__pycache__", "site-packages", "node_modules", "build", "dist", ".tox", ".mypy_cache", ".pytest_cache", ".codumi", ".eggs"}
HTTP = {"get", "post", "put", "patch", "delete", "head", "options"}
TABLE_OPS = {"select", "insert", "update", "upsert", "delete"}
STORAGE_OPS = {"upload", "download", "list", "remove", "move", "copy", "update", "create_signed_url", "create_signed_urls", "get_public_url"}
SQL_METHODS = {"execute", "executemany", "fetch", "fetchrow", "fetchval", "fetch_one", "fetch_all", "exec_driver_sql", "mogrify", "raw"}
API_LIKE = re.compile(r"^/(api|v\d+)(/|$)")
LOOKS_SQL = re.compile(r"^\s*(select|insert|update|delete|with|upsert|merge)\b", re.I | re.S)


def out(o):
    sys.stdout.write(json.dumps(o) + "\n")


def rel(p):
    return os.path.relpath(p, ROOT).replace(os.sep, "/")


def py_files():
    for d, dirs, files in os.walk(ROOT):
        dirs[:] = [x for x in dirs if x not in SKIP and not x.startswith(".")]
        for f in files:
            if f.endswith(".py"):
                yield os.path.join(d, f)


def module_names(path):
    """Dotted names a file can be imported as (from the root and from src/)."""
    r = rel(path)[:-3]
    parts = r.split("/")
    if parts[-1] == "__init__":
        parts = parts[:-1]
    names = [".".join(parts)] if parts else []
    if parts and parts[0] == "src" and len(parts) > 1:
        names.append(".".join(parts[1:]))
    return names


class Module:
    def __init__(self, path, tree):
        self.path = path
        self.file = rel(path)
        self.tree = tree
        self.defs = {}  # name -> (kind, line)
        self.classes = {}  # class -> {member: line}
        self.imports = {}  # alias -> ("module", modname) | ("name", modname, attr)
        self.package = ".".join(module_names(path)[0].split(".")[:-1]) if not path.endswith("__init__.py") else module_names(path)[0] if module_names(path) else ""


def collect(m):
    for node in m.tree.body:
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            m.defs[node.name] = ("function", node.lineno)
        elif isinstance(node, ast.ClassDef):
            m.defs[node.name] = ("class", node.lineno)
            members = {}
            for b in node.body:
                if isinstance(b, (ast.FunctionDef, ast.AsyncFunctionDef)):
                    members[b.name] = b.lineno
                elif isinstance(b, (ast.Assign, ast.AnnAssign)):
                    for t in (b.targets if isinstance(b, ast.Assign) else [b.target]):
                        if isinstance(t, ast.Name):
                            members[t.id] = b.lineno
            m.classes[node.name] = members
        elif isinstance(node, (ast.Assign, ast.AnnAssign)):
            for t in (node.targets if isinstance(node, ast.Assign) else [node.target]):
                for n in ast.walk(t):
                    if isinstance(n, ast.Name):
                        m.defs.setdefault(n.id, ("var", node.lineno))
        elif isinstance(node, ast.Import):
            for a in node.names:
                if a.asname:
                    m.imports[a.asname] = ("module", a.name)
                else:
                    m.imports[a.name.split(".")[0]] = ("module", a.name.split(".")[0])
        elif isinstance(node, ast.ImportFrom):
            base = node.module or ""
            if node.level:
                pkg = m.package.split(".") if m.package else []
                up = pkg[: len(pkg) - (node.level - 1)] if node.level > 1 else pkg
                base = ".".join([p for p in up + ([node.module] if node.module else []) if p])
            for a in node.names:
                if a.name == "*":
                    continue
                m.imports[a.asname or a.name] = ("name", base, a.name)


def main():
    mods = {}
    by_name = {}
    for p in py_files():
        try:
            with open(p, encoding="utf-8") as fh:
                src = fh.read()
            tree = ast.parse(src, filename=p)
        except (SyntaxError, UnicodeDecodeError, ValueError):
            continue
        m = Module(p, tree)
        m.lines = src.count("\n") + 1
        collect(m)
        mods[m.file] = m
        for n in module_names(p):
            by_name[n] = m

    def resolve_import(m, alias):
        """What an imported alias points to: (Module, name or None)."""
        imp = m.imports.get(alias)
        if not imp:
            return None
        if imp[0] == "module":
            t = by_name.get(imp[1])
            return (t, None) if t else None
        _, base, attr = imp
        t = by_name.get(base)
        if t and attr in t.defs:
            return (t, attr)
        sub = by_name.get(base + "." + attr if base else attr)
        if sub:
            return (sub, None)
        if t:
            # re-exported through the package __init__
            r = resolve_import(t, attr)
            if r:
                return r
        return None

    files = 0
    for m in mods.values():
        if ONLY is not None and m.file not in ONLY:
            continue
        files += 1
        out({"k": "file", "f": m.file, "lines": m.lines})
        for name, (kind, line) in m.defs.items():
            out({"k": "decl", "f": m.file, "name": name, "kind": kind, "line": line, "exported": not name.startswith("_")})
        for cls, members in m.classes.items():
            for mem, line in members.items():
                out({"k": "decl", "f": m.file, "name": cls + "." + mem, "kind": "member", "line": line, "exported": not mem.startswith("_")})
        Walker(m, resolve_import).run()
    out({"k": "done", "files": files})


def dep_names(node):
    """Os nomes dentro de Depends(x) / Security(x), em qualquer ponto da expressão."""
    out = []
    for n in ast.walk(node):
        if isinstance(n, ast.Call):
            f = n.func
            fname = f.attr if isinstance(f, ast.Attribute) else (f.id if isinstance(f, ast.Name) else "")
            if fname in ("Depends", "Security") and n.args:
                a = n.args[0]
                if isinstance(a, ast.Call):
                    a = a.func
                if isinstance(a, ast.Attribute):
                    out.append(a.attr)
                elif isinstance(a, ast.Name):
                    out.append(a.id)
    return out


def const_str(node):
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        return node.value
    if isinstance(node, ast.JoinedStr):
        s = ""
        for v in node.values:
            if isinstance(v, ast.Constant) and isinstance(v.value, str):
                s += v.value
            else:
                s += "${}"
        return s
    return None


class Walker(ast.NodeVisitor):
    def __init__(self, m, resolve_import):
        self.m = m
        self.resolve_import = resolve_import
        self.anchor = ["<module>"]
        self.cls = [None]
        self.locals = [set()]
        self.vartypes = [{}]  # local name -> (Module, class) from `x = Class(...)`
        self.parents = {}
        self.prefix = {}  # router var -> prefix
        self.deps = {}  # router var -> dependências (APIRouter(dependencies=[Depends(x)]))

    def run(self):
        for p in ast.walk(self.m.tree):
            for c in ast.iter_child_nodes(p):
                self.parents[c] = p
        # APIRouter(prefix="/v1") / Blueprint(url_prefix="/v1")
        for node in self.m.tree.body:
            if isinstance(node, ast.Assign) and isinstance(node.value, ast.Call):
                for kw in node.value.keywords:
                    if kw.arg == "dependencies":
                        for t in node.targets:
                            if isinstance(t, ast.Name):
                                self.deps[t.id] = dep_names(kw.value)
                    if kw.arg in ("prefix", "url_prefix"):
                        v = const_str(kw.value)
                        if v:
                            for t in node.targets:
                                if isinstance(t, ast.Name):
                                    self.prefix[t.id] = v
        self.visit(self.m.tree)

    # ---- scopes ------------------------------------------------------------
    def visit_ClassDef(self, node):
        for d in node.decorator_list:
            self.visit(d)
        for b in node.bases:
            self.visit(b)
        top = len(self.anchor) == 1
        if top:
            self.anchor.append(node.name)
        self.cls.append(node.name if top else None)
        for b in node.body:
            self.visit(b)
        self.cls.pop()
        if top:
            self.anchor.pop()

    def _func(self, node):
        for d in node.decorator_list:
            self.visit(d)
            self.route(d, node)
        cls = self.cls[-1]
        name = None
        if len(self.anchor) == 1:
            name = node.name
        elif cls and len(self.anchor) == 2:
            name = cls + "." + node.name
        if name:
            self.anchor.append(name)
        loc = set(a.arg for a in node.args.args + node.args.kwonlyargs + node.args.posonlyargs)
        if node.args.vararg:
            loc.add(node.args.vararg.arg)
        if node.args.kwarg:
            loc.add(node.args.kwarg.arg)
        for n in ast.walk(node):
            if isinstance(n, ast.Name) and isinstance(n.ctx, ast.Store):
                loc.add(n.id)
        types = {}
        for n in ast.walk(node):
            if isinstance(n, ast.Assign) and isinstance(n.value, ast.Call) and isinstance(n.value.func, ast.Name):
                c = self.resolve_class(n.value.func.id)
                if c:
                    for t in n.targets:
                        if isinstance(t, ast.Name):
                            types[t.id] = c
        self.locals.append(loc)
        self.vartypes.append(types)
        self.cls.append(cls)
        for b in node.body:
            self.visit(b)
        self.cls.pop()
        self.vartypes.pop()
        self.locals.pop()
        if name:
            self.anchor.pop()

    visit_FunctionDef = _func
    visit_AsyncFunctionDef = _func

    # ---- references --------------------------------------------------------
    def resolve_class(self, name):
        if name in self.m.classes:
            return (self.m, name)
        r = self.resolve_import(self.m, name)
        if r and r[1] and r[1] in r[0].classes:
            return r
        return None

    def ref(self, node, tf, to, kind):
        out({"k": "ref", "f": self.m.file, "line": node.lineno, "from": self.anchor[-1], "tf": tf, "to": to, "kind": kind})

    def kind_of(self, node):
        p = self.parents.get(node)
        if isinstance(p, ast.Call) and p.func is node:
            return "call"
        return "ref"

    def visit_Name(self, node):
        if isinstance(node.ctx, ast.Load) and node.id not in self.locals[-1]:
            m = self.m
            if node.id in m.defs:
                self.ref(node, m.file, node.id, self.kind_of(node))
            else:
                r = self.resolve_import(m, node.id)
                if r and r[1]:
                    self.ref(node, r[0].file, r[1], self.kind_of(node))
        self.generic_visit(node)

    def visit_Attribute(self, node):
        if isinstance(node.ctx, ast.Load):
            v = node.value
            # Class(...).method  and  x = Class(...); x.method
            known = None
            if isinstance(v, ast.Call) and isinstance(v.func, ast.Name) and v.func.id not in self.locals[-1]:
                known = self.resolve_class(v.func.id)
            elif isinstance(v, ast.Name) and v.id in self.vartypes[-1]:
                known = self.vartypes[-1][v.id]
            if known and node.attr in known[0].classes[known[1]]:
                self.ref(node, known[0].file, known[1] + "." + node.attr, self.kind_of(node))
            elif isinstance(v, ast.Name):
                cls = self.cls[-1]
                if v.id in ("self", "cls") and cls and node.attr in self.m.classes.get(cls, {}):
                    self.ref(node, self.m.file, cls + "." + node.attr, self.kind_of(node))
                elif v.id in self.m.classes and node.attr in self.m.classes[v.id] and v.id not in self.locals[-1]:
                    self.ref(node, self.m.file, v.id + "." + node.attr, self.kind_of(node))
                elif v.id not in self.locals[-1]:
                    r = self.resolve_import(self.m, v.id)
                    if r:
                        t, name = r
                        if name is None and node.attr in t.defs:
                            self.ref(node, t.file, node.attr, self.kind_of(node))
                        elif name and name in t.classes and node.attr in t.classes[name]:
                            self.ref(node, t.file, name + "." + node.attr, self.kind_of(node))
        self.generic_visit(node)

    # ---- routes, database, API calls ---------------------------------------
    def route(self, dec, fn):
        if not isinstance(dec, ast.Call) or not isinstance(dec.func, ast.Attribute) or not dec.args:
            return
        path = const_str(dec.args[0])
        if path is None:
            return
        verb = dec.func.attr.lower()
        methods = []
        if verb in HTTP:
            methods = [verb.upper()]
        elif verb in ("route", "api_route", "websocket"):
            for kw in dec.keywords:
                if kw.arg == "methods" and isinstance(kw.value, (ast.List, ast.Tuple, ast.Set)):
                    methods = [const_str(e).upper() for e in kw.value.elts if const_str(e)]
            methods = methods or (["GET"] if verb == "route" else ["ANY"])
        else:
            return
        base = self.prefix.get(dec.func.value.id, "") if isinstance(dec.func.value, ast.Name) else ""
        full = (base.rstrip("/") + "/" + path.lstrip("/")) if base else path
        if not full.startswith("/"):
            full = "/" + full
        cls = self.cls[-1]
        handler = fn.name if len(self.anchor) == 1 else (cls + "." + fn.name if cls else fn.name)
        # guardas: outros decorators (@login_required), Depends(...) nos
        # parâmetros, dependencies=[…] na rota e no router
        guards = []
        for d in fn.decorator_list:
            if d is dec:
                continue
            f = d.func if isinstance(d, ast.Call) else d
            n = f.attr if isinstance(f, ast.Attribute) else (f.id if isinstance(f, ast.Name) else None)
            if n:
                guards.append(n)
        for default in list(fn.args.defaults) + [x for x in fn.args.kw_defaults if x is not None]:
            guards += dep_names(default)
        for a in fn.args.args + fn.args.kwonlyargs:
            if a.annotation is not None:
                guards += dep_names(a.annotation)  # Annotated[User, Depends(current_user)]
        for kw in dec.keywords:
            if kw.arg == "dependencies":
                guards += dep_names(kw.value)
        if isinstance(dec.func.value, ast.Name):
            guards += self.deps.get(dec.func.value.id, [])
        g = ",".join(dict.fromkeys(guards)) or "none"
        for meth in methods:
            out({"k": "route", "f": self.m.file, "method": meth, "path": full, "line": dec.lineno, "handler": handler, "guards": g})

    def chain_ops(self, call, allowed):
        ops, cur = [], call
        while True:
            p = self.parents.get(cur)
            if isinstance(p, ast.Attribute) and p.value is cur:
                gp = self.parents.get(p)
                if isinstance(gp, ast.Call) and gp.func is p:
                    if p.attr in allowed and p.attr not in ops:
                        ops.append(p.attr)
                    cur = gp
                    continue
            return ops

    def visit_Call(self, node):
        f = node.func
        if isinstance(f, ast.Attribute) and node.args:
            arg = const_str(node.args[0])
            attr = f.attr
            if arg is not None and "${}" not in arg:
                client = ast.unparse(f.value)[:60] if hasattr(ast, "unparse") else ""
                if attr in ("table", "from_") and isinstance(f.value, ast.Attribute) and f.value.attr == "storage":
                    out({"k": "db", "f": self.m.file, "line": node.lineno, "from": self.anchor[-1], "type": "bucket", "name": arg, "ops": self.chain_ops(node, STORAGE_OPS), "client": client})
                elif attr in ("table", "from_"):
                    out({"k": "db", "f": self.m.file, "line": node.lineno, "from": self.anchor[-1], "type": "table", "name": arg, "ops": self.chain_ops(node, TABLE_OPS), "client": client})
                elif attr == "rpc":
                    out({"k": "db", "f": self.m.file, "line": node.lineno, "from": self.anchor[-1], "type": "rpc", "name": arg, "ops": [], "client": client})
                elif attr in SQL_METHODS and LOOKS_SQL.match(arg):
                    out({"k": "sqlq", "f": self.m.file, "line": node.lineno, "from": self.anchor[-1], "q": arg, "client": client})
        self.generic_visit(node)

    def api(self, node, value):
        path = re.sub(r"^(\$\{\})+", "", value).split("?")[0]
        m = re.match(r"^https?://[^/]+(/.*)$", path)
        if m:
            path = m.group(1)
        if not API_LIKE.match(path):
            return
        method, p = "", self.parents.get(node)
        for _ in range(4):
            if p is None:
                break
            if isinstance(p, ast.Call) and isinstance(p.func, ast.Attribute) and p.func.attr.lower() in HTTP:
                method = p.func.attr.upper()
                break
            p = self.parents.get(p)
        out({"k": "call_route", "f": self.m.file, "line": node.lineno, "from": self.anchor[-1], "path": path, "method": method})

    def visit_Constant(self, node):
        if isinstance(node.value, str) and not isinstance(self.parents.get(node), ast.JoinedStr) and not self._is_decorator_arg(node):
            self.api(node, node.value)

    def visit_JoinedStr(self, node):
        s = const_str(node)
        if s and not self._is_decorator_arg(node):
            self.api(node, s)
        self.generic_visit(node)

    def _is_decorator_arg(self, node):
        p = self.parents.get(node)
        if isinstance(p, ast.keyword) and p.arg in ("prefix", "url_prefix", "root_path"):
            return True
        gp = self.parents.get(p) if p is not None else None
        return isinstance(p, ast.Call) and isinstance(gp, (ast.FunctionDef, ast.AsyncFunctionDef)) and p in gp.decorator_list


if __name__ == "__main__":
    main()
