package cli

// Ask responde perguntas de localização direto do mapa, sem agente e sem
// tokens: "onde fica a tela de inscrições?", "quem usa formatarPreco?",
// "o que a rota /api/pedidos toca?", "quem mexe na tabela pagamentos?".
// Quando a pergunta não é desse tipo, ou o mapa não acha nada, devolve ok =
// false e a pergunta segue para o agente.

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

type AskAnswer struct {
	OK      bool   `json:"ok"`
	Kind    string `json:"kind"`    // page, refs, route, table, find, audit
	Query   string `json:"query"`   // o comando que respondeu, ex.: page inscricoes
	Output  string `json:"output"`  // a resposta do mapa
	Clipped int    `json:"clipped"` // linhas cortadas
}

var (
	reAskRoute = regexp.MustCompile(`(?:^|\s)(/[\w\-./:{}\[\]$]+)`)
	reAskQuote = regexp.MustCompile("[\"'`“”‘’]([^\"'`“”‘’]{2,60})[\"'`“”‘’]")
	reAskIdent = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)\b`)

	reIntRefs  = regexp.MustCompile(`\b(quem (usa|chama|importa)|onde (e|esta|eh) (usad|chamad)|onde usa|usos? d[eoa]|chamadas? d[eoa]|who (uses|calls)|where is .* (used|called)|callers? of|usages? of)`)
	reIntTable = regexp.MustCompile(`\b(tabelas?|tables?|bucket|rpc)\b`)
	reIntRoute = regexp.MustCompile(`\b(rotas?|endpoints?|routes?|api)\b`)
	reIntPage  = regexp.MustCompile(`\b(telas?|paginas?|pages?|screens?)\b`)
	reIntWhere = regexp.MustCompile(`\b(onde (fica|esta|estao|ficam|e feit|e definid|e declarad|mudo|altero|edito|troco|mexo)|em que arquivo|qual arquivo|qual o arquivo|where (is|are|do i|can i)|which file)`)
	reIntAudit = regexp.MustCompile(`\b(auditoria|audit|falhas? de seguranca|brechas?|security holes?)\b`)
	reIntWork  = regexp.MustCompile(`\b(crie|criar|cria|faca|fazer|faz|implemente|implementar|corrija|corrigir|conserte|consertar|mude|mudar|altere|alterar|adicione|adicionar|remova|remover|refatore|explique|explica|por que|porque|why|how|como (funciona|faco|fazer)|create|add|fix|change|remove|refactor|explain|write|build)\b`)
)

var askStop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a o as os um uma uns umas de do da dos das d e é eh em no na nos nas num numa por pra para pelo pela com sem que qual quais quem onde
		fica ficam esta estao está estão tela telas pagina paginas página páginas rota rotas tabela tabelas endpoint usa usam chama chamam usado usada usados usadas chamado chamada
		mexe mexem toca tocam faz fazem salva salvam grava gravam le leem lê muda mudam aparece aparecem mostra mostram
		meu minha isso isto esse essa este esta aquele aquela ai aí aqui codigo código arquivo arquivos projeto app sistema funcao função componente classe metodo método
		feito feita definido definida declarado declarada mudo altero edito troco mexo me mim eu voce você se tem ter ha há
		the a an of to in on at for from with is are where which who what screen screens page pages route routes table tables used uses call calls file files component function do does i can`) {
		askStop[w] = true
	}
}

func fold(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, _ := transform.String(t, strings.ToLower(s))
	return out
}

// askTerms devolve os candidatos a nome procurado, do mais específico ao mais vago.
func askTerms(q string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.Trim(strings.TrimSpace(s), "?.,!:;")
		if len(s) >= 2 && !seen[strings.ToLower(s)] {
			seen[strings.ToLower(s)] = true
			out = append(out, s)
		}
	}
	for _, m := range reAskQuote.FindAllStringSubmatch(q, -1) {
		add(m[1])
	}
	// nomes de código: camelCase, PascalCase, snake_case, Classe.metodo
	for _, m := range reAskIdent.FindAllStringSubmatch(q, -1) {
		w := m[1]
		if strings.ContainsAny(w, "_.") || hasInnerUpper(w) {
			add(w)
		}
	}
	// o resto: palavras que não são de ligação
	var words []string
	for _, w := range strings.FieldsFunc(q, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' }) {
		if !askStop[strings.ToLower(w)] && !askStop[fold(w)] && len([]rune(w)) >= 3 {
			words = append(words, w)
		}
	}
	if len(words) > 1 {
		add(strings.Join(words, " "))
	}
	for _, w := range words {
		add(w)
	}
	// sem acento também: "inscrições" acha Inscricoes
	n := len(out)
	for _, t := range out[:n] {
		add(fold(t))
	}
	return out
}

func hasInnerUpper(w string) bool {
	for i, r := range w {
		if i > 0 && unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

var reNotFound = regexp.MustCompile(`(?i)^codumi: no |appears in no indexed|no declaration named|no page or screen|no route matches|no table, view`)

// Ask tenta responder a pergunta só com o mapa do projeto em root.
func Ask(root, question string) AskAnswer {
	q := strings.TrimSpace(question)
	forced := strings.HasPrefix(q, "?")
	q = strings.TrimSpace(strings.TrimPrefix(q, "?"))
	f := fold(q)
	if q == "" || len(q) > 240 || strings.Contains(q, "\n") {
		return AskAnswer{}
	}
	// pedido de trabalho ("crie", "corrija", "explique") é do agente
	if !forced && reIntWork.MatchString(f) {
		return AskAnswer{}
	}
	try := func(kind string, args ...string) (AskAnswer, bool) {
		o, notes, code := Run(root, args...)
		o = strings.TrimSpace(o)
		if code != 0 || o == "" || reNotFound.MatchString(o) || reNotFound.MatchString(strings.TrimSpace(notes)) && o == "" {
			return AskAnswer{}, false
		}
		lines := strings.Split(o, "\n")
		clipped := 0
		if len(lines) > 40 {
			clipped = len(lines) - 40
			lines = lines[:40]
		}
		return AskAnswer{OK: true, Kind: kind, Query: strings.Join(args, " "), Output: strings.Join(lines, "\n"), Clipped: clipped}, true
	}
	terms := askTerms(q)
	if reIntAudit.MatchString(f) && len(f) < 80 {
		if a, ok := try("audit", "audit"); ok {
			return a
		}
	}
	if m := reAskRoute.FindStringSubmatch(q); m != nil && (reIntRoute.MatchString(f) || forced || strings.HasPrefix(m[1], "/api")) {
		if a, ok := try("route", "route", m[1]); ok {
			return a
		}
		return AskAnswer{}
	}
	intent := ""
	switch {
	case reIntRefs.MatchString(f):
		intent = "refs"
	case reIntTable.MatchString(f):
		intent = "table"
	case reIntPage.MatchString(f):
		intent = "page"
	case reIntWhere.MatchString(f):
		intent = "where"
	case forced:
		intent = "where"
	default:
		return AskAnswer{}
	}
	for _, t := range terms {
		switch intent {
		case "refs":
			if a, ok := try("refs", "refs", t); ok {
				return a
			}
		case "table":
			if a, ok := try("table", "table", t); ok {
				return a
			}
		case "page":
			if a, ok := try("page", "page", t); ok {
				return a
			}
		case "where":
			if strings.Contains(t, " ") {
				if a, ok := try("page", "page", t); ok {
					return a
				}
				continue
			}
			if a, ok := try("refs", "refs", t); ok {
				return a
			}
			if a, ok := try("page", "page", t); ok {
				return a
			}
			if a, ok := try("table", "table", t); ok {
				return a
			}
		}
	}
	// último recurso para "onde fica": o texto como aparece no código
	if intent == "where" || intent == "page" {
		for _, t := range terms {
			if a, ok := try("find", "find", t, "--limit=15"); ok {
				return a
			}
		}
	}
	return AskAnswer{}
}
