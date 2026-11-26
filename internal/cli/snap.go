package cli

// Cópias dos arquivos antes de o agente mexer, para o app mostrar o que mudou
// e desfazer arquivo por arquivo. O gancho "codumi guard snap --dir=<pasta>"
// roda antes de cada Edit/Write e guarda o arquivo como estava, só na
// primeira vez (a base fica até você aceitar ou desfazer).

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

type SnapEntry struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
	At      int64  `json:"at"`
}

const snapMax = 8 << 20 // arquivo maior que isso não entra (não dá para desfazer)

func snapKey(path string) string {
	h := sha1.Sum([]byte(filepath.Clean(path)))
	return hex.EncodeToString(h[:12])
}

// SnapTake guarda path como está agora, se ainda não houver cópia.
func SnapTake(dir, path string, now int64) {
	if dir == "" || path == "" || !filepath.IsAbs(path) {
		return
	}
	k := snapKey(path)
	meta := filepath.Join(dir, k+".json")
	if _, err := os.Stat(meta); err == nil {
		return
	}
	e := SnapEntry{Path: filepath.Clean(path), At: now}
	if st, err := os.Stat(path); err == nil {
		if st.IsDir() || st.Size() > snapMax {
			return
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return
		}
		os.MkdirAll(dir, 0o700)
		if os.WriteFile(filepath.Join(dir, k+".orig"), b, 0o600) != nil {
			return
		}
		e.Existed = true
	}
	os.MkdirAll(dir, 0o700)
	b, _ := json.Marshal(e)
	os.WriteFile(meta, b, 0o600)
}

// SnapList devolve os arquivos com cópia, do mais antigo ao mais novo.
func SnapList(dir string) []SnapEntry {
	ms, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var out []SnapEntry
	for _, m := range ms {
		var e SnapEntry
		if b, err := os.ReadFile(m); err == nil && json.Unmarshal(b, &e) == nil && e.Path != "" {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At || out[i].At == out[j].At && out[i].Path < out[j].Path })
	return out
}

// SnapOrig devolve o conteúdo de antes (vazio se o arquivo não existia).
func SnapOrig(dir, path string) (b []byte, existed, ok bool) {
	k := snapKey(path)
	var e SnapEntry
	raw, err := os.ReadFile(filepath.Join(dir, k+".json"))
	if err != nil || json.Unmarshal(raw, &e) != nil {
		return nil, false, false
	}
	if !e.Existed {
		return nil, false, true
	}
	b, err = os.ReadFile(filepath.Join(dir, k+".orig"))
	return b, true, err == nil
}

// SnapDrop esquece a cópia: o estado atual vira a base.
func SnapDrop(dir, path string) {
	k := snapKey(path)
	os.Remove(filepath.Join(dir, k+".json"))
	os.Remove(filepath.Join(dir, k+".orig"))
}

// snapSafe deixa só caracteres seguros num nome de pasta.
func snapSafe(s string) string {
	var b []byte
	for i := 0; i < len(s) && len(b) < 64; i++ {
		c := s[i]
		if c == '-' || c == '_' || c == ':' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			if c == ':' {
				c = '_'
			}
			b = append(b, c)
		}
	}
	return string(b)
}

// SnapTurnDir é a pasta das cópias feitas durante a mensagem turn.
func SnapTurnDir(dir, turn string) string {
	t := snapSafe(turn)
	if t == "" {
		return ""
	}
	return filepath.Join(dir, "turns", t)
}
