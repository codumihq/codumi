package store

import (
	"os"
	"path/filepath"
	"sync"
)

// O projeto se chamava Rastro. Quem já usava tem .rastro/rastro.db nos
// projetos e a pasta "rastro" nos dados do usuário; na primeira vez que o
// Codumi passa por eles, as pastas são renomeadas (nada é apagado).

const legacyDir, legacyFile = ".rastro", "rastro.db"

// Migrate troca root/.rastro por root/.codumi. Se as duas existirem (um índice
// novo já foi criado), traz da antiga o que não é índice: notas e links.
func Migrate(root string) { migrateProject(root) }

func migrateProject(root string) {
	old := filepath.Join(root, legacyDir)
	if _, err := os.Stat(old); err != nil {
		return
	}
	dir := filepath.Join(root, Dir)
	if _, err := os.Stat(dir); err == nil {
		for _, name := range []string{"notes", "links"} {
			from, to := filepath.Join(old, name), filepath.Join(dir, name)
			if _, err := os.Stat(from); err != nil {
				continue
			}
			if _, err := os.Stat(to); os.IsNotExist(err) {
				os.Rename(from, to)
			}
		}
		return
	}
	if os.Rename(old, dir) != nil {
		return
	}
	os.Rename(filepath.Join(dir, legacyFile), filepath.Join(dir, File))
	os.Remove(filepath.Join(dir, legacyFile+".tmp"))
}

var userDirOnce sync.Map

// UserDir devolve base/codumi (base = pasta de config ou de cache do sistema),
// renomeando base/rastro na primeira chamada quando ele existir.
func UserDir(base string) string {
	dir := filepath.Join(base, "codumi")
	if _, done := userDirOnce.LoadOrStore(base, true); !done {
		old := filepath.Join(base, "rastro")
		if _, err := os.Stat(old); err == nil {
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				os.Rename(old, dir)
			}
		}
	}
	return dir
}
