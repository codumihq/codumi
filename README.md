<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="brand/logo-dark.svg">
    <img src="brand/logo-light.svg" alt="Codumi" width="320">
  </picture>
</p>

<details align="center">
  <summary><b>Read this in other languages</b></summary>
  <br><a href="README.pt-BR.md">Português (Brasil)</a>
</details>

<p align="center">
  <a href="https://github.com/codumihq/codumi/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/codumihq/codumi/actions/workflows/ci.yml/badge.svg"></a>
  <a href="LICENSE"><img alt="License FSL-1.1-ALv2" src="https://img.shields.io/badge/license-FSL--1.1--ALv2-0e9a63"></a>
  <img alt="Go 1.26" src="https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white">
  <img alt="Indexes TypeScript, Go, Dart, Python, Kotlin/Java, C#, PHP, Rust, Ruby, Elixir, Swift and SQL" src="https://img.shields.io/badge/indexes-TS%20%C2%B7%20Go%20%C2%B7%20Dart%20%C2%B7%20Python%20%C2%B7%20Kotlin%2FJava%20%C2%B7%20C%23%20%C2%B7%20PHP%20%C2%B7%20Rust%20%C2%B7%20Ruby%20%C2%B7%20Elixir%20%C2%B7%20Swift%20%C2%B7%20SQL-3b4a44">
  <img alt="MCP" src="https://img.shields.io/badge/MCP-Claude%20Code%20%C2%B7%20Codex%20%C2%B7%20Cursor%20%C2%B7%20Gemini-0e9a63">
  <img alt="App for macOS and Windows" src="https://img.shields.io/badge/app-macOS%20%C2%B7%20Windows-3b4a44?logo=apple&logoColor=white">
  <img alt="Made in Brazil" src="https://img.shields.io/badge/made%20in-Brazil-009c3b">
</p>

# Codumi

**The exact map of your codebase for AI coding agents, from the click to the database.**

Codumi indexes TypeScript (React, Next.js, TanStack, Vue/Nuxt, Svelte, Astro), Go, Dart/Flutter, Python, Kotlin/Java/Scala, C#, PHP/Laravel, Rust, Ruby/Rails, Elixir/Phoenix, Swift and Supabase projects and answers the questions an agent (or you) asks all day, in a few lines instead of a dozen file reads:

- **Who uses this?** Every call site, with the line. No guessing.
- **What does this route touch?** Tables, RPCs and buckets, following the real call graph.
- **Who can reach this table?** RLS, policies, grants and every place the code uses it.
- **Where are the holes?** `codumi audit` replays your migrations and flags what anon can reach.
- **What does my change affect?** `codumi impact` reads the git diff and follows it up to callers, routes, pages, tables and the tests on the path.
- **What is dead code?** `codumi unused` lists what nobody calls, leaving out what frameworks call by themselves.

## Why exact matters

Many code-graph tools infer edges from names or let an LLM fill the gaps. That works for a tour, but not for refactors or security reviews, where one missed call site is the bug.

Codumi uses your project's own type checker to resolve every reference: the TypeScript compiler for TS/JS, `go/types` for Go and `package:analyzer` for Dart. Python has no built-in type checker, so there Codumi resolves names statically through imports, `self.method`, `Class.method` and `x = Class(...)`; calls on objects of unknown type are not followed. Kotlin/Java and Rust are read the same static way, without Gradle or the Rust compiler: Kotlin/Java through the file's package, imports (with aliases and `*`), `Class.member` and `this.member`; Rust through modules (`lib.rs`, `a.rs`, `a/mod.rs`, inline `mod`), `use` trees (aliases, lists, `*`), `crate::`/`self::`/`super::` paths, other crates in the workspace, `Self::` and `self.method()`. It keeps **every occurrence as its own row**, so two calls from the same function are two results, each with its line.

## Measured on a real app

We ran 12 typical agent tasks on a production Next.js + Supabase app (2,300 files, 403 migrations). For each task we compared what an agent reads without Codumi (grep output plus the files it opens) with Codumi's answer, counting 1 token per 4 bytes on both sides.

| Task | Without Codumi | With Codumi | Savings |
|---|---:|---:|---:|
| Find the uses of 5 shared functions (auth guard, rate limiter, a hook, a component, a session helper) | 42.2k tokens | 16.7k | 2.5× |
| Understand 3 tables: RLS, policies and every use in code | 40.6k | 9.3k | 4.4× (3.1×–7.4×) |
| Understand 3 API routes: handler and everything they touch in the database | 16.9k | 1.8k | 9.5× |
| Security review of the schema (all 403 migrations) | 252k | 5.2k | 49× |
| **All together** | **352k** | **33k** | **~10.7×** |

- **Day to day** (uses, tables and routes, without the audit), the agent's context is about **3.6× smaller**.
- **The audit** is where the gap is widest: without Codumi, the migrations alone (252k tokens) don't fit in one read.
- **Speed:** queries answer in 0.01–0.05 s, against 0.13–0.26 s for grep. The full audit takes 0.6 s.

These numbers are conservative. The "without" side counts only the grep output and the files a careful agent would open: for routes, just the route file and its direct imports. Real sessions usually read more. For tables, part of the gap is grep noise, since a common table name also matches comments and strings.

## Install

```sh
go install github.com/codumihq/codumi@latest
```

For TypeScript you need Node.js and the project's own `typescript` package, which is already there in any TS project. For Go you need the Go toolchain. For Dart you need the Dart or Flutter SDK; on first use Codumi builds a native Dart reader (about 30s, once) and keeps the analyzer's summary cache, so re-runs are fast. For Python you need `python3` (stdlib only, nothing is imported from your project). Kotlin/Java/Scala, C#, PHP, Rust, Ruby, Elixir and Swift need nothing installed: Codumi reads the source itself.

## Use

```sh
cd your-project
codumi index            # first run ~20s on a 2,300-file Next.js app, then incremental (~0.1s if nothing changed, ~3s for a few files)
```

### `codumi refs <name>`: every use, exactly

```
$ codumi refs markPaid
markPaid  [function]  src/lib/orders.ts:16
2 uses in 2 files (call 2, import 2)
src/app/api/webhooks/payments/route.ts
  7      POST                         call
src/app/api/webhooks/stripe/route.ts
  11     POST                         call
```

It works for functions, components (`jsx`), types, and `Class.member`. You can also give the file: `codumi refs src/lib/orders.ts:markPaid`.

### `codumi route <path>`: what an endpoint touches

```
$ codumi route /api/orders
POST /api/orders  src/app/api/orders/route.ts:3
database: 2 objects across 2 uses (--detail for sites and call paths)
  rpc    adjust_stock                                                  ×1
  table  orders                           insert,select                ×1

called from: 1 place
  src/components/OrderButton.tsx:5  OrderButton
```

It follows the handler through the call graph (5 levels) to every `.from()`, `.rpc()` and `.storage.from()`. Uses through a service-role client are marked `[admin]`.

### `codumi page <path>`: what a screen is made of

```
$ codumi page /
page /  src/app/page.tsx:13  Home
components (JSX, 3 levels; --depth=N for more):
  Home  src/app/page.tsx:13
  └─ Hero  src/app/page.tsx:3
     └─ OrderButton  src/components/OrderButton.tsx:5
ui: Button  (--all to expand)
api calls: POST /api/orders
```

It reads Next.js app-router (`app/**/page.tsx`, with its layouts) and pages-router pages. The tree follows the JSX, keeps design-system primitives (`components/ui/…`) on a single line and skips components it already showed. It also lists the tables, RPCs and API routes the components use.

### `codumi find "<text>"`: where a piece of UI copy lives

```
$ codumi find "fresh coffee"
"fresh coffee": 1 match in 1 file
src/app/page.tsx
  6     Hero                     <h1>Fresh coffee, delivered</h1>
```

The search is case-insensitive and runs only on the project's indexed source files, never on `node_modules` or build output. Each match comes with the component or function it is in.

### Across repos: `codumi link`

Do you have a Go API and a TS app that calls it? Index both and link them. `codumi route` will then show callers from the other repo, matched by path *and* HTTP method:

```
$ cd api && codumi link ../web
$ codumi route /v1/stores/{id}
DELETE /v1/stores/{id}  api: internal/http/routes.go:55
handler: Server.deleteStore
database: 1 object across 2 uses (--detail for sites and call paths)
  table  stores                           delete,select                ×2
called from: 1 place
  web: src/lib/api-client.ts:150  deleteStore
```

On the Go side, Codumi reads `net/http` (1.22 patterns), gorilla/mux, chi, gin and echo routes. It finds the real handler inside middleware (`s.admin(s.deleteStore)`) and takes tables from the SQL passed to `Query`/`Exec`. On the TS side, it records calls to API paths: `fetch`, axios, or your own wrapper (`call('/v1/x/${id}', { method: 'PATCH' })`). Dart works the same way: `http`, dio, or wrappers like `Api.getChecked('/api/orders/$id')`. So a Next.js route shows its callers in the web app *and* in the Flutter app:

```
$ codumi route /api/orders/[id]
GET /api/orders/[id]  web: src/app/api/orders/[id]/route.ts:8
database: 2 objects across 2 uses (--detail for sites and call paths)
  table  order_items                      select                       ×1
  table  orders                           select                       ×1
called from: 2 places
  web: src/components/orders/order-page.tsx:41  OrderPage
  app: lib/screens/order_screen.dart:88  _OrderScreenState._load
```

In Dart, Supabase calls (`.from()`, `.rpc()`, `.storage.from()`) are tracked like in TS. In Python, Codumi reads FastAPI and Flask routes (decorators, with `APIRouter(prefix=…)`), supabase-py (`.table()`, `.rpc()`, `.storage`), SQL in `execute()`/`fetch()`, and API calls with `requests`/`httpx`. In Kotlin/Java: Spring (`@GetMapping`, class-level `@RequestMapping`) and Ktor (`route { get("/x") }`) routes, Retrofit interfaces (`@GET("api/…")`) as API calls, supabase-kt (`from()`, `rpc()`, `storage.from()`) and SQL in strings and Room `@Query`. In Rust: axum (`.route("/x", get(handler))`) and actix/rocket (`#[get("/x")]`) routes, `/api/…` calls and SQL strings (sqlx). In C#: ASP.NET controllers (`[Route("api/[controller]")]`, `[HttpGet("{id}")]`) and minimal APIs (`app.MapGet`), Entity Framework (`DbSet<T>`, `[Table]`) as table uses, and SQL strings. In PHP: Laravel routes (`Route::get`, prefix groups, `apiResource`, `routes/api.php` under `/api`), Eloquent models (`Order::where(...)->update()` is a write to `orders`) and `DB::table()`. On the TypeScript side, Codumi also reads TanStack Router/Start (`createFileRoute`, `createServerFileRoute`), React Router (`<Route>` and route objects), SvelteKit (`+page.svelte`, `+server.ts`) and Astro (`src/pages`), plus `.vue`, `.svelte` and `.astro` components (imports, `<Component>` and `<kebab-case>` in the template, calls in `@click`/`:prop`). Vue: Vue Router route objects (also lazy `() => import()`) become screens; Nuxt: `pages/` screens, `server/api` routes (`x.get.ts`), and components/composables used without import. In Ruby on Rails: `config/routes.rb` (`resources`, `resource`, `namespace`, `scope`, `member`/`collection`, `get "x", to: "c#a"`), ActiveRecord models (`Order.where(...).update_all` is a write to `orders`, `self.table_name`), callbacks (`before_action :x`) and constants resolved through the lexical nesting. In Elixir/Phoenix: the router (`scope`, `resources`, `live`, with LiveView screens), `alias`/`import`, and Ecto (`schema "orders"`, `Repo.all/insert/update/delete`, `from(o in Order)`). In Swift: types, extensions and methods across the module, SwiftUI views as components, Vapor routes (`app.get("x", use: h)`, `grouped`) and supabase-swift. In Scala: the Kotlin/Java reader with `def`, `trait` and Scala imports, plus Play's `conf/routes`. ORMs on top of any of these: Prisma (`prisma.order.findMany`, `@@map`), Drizzle (`pgTable`, `db.select().from(orders)`), Django (`Order.objects.filter(...).update()`, `db_table`) and SQLAlchemy (`__tablename__`, `session.query(Order)`).

### `codumi table <name>`: one table, view, function or bucket

```
$ codumi table orders
table public.orders  (RLS on)  supabase/migrations/001_init.sql:15
policies: 2
  select authenticated          "members read orders"
         using (org_id in (select public.my_org_ids()))
  ...
code: 2 uses in 1 file
  src/lib/orders.ts:7  createOrder  insert,select
  src/lib/orders.ts:17  markPaid  update  [admin client, bypasses RLS]
```

For a function, it shows the full signature and who calls it **inside the database** too: other functions, the triggers that fire them, and policies.

```
$ codumi table adjust_stock
function public.adjust_stock(p_product uuid, p_delta int) → void  (SECURITY DEFINER (bypasses RLS))  supabase/migrations/001_init.sql:44
execute: anon,authenticated,public,service_role
database: 1 use (functions, triggers and policies that call it)
  function public.tg_sale()  supabase/migrations/004_sales.sql:12  ← trigger trg_sale on public.sales
code: 1 use in 1 file
  src/lib/orders.ts:13  reserveStock
```

### `codumi impact`: what a change affects

Run it before finishing a task (agents get it as `codumi_impact`). It reads `git diff` (uncommitted, or `--base=main` for a branch), finds the declarations whose lines changed and walks the call graph up:

```
$ codumi impact
1 changed files, 1 changed declarations

changed:
  main.go: markPaid

callers (2, up to 4 levels):
  Server.pixWebhook      main.go  [Server.pixWebhook → markPaid]
  Server.signedWebhook   main.go  [Server.signedWebhook → markPaid]

API routes affected:
  POST /v1/webhooks/pix      via Server.pixWebhook → markPaid
  POST /v1/webhooks/signed   via Server.signedWebhook → markPaid

database touched by the changed code:
  table orders           update
  table payment_events   insert
```

### `codumi unused`: dead code

Declarations with no real use (imports don't count). Pages, route handlers, `main`, tests and methods are left out, since frameworks and interfaces call them. Internal ones are safe to remove; exported ones may be used by another project.

### `codumi audit`: access-control holes in a Supabase project

Codumi replays `supabase/migrations/*.sql` in order, keeping the final state of tables, RLS, policies, functions, grants and buckets. Then it checks:

| Rule | Severity | What it means |
|---|---|---|
| `rls-disabled` | high | A table without RLS can be read and changed by anyone with the anon key. |
| `security-definer-anon` | high / low | A SECURITY DEFINER function that anon can call (Postgres grants EXECUTE to PUBLIC by default). It is high if the body never checks the caller, and low if it uses `auth.uid()`, directly or through a helper. |
| `storage-listable` | high / medium | A storage SELECT policy that only checks `bucket_id`, so anyone (or any signed-in user) can **list** every file. |
| `view-security-definer` | medium | A view that runs as its owner and ignores the RLS of the tables it reads. |
| `webhook-unverified` | medium (heuristic) | A webhook route with no signature or secret check that never re-fetches the event from the provider. Codumi follows the call graph, so `verifySignature()` or `getPayment()` in a helper counts. |
| `route-no-auth` | medium (heuristic) | A route that writes to the database (directly or through what it calls) with no login guard before it: no `before_action :authenticate…` (Rails, including concerns and `skip_before_action`), no `auth` middleware (Laravel), no auth plug in `pipe_through` (Phoenix), no `[Authorize]` (ASP.NET), no `@PreAuthorize` (Spring), no `Depends(current_user)` or `@login_required` (FastAPI, Flask). Login, signup, password and webhook paths are skipped. |
| `service-role-public` / `service-role-client` | high | The service role key is read from a `NEXT_PUBLIC_*` variable or referenced in a client component. |
| `policy-open-write` / `policy-open-read` | high / low | A policy that is `true` for anon. |
| `bucket-public` | low | The files are readable by anyone who has the URL. |

```
$ codumi audit
codumi audit: 3 high, 2 medium, 2 low

HIGH   security-definer-anon  public.adjust_stock()
       SECURITY DEFINER function runs with its owner's rights (bypassing RLS), never checks who is calling, and anon can call it at /rest/v1/rpc/adjust_stock.
       at   supabase/migrations/001_init.sql:44
       fix  revoke execute on function public.adjust_stock from public, anon;  -- and check ownership inside it
       used src/lib/orders.ts:13
...
```

Use `--fail-on=high` in CI to fail the build when a finding at that severity or above appears.

### `codumi note <module>`: what the code can't tell

Some things are never in the code: why a choice was made, the cache you must clear after changing a credential, what is still pending. Module notes hold them in a short Markdown card per feature (`.codumi/notes/<slug>.md`), committed with the code. Codumi adds a live section from the index when it shows a note, so the facts in that part are never stale:

```
$ codumi note new orders --paths=src/lib/orders.ts,src/app/api/orders
$ codumi note orders
# Orders
Order creation, stock reservation and payment marking

## Gotchas
- `adjust_stock` is SECURITY DEFINER and anon can still call it (see `codumi audit`). Revoke it before going live.
...
## Live (from the index)
files: 2
routes: 1
  POST   /api/orders  src/app/api/orders/route.ts:3
database: 4 objects
  rpc adjust_stock                                                    ×1
  table orders                             insert,select,update       ×2  [admin]
used by (outside the module): 2 files
  src/app/api/webhooks/payments/route.ts                       ×1
depends on: 1 file
  src/lib/supabase.ts                                          ×5
```

`codumi note --file=src/lib/orders.ts` returns the notes that cover a file. Agents call it (as `codumi_note` over MCP) before editing. `codumi notes` lists all notes, flagging any whose paths no longer match files.

### `codumi sql "<query>"`

The index is a plain SQLite file. Agents can query it directly:

```sh
codumi sql "select name, count(*) from db_uses where type='table' group by name order by 2 desc limit 10"
```

The tables are `files`, `decls`, `refs`, `db_uses`, `routes`, `route_calls`, `sql_tables`, `sql_policies`, `sql_functions` and `buckets`.

The commands also have Portuguese aliases: `codumi usa`, `codumi tabela`, `codumi rota` and `codumi auditoria`.

### `codumi erro`: a production error, followed to the code

Paste an error with its stack trace (`codumi erro < error.txt`) or pass a Sentry issue (`--sentry=<link>`, with `SENTRY_AUTH_TOKEN`). Each stack line is resolved to the declaration it falls in, and from there up to the API routes and pages that reach it, what the failing code touches in the database and the tests on the path. Agents get it as `codumi_error`.

### `codumi pr`: the pull request review

`codumi pr --base=main` writes a Markdown comment with what the change reaches and the audit findings in the changed files; `--fail-on=high` fails the check. The GitHub Action in this repository posts it on every pull request:

```yaml
- uses: actions/checkout@v4
  with: { fetch-depth: 0 }
- uses: codumihq/codumi@main
  with: { fail-on: high }
```

### `codumi banco`: the real database × the migrations

`codumi banco --db=postgresql://… --rls` compares the live database with your migrations (tables created in the dashboard, migrations not applied, RLS and SECURITY DEFINER that differ) and checks what `anon` and a random signed-in user can read and write. Read-only transaction, 3 s per query, no row counts.

## With AI agents

**MCP** (Claude Code, Cursor, Codex and others):

```sh
claude mcp add codumi -- codumi mcp
```

The server exposes `codumi_refs`, `codumi_route`, `codumi_page`, `codumi_find`, `codumi_table`, `codumi_audit`, `codumi_note`, `codumi_sql` and `codumi_index`. They return the same compact text as the CLI, and the index is built on first use.

**Keep it fresh:** `codumi hook install` re-indexes in the background after every commit, checkout, merge and rebase. It keeps your existing hooks, and `codumi hook uninstall` removes only its own block.

**Without MCP**, add this to your `CLAUDE.md`, `AGENTS.md` or `.cursorrules`:

```md
This repo has a Codumi index. Before reading files to find usages, run:
- `codumi refs <name>` for every call site of a function, component or type
- `codumi route <path>` for what an API route touches in the database
- `codumi table <name>` for RLS, policies and code uses of a table, RPC or bucket
Run `codumi index` after pulling or after large changes.
```

Every command takes `--json`.

## Try it

`examples/demo` is a tiny Next.js + Supabase app with deliberate holes:

```sh
cd examples/demo && npm i && codumi index && codumi audit
```

## Status and roadmap

It covers TypeScript/JavaScript (Next.js, React Router, TanStack, Vue/Nuxt, SvelteKit, Astro), Go, Dart/Flutter, Python, Kotlin/Java/Scala, C#, PHP/Laravel, Rust, Ruby/Rails, Elixir/Phoenix, Swift, Prisma/Drizzle/Django/SQLAlchemy and Supabase SQL migrations.

- [x] MCP server (`codumi mcp`)
- [x] Background re-index on commit (`codumi hook install`)
- [x] Go
- [x] Links between repos (a Go API ↔ the TS client that calls it)
- [x] Dart / Flutter
- [x] Python (static resolution)
- [x] Kotlin/Java, C#, PHP/Laravel, Rust (static resolution)
- [x] Svelte, Astro, TanStack, React Router
- [x] Vue/Nuxt, Ruby/Rails, Elixir/Phoenix, Swift, Scala
- [x] Prisma, Drizzle, Django, SQLAlchemy
- [x] Incremental indexing: only the files that changed, plus the files that used anything renamed or removed (`--full` rebuilds everything)
- [x] Module notes: short human-written cards per feature with a live section from the index

Limits worth knowing:

- The SQL side is a migration replayer, not a full Postgres parser. Dynamic SQL (`EXECUTE` in `DO` blocks) is reported but not followed.
- Overloaded functions are tracked by name.
- `webhook-unverified` is a heuristic. Webhook-settings endpoints (`setWebhook`…) and routes behind auth middleware are skipped.
- `route-no-auth` is a heuristic and only runs where Codumi can see the guards (the frameworks above). A handler file that checks the user itself (`current_user`, `authorize`, `request.user`…) is skipped.
- Go route groups (chi `Route`, gin `Group`) don't add their prefix yet.

Treat the audit as a review list, not a verdict.

## Security

- **Local only.** The index is a SQLite file in `.codumi/` inside your project. The MCP server talks over stdio to the agent that started it; nothing listens on the network and nothing is sent anywhere. The usage log (`codumi stats`) stays on your machine.
- **Read-only queries.** `codumi sql` and the `codumi_sql` tool open the index read-only and accept one `SELECT`, `WITH`, `EXPLAIN` or `VALUES` statement; `ATTACH`, `PRAGMA`, `VACUUM` and writes are refused, so a prompt-injected agent cannot use it to read other databases on your disk.
- **Indexing trusts the project, like `npm install`.** To resolve TypeScript exactly, Codumi loads the `typescript` package from the project's own `node_modules`. Index projects you would also install. Go, Dart, Python, Kotlin/Java, C#, PHP, Rust, Svelte and Astro are read without running project code.
- **The desktop app** shows everything from the model and from your files as text, never as HTML, and links open in your browser, never inside the app. Commands and edits from Claude Code wait for your OK unless you pick another permission mode.

Found a problem? Open a private security advisory on GitHub instead of a public issue.

## License

Codumi's engine (this repository: the indexer, the CLI, the MCP server and the GitHub Action) is licensed under the [Functional Source License, Version 1.1, Apache 2.0 Future License](LICENSE) (FSL-1.1-ALv2). You can read it, run it, change it and use it in your own work and company; what it does not allow is offering it as a competing commercial product. Each version becomes Apache 2.0 two years after its release.

The Codumi app for macOS and Windows, and the phone app, are free and closed source: [download them at codumi.dev](https://codumi.dev).
