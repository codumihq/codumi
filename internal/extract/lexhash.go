package extract

// Leitor de tokens das linguagens de comentário com # (Ruby, Python e
// Elixir). Textos em aspas simples, duplas e triplas, heredocs do Elixir,
// interpolação #{…} pulada, símbolos (:nome) como token 'y', variáveis de
// instância do Ruby (@x) como 'v' e nomes com ? e ! no fim (exists?, save!).

import "strings"

const (
	hashRuby   = 'r'
	hashPython = 'p'
	hashElixir = 'e'
)

func lexHash(src []byte, mode byte) []tok {
	var out []tok
	line := 1
	n := len(src)
	emit := func(k byte, s string, ln int) { out = append(out, tok{k, s, ln}) }
	atLineStart := func(i int) bool { return i == 0 || src[i-1] == '\n' }
	// readStr lê um texto que abre em i com a aspa q (ou três aspas, se triple)
	readStr := func(i int, q byte, triple, interp bool) (string, int) {
		var b strings.Builder
		j := i + 1
		if triple {
			j = i + 3
		}
		for j < n {
			c := src[j]
			if triple && c == q && j+2 < n && src[j+1] == q && src[j+2] == q {
				return b.String(), j + 3
			}
			if !triple && c == q {
				return b.String(), j + 1
			}
			if c == '\\' && j+1 < n {
				if src[j+1] == '\n' {
					line++
				}
				b.WriteByte(src[j+1])
				j += 2
				continue
			}
			if interp && c == '#' && j+1 < n && src[j+1] == '{' {
				d := 0
				for ; j < n; j++ {
					if src[j] == '{' {
						d++
					} else if src[j] == '}' {
						d--
						if d == 0 {
							break
						}
					} else if src[j] == '\n' {
						line++
					}
				}
				b.WriteString("${}")
				j++
				continue
			}
			if c == '\n' {
				if !triple && mode == hashPython {
					return b.String(), j // texto sem fechar: não engole o arquivo
				}
				line++
			}
			b.WriteByte(c)
			j++
		}
		return b.String(), j
	}
	for i := 0; i < n; {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r' || c == '\f':
			i++
		case c == '\\' && i+1 < n && src[i+1] == '\n':
			line++
			i += 2
		case c == '#' && !(mode == hashElixir && i+1 < n && src[i+1] == '{'):
			for i < n && src[i] != '\n' {
				i++
			}
		case mode == hashRuby && c == '=' && atLineStart(i) && strings.HasPrefix(string(src[i:min(n, i+6)]), "=begin"):
			for i < n && !(atLineStart(i) && strings.HasPrefix(string(src[i:min(n, i+4)]), "=end")) {
				if src[i] == '\n' {
					line++
				}
				i++
			}
			for i < n && src[i] != '\n' {
				i++
			}
		case mode == hashRuby && c == '<' && i+2 < n && src[i+1] == '<' && (src[i+2] == '~' || src[i+2] == '-') && i+3 < n && (isIdentStart(src[i+3]) || src[i+3] == '\'' || src[i+3] == '"'):
			// heredoc <<~SQL … SQL: o texto vira um token só
			j := i + 3
			quoted := src[j] == '\'' || src[j] == '"'
			if quoted {
				j++
			}
			k := j
			for k < n && isIdent(src[k]) {
				k++
			}
			tag := string(src[j:k])
			if quoted {
				k++
			}
			// o corpo começa na próxima linha; o resto desta linha continua sendo código
			rest := k
			for rest < n && src[rest] != '\n' {
				rest++
			}
			restLine := line
			body := rest + 1
			var b strings.Builder
			end := body
			lines := 0
			for end < n {
				le := end
				for le < n && src[le] != '\n' {
					le++
				}
				lines++
				if strings.TrimSpace(string(src[end:le])) == tag {
					end = le
					break
				}
				b.Write(src[end:le])
				b.WriteByte('\n')
				end = le + 1
			}
			emit('s', b.String(), restLine)
			// tokens do resto da linha (ex.: <<~SQL.squish)
			sub := lexHash(src[k:rest], mode)
			for _, s := range sub {
				s.line = restLine
				out = append(out, s)
			}
			line += lines
			i = min(n, end+1)
			line++
		case c == '"' || c == '\'':
			triple := (mode != hashRuby) && i+2 < n && src[i+1] == c && src[i+2] == c
			if mode == hashElixir && c == '\'' {
				triple = false
			}
			ln := line
			s, j := readStr(i, c, triple, c == '"' && mode != hashPython)
			emit('s', s, ln)
			i = j
		case mode == hashElixir && c == '~' && i+2 < n && isIdentStart(src[i+1]) && strings.IndexByte("/|\"'([{<", src[i+2]) >= 0:
			// sigilos ~s(…), ~r/…/, ~H"""…"""
			open := src[i+2]
			closeCh := map[byte]byte{'(': ')', '[': ']', '{': '}', '<': '>'}[open]
			if closeCh == 0 {
				closeCh = open
			}
			ln := line
			if (open == '"' || open == '\'') && i+4 < n && src[i+3] == open && src[i+4] == open {
				s, j := readStr(i+2, open, true, false)
				emit('s', s, ln)
				i = j
				continue
			}
			j := i + 3
			for j < n && src[j] != closeCh {
				if src[j] == '\\' {
					j++
				} else if src[j] == '\n' {
					line++
				}
				j++
			}
			emit('s', string(src[i+3:min(j, n)]), ln)
			i = j + 1
			for i < n && isIdent(src[i]) { // modificadores do sigilo
				i++
			}
		case c == ':' && i+1 < n && src[i+1] == ':':
			emit('p', "::", line)
			i += 2
		case c == ':' && mode != hashPython && i+1 < n && (isIdentStart(src[i+1]) || src[i+1] == '"') && (i == 0 || !isIdent(src[i-1]) && src[i-1] != ':'):
			// símbolo :nome (ou :"nome")
			if src[i+1] == '"' {
				ln := line
				s, j := readStr(i+1, '"', false, false)
				emit('y', s, ln)
				i = j
				continue
			}
			j := i + 1
			for j < n && isIdent(src[j]) {
				j++
			}
			if j < n && (src[j] == '?' || src[j] == '!') {
				j++
			}
			emit('y', string(src[i+1:j]), line)
			i = j
		case c == '@' && i+1 < n && (isIdentStart(src[i+1]) || src[i+1] == '@'):
			j := i + 1
			for j < n && (src[j] == '@' || isIdent(src[j]) || mode == hashPython && src[j] == '.') {
				j++
			}
			name := string(src[i+1 : j])
			if mode == hashRuby {
				emit('v', "@"+strings.TrimLeft(name, "@"), line)
			} else {
				if k := strings.LastIndexByte(name, '.'); k >= 0 {
					name = name[k+1:]
				}
				emit('a', name, line)
			}
			i = j
		case isIdentStart(c) && c != '$':
			j := i
			for j < n && isIdent(src[j]) && src[j] != '$' {
				j++
			}
			if mode != hashPython && j < n && (src[j] == '?' || src[j] == '!') && !(j+1 < n && (src[j+1] == '=' || src[j+1] == ':' && mode == hashRuby)) {
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
		case c == '-' && i+1 < n && src[i+1] == '>':
			emit('p', "->", line)
			i += 2
		case c == '=' && i+1 < n && src[i+1] == '>':
			emit('p', "=>", line)
			i += 2
		case c == '|' && i+1 < n && src[i+1] == '>':
			emit('p', "|>", line)
			i += 2
		case c == '&' && i+1 < n && src[i+1] == '.':
			emit('p', ".", line) // user&.name
			i += 2
		default:
			emit('p', string(c), line)
			i++
		}
	}
	return out
}

// snake: PedidoItem → pedido_item.
func snake(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			if i > 0 && (s[i-1] >= 'a' && s[i-1] <= 'z' || s[i-1] >= '0' && s[i-1] <= '9' || i+1 < len(s) && s[i+1] >= 'a' && s[i+1] <= 'z' && s[i-1] != '_') {
				b.WriteByte('_')
			}
			b.WriteByte(c + 32)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// camel: pedido_item → PedidoItem.
func camel(s string) string {
	var b strings.Builder
	up := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || c == '-' || c == '/' {
			up = true
			continue
		}
		if up && c >= 'a' && c <= 'z' {
			c -= 32
		}
		up = false
		b.WriteByte(c)
	}
	return b.String()
}

var irregular = map[string]string{"person": "people", "man": "men", "woman": "women", "child": "children", "mouse": "mice", "ox": "oxen", "foot": "feet", "tooth": "teeth", "goose": "geese"}

// plural do inglês, como o Rails faz para o nome da tabela.
func plural(s string) string {
	i := strings.LastIndexByte(s, '_') + 1
	base, last := s[:i], s[i:]
	if p, ok := irregular[last]; ok {
		return base + p
	}
	switch {
	case strings.HasSuffix(last, "s") || strings.HasSuffix(last, "x") || strings.HasSuffix(last, "z") || strings.HasSuffix(last, "ch") || strings.HasSuffix(last, "sh"):
		return s + "es"
	case strings.HasSuffix(last, "y") && len(last) > 1 && !strings.ContainsRune("aeiou", rune(last[len(last)-2])):
		return s[:len(s)-1] + "ies"
	}
	return s + "s"
}

// singular, para achar o controller de resources :pedidos.
func singular(s string) string {
	for k, v := range irregular {
		if strings.HasSuffix(s, v) {
			return strings.TrimSuffix(s, v) + k
		}
	}
	switch {
	case strings.HasSuffix(s, "ies"):
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "ses") || strings.HasSuffix(s, "xes") || strings.HasSuffix(s, "ches") || strings.HasSuffix(s, "shes"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss"):
		return s[:len(s)-1]
	}
	return s
}
