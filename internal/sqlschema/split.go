package sqlschema

import "strings"

// Stmt is one SQL statement. Text keeps everything; Skel has dollar-quoted
// bodies replaced by $BODY$ so keywords inside function bodies don't match.
type Stmt struct {
	Text   string
	Skel   string
	Line   int
	Bodies []string
}

// Split breaks a SQL script into statements, honouring comments, quotes and
// dollar quoting.
func Split(src string) []Stmt {
	var out []Stmt
	var text, skel strings.Builder
	var bodies []string
	line, start := 1, 0
	mark := func() {
		if start == 0 {
			start = line
		}
	}
	flush := func() {
		t := strings.TrimSpace(text.String())
		if t != "" {
			out = append(out, Stmt{Text: t, Skel: strings.TrimSpace(skel.String()), Line: start, Bodies: bodies})
		}
		text.Reset()
		skel.Reset()
		bodies = nil
		start = 0
	}
	n := len(src)
	for i := 0; i < n; {
		c := src[i]
		switch {
		case c == '-' && i+1 < n && src[i+1] == '-':
			j := strings.IndexByte(src[i:], '\n')
			if j < 0 {
				i = n
			} else {
				i += j
			}
			continue
		case c == '/' && i+1 < n && src[i+1] == '*':
			end := n
			if j := strings.Index(src[i+2:], "*/"); j >= 0 {
				end = i + 2 + j + 2
			}
			line += strings.Count(src[i:end], "\n")
			text.WriteByte(' ')
			skel.WriteByte(' ')
			i = end
			continue
		case c == '\'' || c == '"':
			mark()
			j := i + 1
			for j < n {
				if src[j] == c {
					if j+1 < n && src[j+1] == c { // doubled quote escape
						j += 2
						continue
					}
					break
				}
				j++
			}
			end := j + 1
			if end > n {
				end = n
			}
			seg := src[i:end]
			line += strings.Count(seg, "\n")
			text.WriteString(seg)
			skel.WriteString(seg)
			i = end
			continue
		case c == '$':
			if tag := dollarTag(src[i:]); tag != "" {
				mark()
				bodyStart := i + len(tag)
				end := n
				bodyEnd := n
				if j := strings.Index(src[bodyStart:], tag); j >= 0 {
					bodyEnd = bodyStart + j
					end = bodyEnd + len(tag)
				}
				seg := src[i:end]
				line += strings.Count(seg, "\n")
				text.WriteString(seg)
				skel.WriteString("$BODY$")
				bodies = append(bodies, src[bodyStart:bodyEnd])
				i = end
				continue
			}
		case c == ';':
			flush()
			i++
			continue
		}
		if c == '\n' {
			line++
		} else if c != ' ' && c != '\t' && c != '\r' {
			mark()
		}
		text.WriteByte(c)
		skel.WriteByte(c)
		i++
	}
	flush()
	return out
}

// dollarTag returns "$tag$" if s starts with a dollar-quote opener.
func dollarTag(s string) string {
	if len(s) < 2 || s[0] != '$' {
		return ""
	}
	if s[1] >= '0' && s[1] <= '9' {
		return ""
	}
	for j := 1; j < len(s); j++ {
		ch := s[j]
		if ch == '$' {
			return s[:j+1]
		}
		if !(ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			return ""
		}
	}
	return ""
}
