'use strict';
// Codumi TypeScript extractor.
// Uses the project's own TypeScript compiler and type checker, so every
// reference is resolved exactly (no name guessing). Emits one JSON object per
// line on stdout. Run by the Go binary; not meant to be used directly.

const path = require('path');
const fs = require('fs');

const root = path.resolve(process.argv[2] || '.');
const out = (o) => process.stdout.write(JSON.stringify(o) + '\n');
// Optional list of files (relative to root) to walk; the program is still
// built whole so references resolve exactly.
const only = process.argv[3] ? new Set(fs.readFileSync(process.argv[3], 'utf8').split('\n').filter(Boolean)) : null;

function loadTS() {
  const tries = [];
  if (process.env.CODUMI_TYPESCRIPT) tries.push(process.env.CODUMI_TYPESCRIPT);
  for (const base of [root, process.cwd()]) {
    try { tries.push(require.resolve('typescript', { paths: [base] })); } catch (_) {}
  }
  for (const t of tries) { try { return require(t); } catch (_) {} }
  try { return require('typescript'); } catch (_) {}
  process.stderr.write('codumi: the "typescript" package was not found. Install it in the project (npm i -D typescript) or set CODUMI_TYPESCRIPT to its path.\n');
  process.exit(2);
}
const ts = loadTS();

const IGNORE = /(^|\/)(node_modules|\.next|\.nuxt|dist|build|out|coverage|\.git|\.codumi|\.vercel|\.turbo|graphify-out)(\/|$)/;
const rel = (f) => path.relative(root, f).split(path.sep).join('/');
const under = (f) => { const r = rel(f); return r !== '' && !r.startsWith('..') && !path.isAbsolute(r) && !IGNORE.test(r); };

// ---- program -------------------------------------------------------------
let rootNames, options;
const cfgPath = ts.findConfigFile(root, ts.sys.fileExists, 'tsconfig.json');
if (cfgPath) {
  const cfg = ts.readConfigFile(cfgPath, ts.sys.readFile);
  const parsed = ts.parseJsonConfigFileContent(cfg.config || {}, ts.sys, path.dirname(cfgPath));
  rootNames = parsed.fileNames;
  options = parsed.options;
  // tsconfig "solução" (Vite, monorepos): "files": [] e "references" para
  // tsconfig.app.json, tsconfig.node.json… — os arquivos estão nas referências
  if (!rootNames.length && parsed.projectReferences && parsed.projectReferences.length) {
    const seen = new Set();
    let best = null;
    for (const ref of parsed.projectReferences) {
      let p = ts.resolveProjectReferencePath ? ts.resolveProjectReferencePath(ref) : ref.path;
      if (ts.sys.directoryExists(p)) p = path.join(p, 'tsconfig.json');
      if (!ts.sys.fileExists(p)) continue;
      const rc = ts.readConfigFile(p, ts.sys.readFile);
      const rp = ts.parseJsonConfigFileContent(rc.config || {}, ts.sys, path.dirname(p));
      for (const f of rp.fileNames) seen.add(f);
      // as opções do projeto com mais arquivos (o app, não o vite.config)
      if (!best || rp.fileNames.length > best.fileNames.length) best = rp;
    }
    if (best) { rootNames = [...seen]; options = Object.assign({}, best.options, { composite: false, incremental: false }); }
  }
} else {
  rootNames = ts.sys.readDirectory(root, ['.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs'], ['**/node_modules/**'], undefined);
  options = { allowJs: true, jsx: ts.JsxEmit.Preserve, target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext };
}
rootNames = rootNames.filter(under);
const program = ts.createProgram({ rootNames, options: Object.assign({}, options, { noEmit: true, skipLibCheck: true }) });
const checker = program.getTypeChecker();
const inProject = (sf) => !sf.isDeclarationFile && under(sf.fileName);

// ---- helpers -------------------------------------------------------------
function declName(d) {
  if (!d) return null;
  if (d.name && (ts.isIdentifier(d.name) || ts.isStringLiteral(d.name) || ts.isPrivateIdentifier(d.name))) return d.name.text;
  if (ts.isExportAssignment(d)) return 'default';
  if ((ts.isFunctionDeclaration(d) || ts.isClassDeclaration(d)) && !d.name) return 'default';
  return null;
}

function isTopLevel(d) {
  if (d.parent && ts.isSourceFile(d.parent)) return true;
  return ts.isVariableDeclaration(d) && !!d.parent && !!d.parent.parent && !!d.parent.parent.parent && ts.isSourceFile(d.parent.parent.parent);
}

const isMemberLike = (n) => ts.isClassElement(n) || ts.isTypeElement(n);
const isContainer = (n) => ts.isClassLike(n) || ts.isInterfaceDeclaration(n);

// Name of the top-level declaration (or Top.member) that contains `node`.
function anchorOf(node) {
  let n = node, member = null;
  while (n.parent && !ts.isSourceFile(n.parent)) {
    if (isMemberLike(n) && isContainer(n.parent)) member = n;
    n = n.parent;
  }
  let top = n;
  if (ts.isVariableStatement(n)) {
    top = null;
    for (const d of n.declarationList.declarations) {
      if (node.pos >= d.pos && node.end <= d.end) { top = d; break; }
    }
  }
  const tn = declName(top);
  if (!tn) return null;
  if (member && member.parent === n) {
    const mn = declName(member);
    if (mn) return tn + '.' + mn;
  }
  return tn;
}

function targetOf(sym) {
  if (sym.flags & ts.SymbolFlags.Alias) {
    try { sym = checker.getAliasedSymbol(sym); } catch (_) { return null; }
  }
  const d = sym.declarations && sym.declarations[0];
  if (!d || ts.isSourceFile(d)) return null;
  const sf = d.getSourceFile();
  if (!inProject(sf)) return null;
  let name = null;
  if (isTopLevel(d)) name = declName(d);
  else if (isMemberLike(d) && d.parent && isContainer(d.parent) && d.parent.parent && ts.isSourceFile(d.parent.parent)) {
    const p = declName(d.parent), m = declName(d);
    if (p && m) name = p + '.' + m;
  }
  if (!name) return null;
  return { f: rel(sf.fileName), name, decl: d };
}

function lineOf(sf, node) { return sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1; }

function refKind(node) {
  let n = node;
  if (n.parent && ts.isPropertyAccessExpression(n.parent) && n.parent.name === n) n = n.parent;
  const p = n.parent;
  if (!p) return 'ref';
  if (ts.isJsxClosingElement(p)) return null;
  if (ts.findAncestor(node, ts.isImportDeclaration) || ts.findAncestor(node, ts.isImportEqualsDeclaration)) return 'import';
  if (ts.findAncestor(node, ts.isExportDeclaration)) return 'export';
  if (ts.isCallExpression(p) && p.expression === n) return 'call';
  if (ts.isNewExpression(p) && p.expression === n) return 'new';
  if ((ts.isJsxOpeningElement(p) || ts.isJsxSelfClosingElement(p)) && p.tagName === n) return 'jsx';
  if (ts.isTypeReferenceNode(p) || ts.isExpressionWithTypeArguments(p) || ts.isTypeQueryNode(p)) return 'type';
  return 'ref';
}

const KIND = (st) => {
  if (ts.isFunctionDeclaration(st)) return 'function';
  if (ts.isClassDeclaration(st)) return 'class';
  if (ts.isInterfaceDeclaration(st)) return 'interface';
  if (ts.isTypeAliasDeclaration(st)) return 'type';
  if (ts.isEnumDeclaration(st)) return 'enum';
  if (ts.isModuleDeclaration(st)) return 'namespace';
  if (ts.isExportAssignment(st)) return 'default';
  return 'other';
};
const hasExport = (st) => !!(ts.getCombinedModifierFlags && st.modifiers && st.modifiers.some((m) => m.kind === ts.SyntaxKind.ExportKeyword));

const NOT_SUPABASE = /^(Array|Buffer|Object|String|Set|Map|Promise|Observable|Uint8Array|Int8Array|Uint16Array|Int16Array|Uint32Array|Int32Array|Float32Array|Float64Array|BigInt64Array|BigUint64Array|Rx|_|R|Immutable)$/;
function looksSupabase(recv) {
  if (ts.isIdentifier(recv) && NOT_SUPABASE.test(recv.text)) return false;
  try {
    const t = checker.getTypeAtLocation(recv);
    if (t.flags & ts.TypeFlags.Any) return true;
    const props = new Set(t.getProperties().map((p) => p.name));
    return props.has('from') && (props.has('rpc') || props.has('schema') || props.has('storage'));
  } catch (_) { return false; }
}

const TABLE_OPS = new Set(['select', 'insert', 'update', 'upsert', 'delete']);
const STORAGE_OPS = new Set(['upload', 'download', 'list', 'remove', 'move', 'copy', 'update', 'createSignedUrl', 'createSignedUrls', 'createSignedUploadUrl', 'uploadToSignedUrl', 'getPublicUrl', 'exists', 'info']);

function chainOps(call, allowed) {
  const ops = [];
  let cur = call;
  while (cur.parent && ts.isPropertyAccessExpression(cur.parent) && cur.parent.expression === cur &&
         cur.parent.parent && ts.isCallExpression(cur.parent.parent) && cur.parent.parent.expression === cur.parent) {
    const name = cur.parent.name.text;
    if (allowed.has(name) && !ops.includes(name)) ops.push(name);
    cur = cur.parent.parent;
  }
  return ops;
}

function strArg(a) {
  if (!a) return null;
  if (ts.isStringLiteral(a) || ts.isNoSubstitutionTemplateLiteral(a)) return a.text;
  if (ts.isTemplateExpression(a)) {
    let s = a.head.text;
    for (const sp of a.templateSpans) s += '${}' + sp.literal.text;
    return s;
  }
  return null;
}

const short = (sf, n) => n.getText(sf).replace(/\s+/g, '').slice(0, 60);

// ---- routes (Next.js app router) ----------------------------------------
const HTTP = new Set(['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS', 'HEAD']);
// Pages: Next.js app router (app/**/page.tsx) and pages router (pages/**).
function pagePath(r) {
  let m = r.match(/(?:^|\/)app\/(?:(.*?)\/)?page\.[cm]?[jt]sx?$/);
  if (m) {
    const segs = (m[1] || '').split('/').filter((s) => s && !/^\(.*\)$/.test(s) && !s.startsWith('@'));
    return '/' + segs.join('/');
  }
  m = r.match(/(?:^|\/)pages\/(.*)\.[jt]sx?$/);
  if (m && !/^(api\/|_app$|_document$|_error$)/.test(m[1])) {
    const p = m[1].replace(/(^|\/)index$/, '');
    return '/' + p;
  }
  return null;
}

// The component a file exports by default: `export default function Home`,
// `export default Home` or `export default class …`.
function defaultExport(sf) {
  for (const st of sf.statements) {
    const mods = st.modifiers || [];
    const isDefault = mods.some((m) => m.kind === ts.SyntaxKind.DefaultKeyword);
    if (isDefault && (ts.isFunctionDeclaration(st) || ts.isClassDeclaration(st))) return { name: declName(st), line: lineOf(sf, st) };
    if (ts.isExportAssignment(st) && !st.isExportEquals) {
      const e = st.expression;
      if (ts.isIdentifier(e)) return { name: e.text, line: lineOf(sf, st) };
      return { name: 'default', line: lineOf(sf, st) };
    }
  }
  return null;
}

function routePath(r) {
  const clean = (p) => '/' + p.split('/').filter((s) => s && !/^\(.*\)$/.test(s) && !s.startsWith('@')).join('/');
  // Next.js: app/**/route.ts
  let m = r.match(/(?:^|\/)app\/(.*?)\/?route\.[cm]?[jt]sx?$/);
  if (m) return clean(m[1]);
  // SvelteKit: src/routes/**/+server.ts
  m = r.match(/(?:^|\/)src\/routes\/(.*?)\/?\+server\.[jt]s$/);
  if (m) return clean(m[1]);
  // Astro: src/pages/**/*.ts (endpoints)
  m = r.match(/(?:^|\/)src\/pages\/(.*)\.[jt]s$/);
  if (m) return clean(m[1].replace(/(^|\/)index$/, ''));
  return null;
}

// ---- rotas e telas declaradas no código: TanStack Router/Start e React Router ----
const propOf = (obj, name) => obj && ts.isObjectLiteralExpression(obj) ? obj.properties.find((p) => p.name && p.name.getText && p.name.getText() === name) : null;
const joinPath = (a, b) => b.startsWith('/') ? b : (a.replace(/\/$/, '') + '/' + b).replace(/\/+/g, '/');

// o nó que nomeia o componente de um valor: X, <X />, <Guard><X /></Guard>, () => <X />
function componentNode(sf, v) {
  if (!v) return null;
  if (ts.isIdentifier(v)) return v;
  if (ts.isJsxSelfClosingElement(v)) return v.tagName;
  if (ts.isJsxElement(v)) {
    // <ProtectedRoute><Inscricoes /></ProtectedRoute>: a tela é o filho único
    const kids = v.children.filter((c) => ts.isJsxElement(c) || ts.isJsxSelfClosingElement(c));
    if (kids.length === 1) return componentNode(sf, kids[0]);
    return v.openingElement.tagName;
  }
  if (ts.isParenthesizedExpression(v)) return componentNode(sf, v.expression);
  if (ts.isArrowFunction(v) && !ts.isBlock(v.body)) return componentNode(sf, v.body);
  return null;
}
function componentName(sf, v) { const n = componentNode(sf, v); return n ? n.getText(sf) : null; }

// onde o componente da tela está declarado: segue o import (e o lazy(() => import(...)))
function pageTarget(sf, f, v, line) {
  const n = componentNode(sf, v);
  if (!n) return null;
  const fallback = { f, name: n.getText(sf), line };
  let sym;
  try { sym = checker.getSymbolAtLocation(n); } catch (_) { return fallback; }
  const t = sym && targetOf(sym);
  if (!t) return fallback;
  const d = t.decl;
  if (ts.isVariableDeclaration(d) && d.initializer && /import\(/.test(d.initializer.getText())) {
    const m = d.initializer.getText().match(/import\(\s*['"]([^'"]+)['"]/);
    const r = m && ts.resolveModuleName(m[1], d.getSourceFile().fileName, options, ts.sys).resolvedModule;
    const sf2 = r && program.getSourceFile(r.resolvedFileName);
    const de = sf2 && inProject(sf2) && defaultExport(sf2);
    if (de) return { f: rel(sf2.fileName), name: de.name, line: de.line };
  }
  return { f: t.f, name: t.name, line: lineOf(d.getSourceFile(), d) };
}

// TanStack: createFileRoute('/x')({ component: X, server: { handlers: { GET } } }),
// createAPIFileRoute('/api/x')({ GET }), createServerFileRoute('/api/x').methods({ GET })
function tanstack(sf, f, node) {
  const e = node.expression;
  let path = null, opts = null, kind = null;
  if (ts.isCallExpression(e) && ts.isIdentifier(e.expression)) {
    const fn = e.expression.text;
    if (fn === 'createFileRoute' || fn === 'createAPIFileRoute') { path = strArg(e.arguments[0]); opts = node.arguments[0]; kind = fn; }
  } else if (ts.isPropertyAccessExpression(e) && e.name.text === 'methods' && ts.isCallExpression(e.expression) && ts.isIdentifier(e.expression.expression) && e.expression.expression.text === 'createServerFileRoute') {
    path = strArg(e.expression.arguments[0]); opts = node.arguments[0]; kind = 'server';
  }
  if (path === null || !opts) return;
  const line = lineOf(sf, node);
  const handler = anchorOf(node) || '<module>';
  const handlers = (o) => {
    if (!o || !ts.isObjectLiteralExpression(o)) return;
    for (const p of o.properties) {
      const n = p.name && p.name.getText(sf);
      if (n && HTTP.has(n)) out({ k: 'route', f, method: n, path, line: lineOf(sf, p), handler });
    }
  };
  if (kind === 'createFileRoute') {
    const t = pageTarget(sf, f, propOf(opts, 'component') && propOf(opts, 'component').initializer, line);
    if (t) out({ k: 'page', f: t.f, path, line: t.line, name: t.name });
    const srv = propOf(opts, 'server');
    if (srv && srv.initializer) { const h = propOf(srv.initializer, 'handlers'); if (h) handlers(h.initializer); }
  } else handlers(opts);
}

// React Router: <Route path="x" element={<X />} /> (com os pais) e
// createBrowserRouter([{ path, element | Component, children }])
function reactRouterJsx(sf, f, node) {
  const tag = node.tagName.getText(sf);
  if (tag !== 'Route') return;
  const attr = (n) => node.attributes.properties.find((a) => ts.isJsxAttribute(a) && a.name.getText(sf) === n);
  const pathOf = (el) => {
    const a = el.attributes.properties.find((x) => ts.isJsxAttribute(x) && x.name.getText(sf) === 'path');
    return a && a.initializer && ts.isStringLiteral(a.initializer) ? a.initializer.text : null;
  };
  let path = pathOf(node);
  const idx = attr('index');
  if (path === null && !idx) return;
  if (path === null) path = '';
  // sobe pelos <Route> pais
  for (let p = node.parent; p; p = p.parent) {
    if (ts.isJsxElement(p) && p.openingElement.tagName.getText(sf) === 'Route' && p.openingElement !== node) {
      const pp = pathOf(p.openingElement);
      if (pp !== null) path = path ? joinPath(pp, path) : pp;
    }
  }
  if (!path.startsWith('/')) path = '/' + path;
  const el = attr('element') || attr('Component') || attr('component');
  const t = el && el.initializer && pageTarget(sf, f, ts.isJsxExpression(el.initializer) ? el.initializer.expression : el.initializer, lineOf(sf, node));
  if (t) out({ k: 'page', f: t.f, path, line: t.line, name: t.name });
}

function reactRouterObject(sf, f, node) {
  const p = propOf(node, 'path');
  const el = propOf(node, 'element') || propOf(node, 'Component');
  if (!p || !el || !p.initializer || !ts.isStringLiteral(p.initializer)) return;
  let path = p.initializer.text;
  // filhos de outra rota em children: [...]
  for (let q = node.parent; q; q = q.parent) {
    if (ts.isObjectLiteralExpression(q) && q !== node) {
      const pp = propOf(q, 'path');
      if (pp && pp.initializer && ts.isStringLiteral(pp.initializer)) path = joinPath(pp.initializer.text, path);
    }
  }
  if (!path.startsWith('/')) path = '/' + path;
  const t = pageTarget(sf, f, el.initializer, lineOf(sf, node));
  if (t) out({ k: 'page', f: t.f, path, line: t.line, name: t.name });
}

// ---- walk ----------------------------------------------------------------
let files = 0;
for (const sf of program.getSourceFiles()) {
  if (!inProject(sf)) continue;
  files++;
  const f = rel(sf.fileName);
  if (only && !only.has(f)) continue;
  out({ k: 'file', f, lines: sf.getLineStarts().length });

  const rpath = routePath(f);
  const ppath = pagePath(f);
  if (ppath !== null) {
    const d = defaultExport(sf);
    if (d) out({ k: 'page', f, path: ppath, line: d.line, name: d.name });
  }
  for (const st of sf.statements) {
    const exported = hasExport(st);
    if (ts.isVariableStatement(st)) {
      for (const d of st.declarationList.declarations) {
        const name = declName(d);
        if (!name) continue;
        const init = d.initializer;
        const kind = init && (ts.isArrowFunction(init) || ts.isFunctionExpression(init)) ? 'function' : 'const';
        out({ k: 'decl', f, name, kind, line: lineOf(sf, d), exported });
        if (rpath && exported && HTTP.has(name)) out({ k: 'route', f, method: name, path: rpath, line: lineOf(sf, d) });
      }
      continue;
    }
    if (ts.isExportDeclaration(st) && rpath && st.exportClause && ts.isNamedExports(st.exportClause)) {
      for (const el of st.exportClause.elements) {
        if (HTTP.has(el.name.text)) out({ k: 'route', f, method: el.name.text, path: rpath, line: lineOf(sf, el) });
      }
      continue;
    }
    const name = declName(st);
    if (!name) continue;
    out({ k: 'decl', f, name, kind: KIND(st), line: lineOf(sf, st), exported: exported || ts.isExportAssignment(st) });
    if (rpath && exported && HTTP.has(name) && ts.isFunctionDeclaration(st)) out({ k: 'route', f, method: name, path: rpath, line: lineOf(sf, st) });
    if ((ts.isClassDeclaration(st) || ts.isInterfaceDeclaration(st)) && st.members) {
      for (const m of st.members) {
        const mn = declName(m);
        if (mn) out({ k: 'decl', f, name: name + '.' + mn, kind: 'member', line: lineOf(sf, m), exported });
      }
    }
  }

  const visit = (node) => {
    if (ts.isIdentifier(node)) handleIdent(sf, f, node);
    else if (ts.isCallExpression(node)) { handleCall(sf, f, node); tanstack(sf, f, node); }
    else if (ts.isJsxSelfClosingElement(node) || ts.isJsxOpeningElement(node)) reactRouterJsx(sf, f, node);
    else if (ts.isObjectLiteralExpression(node)) reactRouterObject(sf, f, node);
    ts.forEachChild(node, visit);
  };
  visit(sf);
}

function handleIdent(sf, f, node) {
  const p = node.parent;
  if (p && ts.isImportSpecifier(p) && p.propertyName && p.name === node) return; // `a as b`: count `a` only
  let sym;
  try {
    sym = p && ts.isShorthandPropertyAssignment(p) && p.name === node
      ? checker.getShorthandAssignmentValueSymbol(p)
      : checker.getSymbolAtLocation(node);
  } catch (_) { return; }
  if (!sym) return;
  const t = targetOf(sym);
  if (!t || t.decl.name === node) return; // declaration itself
  const kind = refKind(node);
  if (!kind) return;
  out({ k: 'ref', f, line: lineOf(sf, node), from: anchorOf(node) || '<module>', tf: t.f, to: t.name, kind });
}

function handleCall(sf, f, node) {
  const e = node.expression;
  const arg = strArg(node.arguments[0]);
  if (arg === null) return;

  if (ts.isPropertyAccessExpression(e) && (e.name.text === 'from' || e.name.text === 'rpc')) {
    const recv = e.expression;
    let type = null, client = recv;
    if (e.name.text === 'rpc') {
      if (looksSupabase(recv)) type = 'rpc';
    } else if (ts.isPropertyAccessExpression(recv) && recv.name.text === 'storage') {
      type = 'bucket'; client = recv.expression;
    } else if (looksSupabase(recv)) {
      type = 'table';
    }
    if (!type) return;
    const ops = chainOps(node, type === 'bucket' ? STORAGE_OPS : TABLE_OPS);
    out({ k: 'db', f, line: lineOf(sf, node), from: anchorOf(node) || '<module>', type, name: arg, ops, client: short(sf, client) });
    return;
  }

  // Calls to API paths: fetch('/api/x'), axios.post('/v1/x'), or any
  // wrapper like call(`${base}/v1/x/${id}`, token, { method: 'PATCH' }).
  const path = arg.replace(/^(\$\{\})+/, '');
  if (!path.startsWith('/')) return;
  // o caminho de quem declara a rota não é chamada (TanStack)
  if (ts.isIdentifier(e) && /^create(File|APIFile|ServerFile|Lazy|LazyFile)?Route$/.test(e.text)) return;
  const isFetch = ts.isIdentifier(e) && e.text === 'fetch';
  const verb = ts.isPropertyAccessExpression(e) && /^(get|post|put|patch|delete)$/.test(e.name.text) ? e.name.text.toUpperCase() : '';
  const apiLike = /^\/(api|v\d+)(\/|$)/.test(path);
  if (!isFetch && !verb && !apiLike) return;
  let method = verb;
  for (const a of node.arguments.slice(1)) {
    if (!ts.isObjectLiteralExpression(a)) continue;
    for (const p of a.properties) {
      if (ts.isPropertyAssignment(p) && p.name && p.name.getText(sf) === 'method') {
        const m = strArg(p.initializer);
        if (m) method = m.toUpperCase();
      }
    }
  }
  if (!method && isFetch) method = 'GET';
  out({ k: 'call_route', f, line: lineOf(sf, node), from: anchorOf(node) || '<module>', path: path.split('?')[0], method });
}

out({ k: 'done', files });
