<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="brand/logo-dark.svg">
    <img src="brand/logo-light.svg" alt="Codumi" width="320">
  </picture>
</p>

<details align="center">
  <summary><b>Leia em outros idiomas</b></summary>
  <br><a href="README.md">English</a>
</details>

<p align="center">
  <a href="https://github.com/codumihq/codumi/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/codumihq/codumi/actions/workflows/ci.yml/badge.svg"></a>
  <a href="LICENSE"><img alt="Licença FSL-1.1-ALv2" src="https://img.shields.io/badge/license-FSL--1.1--ALv2-0e9a63"></a>
  <img alt="Go 1.26" src="https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white">
  <img alt="Indexa TypeScript, Go, Dart, Python, Kotlin/Java, C#, PHP, Rust, Ruby, Elixir, Swift e SQL" src="https://img.shields.io/badge/indexa-TS%20%C2%B7%20Go%20%C2%B7%20Dart%20%C2%B7%20Python%20%C2%B7%20Kotlin%2FJava%20%C2%B7%20C%23%20%C2%B7%20PHP%20%C2%B7%20Rust%20%C2%B7%20Ruby%20%C2%B7%20Elixir%20%C2%B7%20Swift%20%C2%B7%20SQL-3b4a44">
  <img alt="MCP" src="https://img.shields.io/badge/MCP-Claude%20Code%20%C2%B7%20Codex%20%C2%B7%20Cursor%20%C2%B7%20Gemini-0e9a63">
  <img alt="App para macOS e Windows" src="https://img.shields.io/badge/app-macOS%20%C2%B7%20Windows-3b4a44?logo=apple&logoColor=white">
  <img alt="Feito no Brasil" src="https://img.shields.io/badge/feito%20no-Brasil-009c3b">
</p>

# Codumi

**O mapa exato do seu código para agentes de IA, do clique até o banco de dados.**

O Codumi indexa projetos TypeScript (React, Next.js, TanStack, Vue/Nuxt, Svelte, Astro), Go, Dart/Flutter, Python, Kotlin/Java/Scala, C#, PHP/Laravel, Rust, Ruby/Rails, Elixir/Phoenix, Swift e Supabase e responde às perguntas que um agente (ou você) faz o dia inteiro, em poucas linhas em vez de uma dúzia de arquivos lidos:

- **Quem usa isto?** Cada ponto de chamada, com a linha. Sem adivinhar.
- **O que esta rota toca?** Tabelas, RPCs e buckets, seguindo o grafo de chamadas real.
- **Quem alcança esta tabela?** RLS, policies, grants e cada lugar do código que a usa.
- **Onde estão os furos?** O `codumi audit` reproduz suas migrações e aponta o que o anônimo consegue alcançar.
- **O que a minha mudança afeta?** O `codumi impact` lê o diff do git e sobe até quem chama, as rotas, as telas, as tabelas e os testes no caminho.
- **O que é código morto?** O `codumi unused` lista o que ninguém chama, sem contar o que o framework chama sozinho.

## Por que a exatidão importa

Muitas ferramentas de grafo de código deduzem as ligações pelos nomes ou deixam um LLM preencher as lacunas. Isso serve para um passeio pelo código, mas não para refatorações nem revisões de segurança, em que um ponto de chamada esquecido é justamente o bug.

O Codumi usa o verificador de tipos do próprio projeto para resolver cada referência: o compilador do TypeScript para TS/JS, o `go/types` para Go e o `package:analyzer` para Dart. Python não tem verificador de tipos embutido, então ali o Codumi resolve os nomes de forma estática, pelos imports, `self.method`, `Class.method` e `x = Class(...)`; chamadas em objetos de tipo desconhecido não são seguidas. Kotlin/Java e Rust são lidos do mesmo jeito estático, sem Gradle nem o compilador do Rust: Kotlin/Java pelo pacote do arquivo, pelos imports (com alias e `*`), `Classe.membro` e `this.membro`; Rust pelos módulos (`lib.rs`, `a.rs`, `a/mod.rs`, `mod` no código), pelas árvores de `use` (alias, listas, `*`), caminhos `crate::`/`self::`/`super::`, outros crates do workspace, `Self::` e `self.metodo()`. Ele guarda **cada ocorrência numa linha própria**, então duas chamadas da mesma função são dois resultados, cada um com sua linha.

## Medido num app real

Rodamos 12 tarefas típicas de agente num app Next.js + Supabase em produção (2.300 arquivos, 403 migrações). Para cada tarefa, comparamos o que um agente lê sem o Codumi (a saída do grep mais os arquivos que ele abre) com a resposta do Codumi, contando 1 token a cada 4 bytes dos dois lados.

| Tarefa | Sem o Codumi | Com o Codumi | Economia |
|---|---:|---:|---:|
| Achar os usos de 5 funções compartilhadas (guarda de autenticação, limitador de taxa, um hook, um componente, um helper de sessão) | 42,2k tokens | 16,7k | 2,5× |
| Entender 3 tabelas: RLS, policies e cada uso no código | 40,6k | 9,3k | 4,4× (3,1×–7,4×) |
| Entender 3 rotas de API: o handler e tudo que elas tocam no banco | 16,9k | 1,8k | 9,5× |
| Revisão de segurança do schema (as 403 migrações) | 252k | 5,2k | 49× |
| **Tudo junto** | **352k** | **33k** | **~10,7×** |

- **No dia a dia** (usos, tabelas e rotas, sem a auditoria), o contexto do agente fica cerca de **3,6× menor**.
- **A auditoria** é onde a diferença é maior: sem o Codumi, só as migrações (252k tokens) não cabem numa leitura.
- **Velocidade:** as consultas respondem em 0,01–0,05 s, contra 0,13–0,26 s do grep. A auditoria completa leva 0,6 s.

Esses números são conservadores. O lado "sem" conta só a saída do grep e os arquivos que um agente cuidadoso abriria: nas rotas, apenas o arquivo da rota e os imports diretos. Sessões reais costumam ler mais. Nas tabelas, parte da diferença é ruído do grep, já que o nome de uma tabela comum também aparece em comentários e textos.

## Instalação

```sh
go install github.com/codumihq/codumi@latest
```

Para TypeScript, você precisa do Node.js e do pacote `typescript` do próprio projeto, que já está lá em qualquer projeto TS. Para Go, do toolchain do Go. Para Dart, do SDK do Dart ou do Flutter; no primeiro uso o Codumi compila um leitor nativo de Dart (cerca de 30 s, uma vez só) e guarda o cache de resumos do analyzer, então as próximas execuções são rápidas. Para Python, do `python3` (só a biblioteca padrão; nada do seu projeto é importado). Kotlin/Java/Scala, C#, PHP, Rust, Ruby, Elixir e Swift não pedem nada instalado: o Codumi lê o código sozinho.

## Uso

```sh
cd seu-projeto
codumi index            # primeira vez ~20 s num app Next.js de 2.300 arquivos; depois incremental (~0,1 s sem mudanças, ~3 s com poucos arquivos)
```

### `codumi refs <nome>`: cada uso, exatamente

```
$ codumi refs markPaid
markPaid  [function]  src/lib/orders.ts:16
2 uses in 2 files (call 2, import 2)
src/app/api/webhooks/payments/route.ts
  7      POST                         call
src/app/api/webhooks/stripe/route.ts
  11     POST                         call
```

Funciona com funções, componentes (`jsx`), tipos e `Class.member`. Você também pode indicar o arquivo: `codumi refs src/lib/orders.ts:markPaid`.

### `codumi route <caminho>`: o que um endpoint toca

```
$ codumi route /api/orders
POST /api/orders  src/app/api/orders/route.ts:3
database: 2 objects across 2 uses (--detail for sites and call paths)
  rpc    adjust_stock                                                  ×1
  table  orders                           insert,select                ×1

called from: 1 place
  src/components/OrderButton.tsx:5  OrderButton
```

Ele segue o handler pelo grafo de chamadas (5 níveis) até cada `.from()`, `.rpc()` e `.storage.from()`. Usos por um cliente com service role aparecem marcados com `[admin]`.

### `codumi page <caminho>`: do que uma tela é feita

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

Lê páginas do app router do Next.js (`app/**/page.tsx`, com os layouts) e do pages router. A árvore segue o JSX, deixa os componentes básicos do design system (`components/ui/…`) numa linha só e pula componentes que já mostrou. Também lista as tabelas, RPCs e rotas de API que os componentes usam.

### `codumi find "<texto>"`: onde mora um texto da interface

```
$ codumi find "fresh coffee"
"fresh coffee": 1 match in 1 file
src/app/page.tsx
  6     Hero                     <h1>Fresh coffee, delivered</h1>
```

A busca não diferencia maiúsculas e roda só nos arquivos-fonte indexados do projeto, nunca no `node_modules` nem no resultado do build. Cada ocorrência vem com o componente ou a função em que está.

### Entre repositórios: `codumi link`

Tem uma API em Go e um app TS que a chama? Indexe os dois e ligue um ao outro. O `codumi route` passa a mostrar quem chama a rota no outro repositório, casando pelo caminho *e* pelo método HTTP:

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

No lado Go, o Codumi lê rotas de `net/http` (padrões do 1.22), gorilla/mux, chi, gin e echo. Ele acha o handler de verdade dentro de middlewares (`s.admin(s.deleteStore)`) e tira as tabelas do SQL passado para `Query`/`Exec`. No lado TS, registra chamadas a caminhos de API: `fetch`, axios ou o seu próprio wrapper (`call('/v1/x/${id}', { method: 'PATCH' })`). Dart funciona do mesmo jeito: `http`, dio ou wrappers como `Api.getChecked('/api/orders/$id')`. Assim, uma rota do Next.js mostra quem a chama no app web *e* no app Flutter:

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

Em Dart, as chamadas ao Supabase (`.from()`, `.rpc()`, `.storage.from()`) são rastreadas como em TS. Em Python, o Codumi lê rotas do FastAPI e do Flask (decorators, com `APIRouter(prefix=…)`), supabase-py (`.table()`, `.rpc()`, `.storage`), SQL em `execute()`/`fetch()` e chamadas de API com `requests`/`httpx`. Em Kotlin/Java: rotas do Spring (`@GetMapping`, `@RequestMapping` na classe) e do Ktor (`route { get("/x") }`), interfaces do Retrofit (`@GET("api/…")`) como chamadas de API, supabase-kt (`from()`, `rpc()`, `storage.from()`) e SQL em textos e no `@Query` do Room. Em Rust: rotas do axum (`.route("/x", get(handler))`) e do actix/rocket (`#[get("/x")]`), chamadas `/api/…` e SQL em textos (sqlx). Em C#: controllers do ASP.NET (`[Route("api/[controller]")]`, `[HttpGet("{id}")]`) e minimal APIs (`app.MapGet`), Entity Framework (`DbSet<T>`, `[Table]`) como uso de tabela e SQL em textos. Em PHP: rotas do Laravel (`Route::get`, grupos com prefix, `apiResource`, `routes/api.php` sob `/api`), modelos Eloquent (`Pedido::where(...)->update()` é escrita em `pedidos`) e `DB::table()`. No lado do TypeScript, o Codumi também lê TanStack Router/Start (`createFileRoute`, `createServerFileRoute`), React Router (`<Route>` e objetos de rota), SvelteKit (`+page.svelte`, `+server.ts`) e Astro (`src/pages`), além dos componentes `.vue`, `.svelte` e `.astro` (imports, `<Componente>` e `<kebab-case>` no template, chamadas em `@click`/`:prop`). Vue: os objetos de rota do Vue Router (inclusive `() => import()`) viram telas; Nuxt: telas de `pages/`, rotas de `server/api` (`x.get.ts`) e componentes/composables usados sem import. Em Ruby on Rails: `config/routes.rb` (`resources`, `resource`, `namespace`, `scope`, `member`/`collection`, `get "x", to: "c#a"`), modelos ActiveRecord (`Pedido.where(...).update_all` é escrita em `pedidos`, `self.table_name`), callbacks (`before_action :x`) e constantes resolvidas pelo aninhamento léxico. Em Elixir/Phoenix: o router (`scope`, `resources`, `live`, com as telas LiveView), `alias`/`import` e o Ecto (`schema "pedidos"`, `Repo.all/insert/update/delete`, `from(p in Pedido)`). Em Swift: tipos, extensions e métodos do módulo inteiro, views SwiftUI como componentes, rotas do Vapor (`app.get("x", use: h)`, `grouped`) e supabase-swift. Em Scala: o leitor de Kotlin/Java com `def`, `trait` e os imports do Scala, mais o `conf/routes` do Play. ORMs por cima de qualquer um deles: Prisma (`prisma.pedido.findMany`, `@@map`), Drizzle (`pgTable`, `db.select().from(pedidos)`), Django (`Pedido.objects.filter(...).update()`, `db_table`) e SQLAlchemy (`__tablename__`, `session.query(Pedido)`).

### `codumi table <nome>`: uma tabela, view, função ou bucket

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

Para uma função, mostra a assinatura completa e quem a chama **dentro do banco** também: outras funções, os triggers que a disparam e as policies.

```
$ codumi table adjust_stock
function public.adjust_stock(p_product uuid, p_delta int) → void  (SECURITY DEFINER (bypasses RLS))  supabase/migrations/001_init.sql:44
execute: anon,authenticated,public,service_role
database: 1 use (functions, triggers and policies that call it)
  function public.tg_sale()  supabase/migrations/004_sales.sql:12  ← trigger trg_sale on public.sales
code: 1 use in 1 file
  src/lib/orders.ts:13  reserveStock
```

### `codumi impact`: o que uma mudança afeta

Rode antes de dar uma tarefa por terminada (os agentes têm o `codumi_impact`). Ele lê o `git diff` (o que não foi commitado, ou `--base=main` para um branch), acha as declarações cujas linhas mudaram e sobe pelo grafo de chamadas: quem chama, as rotas e telas que chegam ali, o que o código mudado toca no banco, as rotas da API que ele chama e os testes no caminho.

### `codumi unused`: código morto

Declarações sem nenhum uso de verdade (import não conta). Telas, handlers de rota, `main`, testes e métodos ficam de fora, porque o framework e as interfaces chamam. As internas dá para apagar; as exportadas podem ser usadas por outro projeto.

### `codumi audit`: furos de controle de acesso num projeto Supabase

O Codumi reproduz `supabase/migrations/*.sql` em ordem, guardando o estado final das tabelas, RLS, policies, funções, grants e buckets. Depois, verifica:

| Regra | Gravidade | O que significa |
|---|---|---|
| `rls-disabled` | alta | Uma tabela sem RLS pode ser lida e alterada por qualquer um que tenha a chave anon. |
| `security-definer-anon` | alta / baixa | Uma função SECURITY DEFINER que o anônimo consegue chamar (o Postgres dá EXECUTE a PUBLIC por padrão). É alta se o corpo nunca confere quem chama, e baixa se usa `auth.uid()`, direto ou por um helper. |
| `storage-listable` | alta / média | Uma policy de SELECT no storage que só confere o `bucket_id`, então qualquer um (ou qualquer usuário logado) consegue **listar** todos os arquivos. |
| `view-security-definer` | média | Uma view que roda como a dona e ignora o RLS das tabelas que lê. |
| `webhook-unverified` | média (heurística) | Uma rota de webhook sem conferência de assinatura ou segredo e que nunca busca o evento de novo no provedor. O Codumi segue o grafo de chamadas, então `verifySignature()` ou `getPayment()` num helper contam. |
| `route-no-auth` | média (heurística) | Rota que grava no banco (direto ou pelo que ela chama) sem nenhum login antes: sem `before_action :authenticate…` (Rails, inclusive concerns e `skip_before_action`), sem middleware `auth` (Laravel), sem plug de login no `pipe_through` (Phoenix), sem `[Authorize]` (ASP.NET), sem `@PreAuthorize` (Spring), sem `Depends(current_user)` ou `@login_required` (FastAPI, Flask). Caminhos de login, cadastro, senha e webhook ficam de fora. |
| `service-role-public` / `service-role-client` | alta | A chave service role é lida de uma variável `NEXT_PUBLIC_*` ou citada num componente do cliente. |
| `policy-open-write` / `policy-open-read` | alta / baixa | Uma policy que é `true` para o anônimo. |
| `bucket-public` | baixa | Os arquivos podem ser lidos por qualquer um que tenha a URL. |

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

Use `--fail-on=high` no CI para o build falhar quando aparecer um achado dessa gravidade ou acima.

### `codumi note <módulo>`: o que o código não conta

Algumas coisas nunca estão no código: por que uma escolha foi feita, o cache que precisa ser limpo depois de trocar uma credencial, o que ainda está pendente. As fichas de módulo guardam isso num cartão curto em Markdown por funcionalidade (`.codumi/notes/<slug>.md`), versionado junto com o código. Quando mostra uma ficha, o Codumi acrescenta uma parte viva tirada do índice, então os fatos dessa parte nunca ficam desatualizados:

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

`codumi note --file=src/lib/orders.ts` devolve as fichas que cobrem um arquivo. Os agentes chamam isso (como `codumi_note`, pelo MCP) antes de editar. `codumi notes` lista todas as fichas e marca as que apontam para arquivos que não existem mais.

### `codumi sql "<consulta>"`

O índice é um arquivo SQLite comum. Os agentes podem consultá-lo direto:

```sh
codumi sql "select name, count(*) from db_uses where type='table' group by name order by 2 desc limit 10"
```

As tabelas são `files`, `decls`, `refs`, `db_uses`, `routes`, `route_calls`, `sql_tables`, `sql_policies`, `sql_functions` e `buckets`.

Os comandos também têm apelidos em português: `codumi usa`, `codumi tabela`, `codumi rota`, `codumi auditoria`, `codumi impacto` e `codumi morto`.

### `codumi erro`: um erro de produção, seguido até o código

Cole o erro com a pilha (`codumi erro < erro.txt`) ou passe um problema do Sentry (`--sentry=<link>`, com `SENTRY_AUTH_TOKEN`). Cada linha da pilha cai na declaração onde está, e dali sobe até as rotas da API e as telas que chegam nela, o que o código que falhou toca no banco e os testes no caminho. Os agentes usam como `codumi_error`.

### `codumi pr`: a revisão do pull request

`codumi pr --base=main` escreve um comentário em Markdown com o que a mudança alcança e os achados da auditoria nos arquivos mudados; `--fail-on=high` reprova a verificação. A Action do GitHub deste repositório comenta em cada pull request:

```yaml
- uses: actions/checkout@v4
  with: { fetch-depth: 0 }
- uses: codumihq/codumi@main
  with: { fail-on: high, lang: pt }
```

### `codumi banco`: o banco de verdade × as migrações

`codumi banco --db=postgresql://… --rls` compara o banco no ar com as migrações (tabelas criadas no painel, migrações não aplicadas, RLS e SECURITY DEFINER diferentes) e confere o que `anon` e um usuário logado qualquer conseguem ler e gravar. Transação só leitura, 3 s por consulta, sem contar linhas.

## Com agentes de IA

**MCP** (Claude Code, Cursor, Codex e outros):

```sh
claude mcp add codumi -- codumi mcp
```

O servidor expõe `codumi_refs`, `codumi_route`, `codumi_page`, `codumi_find`, `codumi_table`, `codumi_audit`, `codumi_note`, `codumi_sql` e `codumi_index`. Eles devolvem o mesmo texto compacto da linha de comando, e o índice é criado no primeiro uso.

**Sempre atualizado:** `codumi hook install` reindexa em segundo plano depois de cada commit, checkout, merge e rebase. Ele mantém os hooks que você já tem, e `codumi hook uninstall` tira só o bloco dele.

**Sem MCP**, coloque isto no seu `CLAUDE.md`, `AGENTS.md` ou `.cursorrules`:

```md
Este repositório tem um índice do Codumi. Antes de ler arquivos para achar usos, rode:
- `codumi refs <nome>` para cada ponto de chamada de uma função, componente ou tipo
- `codumi route <caminho>` para o que uma rota de API toca no banco
- `codumi table <nome>` para RLS, policies e usos no código de uma tabela, RPC ou bucket
Rode `codumi index` depois de um pull ou de mudanças grandes.
```

Todo comando aceita `--json`.

## Experimente

`examples/demo` é um app Next.js + Supabase pequeno, com furos de propósito:

```sh
cd examples/demo && npm i && codumi index && codumi audit
```

## Situação e próximos passos

Cobre TypeScript/JavaScript (Next.js, React Router, TanStack, Vue/Nuxt, SvelteKit, Astro), Go, Dart/Flutter, Python, Kotlin/Java/Scala, C#, PHP/Laravel, Rust, Ruby/Rails, Elixir/Phoenix, Swift, Prisma/Drizzle/Django/SQLAlchemy e migrações SQL do Supabase.

- [x] Servidor MCP (`codumi mcp`)
- [x] Reindexação em segundo plano a cada commit (`codumi hook install`)
- [x] Go
- [x] Ligação entre repositórios (uma API em Go ↔ o cliente TS que a chama)
- [x] Dart / Flutter
- [x] Python (resolução estática)
- [x] Kotlin/Java, C#, PHP/Laravel, Rust (resolução estática)
- [x] Svelte, Astro, TanStack, React Router
- [x] Vue/Nuxt, Ruby/Rails, Elixir/Phoenix, Swift, Scala
- [x] Prisma, Drizzle, Django, SQLAlchemy
- [x] Indexação incremental: só os arquivos que mudaram, mais os que usavam algo renomeado ou removido (`--full` refaz tudo)
- [x] Fichas de módulo: cartões curtos escritos por pessoas, por funcionalidade, com uma parte viva tirada do índice

Limites que vale conhecer:

- O lado SQL reproduz migrações; não é um parser completo de Postgres. SQL dinâmico (`EXECUTE` em blocos `DO`) é relatado, mas não seguido.
- Funções sobrecarregadas são rastreadas pelo nome.
- `webhook-unverified` é uma heurística. Endpoints de configuração de webhook (`setWebhook`…) e rotas atrás de middleware de autenticação são ignorados.
- `route-no-auth` é heurística e só roda onde o Codumi enxerga as guardas (os frameworks acima). Um arquivo de handler que confere o usuário por conta própria (`current_user`, `authorize`, `request.user`…) fica de fora.
- Grupos de rotas em Go (`Route` do chi, `Group` do gin) ainda não somam o prefixo.

Trate a auditoria como uma lista para revisar, não como um veredito.

## Segurança

- **Tudo local.** O índice é um arquivo SQLite em `.codumi/`, dentro do seu projeto. O servidor MCP conversa por stdio com o agente que o iniciou; nada escuta na rede e nada é enviado para lugar nenhum. O registro de uso (`codumi stats`) fica na sua máquina.
- **Consultas só de leitura.** `codumi sql` e a ferramenta `codumi_sql` abrem o índice em modo leitura e aceitam uma instrução `SELECT`, `WITH`, `EXPLAIN` ou `VALUES`; `ATTACH`, `PRAGMA`, `VACUUM` e escritas são recusados, então um agente enganado por prompt injection não consegue usá-la para ler outros bancos do seu disco.
- **Indexar é confiar no projeto, como num `npm install`.** Para resolver TypeScript com exatidão, o Codumi carrega o pacote `typescript` do `node_modules` do próprio projeto. Indexe projetos que você também instalaria. Go, Dart, Python, Kotlin/Java, C#, PHP, Rust, Svelte e Astro são lidos sem rodar código do projeto.
- **O app** mostra tudo que vem do modelo e dos seus arquivos como texto, nunca como HTML, e os links abrem no seu navegador, nunca dentro do app. Comandos e edições do Claude Code esperam o seu OK, a menos que você escolha outro modo de permissão.

Achou um problema? Abra um aviso de segurança privado no GitHub (security advisory) em vez de uma issue pública.

## Licença

O motor do Codumi (este repositório: o indexador, o CLI, o servidor MCP e a Action do GitHub) está sob a [Functional Source License, versão 1.1, com licença futura Apache 2.0](LICENSE) (FSL-1.1-ALv2). Você pode ler, rodar, mudar e usar no seu trabalho e na sua empresa; o que ela não permite é oferecer o Codumi como produto comercial concorrente. Cada versão vira Apache 2.0 dois anos depois de lançada.

O app do Codumi para macOS e Windows, e o app do celular, são grátis e de código fechado: [baixe em codumi.dev](https://codumi.dev).
