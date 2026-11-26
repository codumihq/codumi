package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/codumihq/codumi/internal/store"
)

// NoteData is a module note as the desktop app edits it.
type NoteData struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Paths       []string `json:"paths"`
	Body        string   `json:"body"`
	Updated     int64    `json:"updated"`
}

// NoteLinks is the live context of a note: the indexed files its paths
// match and the database objects those files use.
type NoteLinks struct {
	Files      []string `json:"files"`
	FilesTotal int      `json:"filesTotal"`
	Tables     []string `json:"tables"`
	Indexed    bool     `json:"indexed"`
}

func toData(n *note) NoteData {
	d := NoteData{Slug: n.Slug, Name: n.Name, Description: n.Description, Paths: n.Paths, Body: n.Body}
	if d.Paths == nil {
		d.Paths = []string{}
	}
	if st, err := os.Stat(n.File); err == nil {
		d.Updated = st.ModTime().Unix()
	}
	return d
}

// ListNotes returns the notes of the project at root, most recently
// updated first.
func ListNotes(root string) []NoteData {
	out := []NoteData{}
	for _, n := range loadNotes(root) {
		out = append(out, toData(n))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
	return out
}

func notePath(root, slug string) (string, error) {
	if !reSlug.MatchString(slug) {
		return "", fmt.Errorf("invalid note slug %q", slug)
	}
	return filepath.Join(notesDir(root), slug+".md"), nil
}

func ReadNote(root, slug string) (NoteData, error) {
	p, err := notePath(root, slug)
	if err != nil {
		return NoteData{}, err
	}
	n, err := parseNote(p)
	if err != nil {
		return NoteData{}, err
	}
	return toData(n), nil
}

func yamlStr(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" || strings.ContainsAny(s, ":#'\"[]{}") || strings.HasPrefix(s, "-") {
		return `"` + strings.ReplaceAll(s, `"`, `'`) + `"`
	}
	return s
}

// WriteNote saves a note (creating .codumi/notes if needed) in the same
// format `codumi note new` writes, so the CLI and MCP read it unchanged.
func WriteNote(root string, d NoteData) (NoteData, error) {
	p, err := notePath(root, d.Slug)
	if err != nil {
		return d, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return d, err
	}
	ensureNotesTracked(root)
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + yamlStr(d.Name) + "\n")
	b.WriteString("description: " + yamlStr(d.Description) + "\n")
	b.WriteString("paths:\n")
	for _, x := range d.Paths {
		if x = strings.TrimSpace(x); x != "" {
			b.WriteString("  - " + x + "\n")
		}
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(d.Body) + "\n")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return d, err
	}
	if err := os.Rename(tmp, p); err != nil {
		return d, err
	}
	return ReadNote(root, d.Slug)
}

func DeleteNote(root, slug string) error {
	p, err := notePath(root, slug)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// Slugify turns a note name into a file name: "Autenticação" → "autenticacao".
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFD.String(strings.ToLower(name)) {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// NewNote creates a note with a unique slug from name and the given body.
func NewNote(root, name, body string) (NoteData, error) {
	base := Slugify(name)
	if base == "" {
		return NoteData{}, errors.New("give the note a name")
	}
	slug := base
	for i := 2; ; i++ {
		p, _ := notePath(root, slug)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			break
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
	return WriteNote(root, NoteData{Slug: slug, Name: strings.TrimSpace(name), Paths: []string{}, Body: body})
}

// LinksFor computes the live context of a set of note paths.
func LinksFor(root string, paths []string) NoteLinks {
	l := NoteLinks{Files: []string{}, Tables: []string{}}
	_, idx, ok := store.Find(root)
	if !ok || len(paths) == 0 {
		return l
	}
	db, err := store.Open(idx)
	if err != nil {
		return l
	}
	defer db.Close()
	l.Indexed = true
	m := (&note{Paths: paths}).matcher()
	inside := map[string]bool{}
	for _, f := range indexedFiles(db) {
		if m(f) {
			inside[f] = true
			l.FilesTotal++
			if len(l.Files) < 40 {
				l.Files = append(l.Files, f)
			}
		}
	}
	sort.Strings(l.Files)
	seen := map[string]bool{}
	if rows, err := db.Query("SELECT DISTINCT file, type, name FROM db_uses"); err == nil {
		for rows.Next() {
			var f, typ, name string
			rows.Scan(&f, &typ, &name)
			if !inside[f] {
				continue
			}
			k := name
			if typ != "table" {
				k = typ + " " + name
			}
			if !seen[k] {
				seen[k] = true
				l.Tables = append(l.Tables, k)
			}
		}
		rows.Close()
	}
	sort.Strings(l.Tables)
	return l
}

// RelPath turns an absolute path inside root into the slash form notes use.
func RelPath(root, abs string) (string, error) {
	r, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(r, "..") {
		return "", fmt.Errorf("%s is outside the project", abs)
	}
	return filepath.ToSlash(r), nil
}
