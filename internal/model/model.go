// Package model holds the in-memory project graph shared by the extractors,
// the store and the audit.
package model

type File struct {
	Path  string
	Lines int
}

// Decl is a top-level declaration (or Top.member).
type Decl struct {
	File, Name, Kind string
	Line             int
	Exported         bool
}

// Ref is one exact use of a declaration. Every occurrence is its own row.
type Ref struct {
	File   string
	Line   int
	From   string // enclosing top-level declaration, or <module>
	ToFile string
	To     string
	Kind   string // call, import, export, jsx, type, new, ref
}

// DBUse is a Supabase table, RPC or storage bucket used from code.
type DBUse struct {
	File   string
	Line   int
	From   string
	Type   string // table, rpc, bucket
	Name   string
	Ops    []string
	Client string // receiver expression, e.g. supabaseAdmin()
}

type Route struct {
	File, Method, Path string // File/Line: where the route is declared
	Line               int
	HandlerFile        string // the function that handles it
	Handler            string
	Guards             string // middleware wrapping the handler, comma-separated (Go)
}

type RouteCall struct {
	File   string
	Line   int
	From   string
	Path   string
	Method string // empty when unknown
}

type Table struct {
	Schema, Name string
	View         bool // a view: RLS doesn't apply, it runs as its owner unless security_invoker
	Invoker      bool // view created with security_invoker = true
	RLS          bool
	AnonGrant    bool // anon can reach it through PostgREST grants
	Src          string
}

type Policy struct {
	Schema, Table, Name string
	Cmd                 string // all, select, insert, update, delete
	Roles               []string
	Permissive          bool
	Using, Check        string
	Src                 string
}

type Function struct {
	Schema, Name string
	Args         string // parameter list as written, e.g. "p_product uuid, p_delta numeric"
	Returns      string
	SecDef       bool
	CallerScoped bool     // body checks the caller (auth.uid(), auth.jwt()…)
	Exec         []string // roles that may execute it
	Src          string
}

type Bucket struct {
	ID     string
	Public bool
	Src    string
}

// Graph is everything Codumi knows about a project.
type Graph struct {
	Root       string
	Files      []File
	Decls      []Decl
	Refs       []Ref
	DBUses     []DBUse
	Routes     []Route
	Pages      []Page
	RouteCalls []RouteCall
	Tables     []Table
	Policies   []Policy
	Functions  []Function
	Buckets    []Bucket
	SQLRefs    []SQLRef
	Warnings   []string
}

// SQLRef is a use of a function from inside the database: another
// function's body, a trigger, or a policy expression.
type SQLRef struct {
	Kind string // function, trigger, policy
	Name string // who uses it, e.g. "public.tg_stock()" or "trg_x on public.orders"
	To   string // function name
	Src  string
}

// Page is a UI page (Next.js app or pages router) and its main component.
type Page struct {
	File, Path, Component string
	Line                  int
}
