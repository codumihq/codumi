package extract

// Leitor de tokens compartilhado pelos extratores de Kotlin/Java e Rust: pula
// comentários (inclusive aninhados), separa textos (com as formas brutas de
// cada linguagem) e marca a linha de cada token.

import (
	"regexp"
	"strings"

	"github.com/codumihq/codumi/internal/model"
)

type tok struct {
	k    byte // 'i' identificador, 's' texto, 'p' pontuação, 'n' número, 'a' anotação (@Nome)
	s    string
	line int
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}
func isIdent(c byte) bool { return isIdentStart(c) || c >= '0' && c <= '9' }

// lex quebra o código em tokens. rust liga as formas r"..", r#".."#, b"..",
// as vidas ('a) e os caracteres do Rust; sem ele, vale Kotlin/Java ("""..""").
func lex(src []byte, rust bool) []tok {
	var out []tok
	line := 1
	n := len(src)
	i := 0
	emit := func(k byte, s string, ln int) { out = append(out, tok{k, s, ln}) }
	for i < n {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r' || c == '\f':
			i++
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			depth := 0
			for i < n {
				if src[i] == '/' && i+1 < n && src[i+1] == '*' {
					depth++
					i += 2
					continue
				}
				if src[i] == '*' && i+1 < n && src[i+1] == '/' {
					depth--
					i += 2
					if depth == 0 {
						break
					}
					continue
				}
				if src[i] == '\n' {
					line++
				}
				i++
			}
		case rust && (c == 'r' || c == 'b') && rawStart(src, i):
			// r"..", r#".."#, br"..", b".."
			j := i
			if src[j] == 'b' {
				j++
			}
			raw := j < n && src[j] == 'r'
			if raw {
				j++
			}
			hashes := 0
			for j < n && src[j] == '#' {
				hashes++
				j++
			}
			j++ // aspas
			start, ln := j, line
			end := "\"" + strings.Repeat("#", hashes)
			for j < n && !(src[j] == '"' && strings.HasPrefix(string(src[j:min(n, j+len(end))]), end)) {
				if src[j] == '\\' && !raw {
					j++
				}
				if j < n && src[j] == '\n' {
					line++
				}
				j++
			}
			emit('s', string(src[start:min(j, n)]), ln)
			i = min(n, j+len(end))
		case c == '"':
			if !rust && i+2 < n && src[i+1] == '"' && src[i+2] == '"' {
				j, ln := i+3, line
				for j+2 < n && !(src[j] == '"' && src[j+1] == '"' && src[j+2] == '"') {
					if src[j] == '\n' {
						line++
					}
					j++
				}
				emit('s', string(src[i+3:min(j, n)]), ln)
				i = min(n, j+3)
				for i < n && src[i] == '"' { // """..."""" (aspas no fim)
					i++
				}
				continue
			}
			j, ln := i+1, line
			var b strings.Builder
			for j < n && src[j] != '"' {
				if src[j] == '\\' && j+1 < n {
					b.WriteByte(src[j+1])
					j += 2
					continue
				}
				if src[j] == '\n' {
					line++
					if !rust {
						break // texto sem fechar: não engole o arquivo
					}
				}
				b.WriteByte(src[j])
				j++
			}
			emit('s', b.String(), ln)
			i = j + 1
		case c == '\'':
			// caractere ('x', '\n') ou, no Rust, uma vida ('a)
			if i+2 < n && src[i+1] == '\\' {
				j := i + 2
				for j < n && src[j] != '\'' && src[j] != '\n' {
					j++
				}
				i = j + 1
			} else if i+2 < n && src[i+2] == '\'' {
				i += 3
			} else if i+1 < n && src[i+1] >= 0x80 {
				j := i + 1
				for j < n && j < i+6 && src[j] != '\'' {
					j++
				}
				i = j + 1
			} else {
				// vida do Rust ('a, 'static): vira um token próprio, não um nome
				j := i + 1
				for j < n && isIdent(src[j]) {
					j++
				}
				emit('n', string(src[i:j]), line)
				i = max(j, i+1)
			}
		case c == '@' && i+1 < n && isIdentStart(src[i+1]):
			j := i + 1
			for j < n && (isIdent(src[j]) || src[j] == '.') {
				j++
			}
			name := string(src[i+1 : j])
			if k := strings.LastIndexByte(name, '.'); k >= 0 {
				name = name[k+1:]
			}
			emit('a', name, line)
			i = j
		case c == '`':
			j := i + 1
			for j < n && src[j] != '`' && src[j] != '\n' {
				j++
			}
			emit('i', string(src[i+1:min(j, n)]), line)
			i = j + 1
		case isIdentStart(c):
			j := i
			for j < n && isIdent(src[j]) {
				j++
			}
			emit('i', string(src[i:j]), line)
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < n && (isIdent(src[j]) || src[j] == '.' && j+1 < n && src[j+1] >= '0' && src[j+1] <= '9') {
				j++
			}
			emit('n', string(src[i:j]), line)
			i = j
		case c == ':' && i+1 < n && src[i+1] == ':':
			emit('p', "::", line)
			i += 2
		case c == '-' && i+1 < n && src[i+1] == '>':
			emit('p', "->", line)
			i += 2
		case c == '=' && i+1 < n && src[i+1] == '>':
			emit('p', "=>", line)
			i += 2
		default:
			emit('p', string(c), line)
			i++
		}
	}
	return out
}

func rawStart(src []byte, i int) bool {
	j := i
	if src[j] == 'b' {
		j++
		if j < len(src) && src[j] == '"' {
			return i == 0 || !isIdent(src[i-1])
		}
	}
	if j >= len(src) || src[j] != 'r' {
		return false
	}
	j++
	for j < len(src) && src[j] == '#' {
		j++
	}
	return j < len(src) && src[j] == '"' && (i == 0 || !isIdent(src[i-1]))
}

var (
	apiPath  = regexp.MustCompile(`^(https?://[^/]+)?/(api|v\d+)(/|$)`)
	looksSQL = regexp.MustCompile(`(?is)^\s*(select|insert|update|delete|with|upsert|merge)\b`)
	sqlName  = regexp.MustCompile(`^[A-Za-z_][\w.]*$`)
	tableOps = map[string]bool{"select": true, "insert": true, "update": true, "upsert": true, "delete": true}
)

// apiRoute devolve o caminho de um texto que parece rota da própria API.
func apiRoute(s string) (string, bool) {
	m := apiPath.FindStringSubmatchIndex(s)
	if m == nil {
		return "", false
	}
	p := s[m[4]-1:] // a partir da barra antes de api/vN
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if strings.ContainsAny(p, " \n") {
		return "", false
	}
	return p, true
}

// sqlUse transforma um texto de SQL em usos de tabela.
func sqlUse(g *model.Graph, file string, line int, from, q, client string) {
	for _, t := range sqlTables(q) {
		if !sqlName.MatchString(t.name) {
			continue // nome montado em tempo de execução (#{x}_precos, :tabela)
		}
		g.DBUses = append(g.DBUses, model.DBUse{File: file, Line: line, From: from, Type: "table", Name: t.name, Ops: t.ops, Client: client})
	}
}
