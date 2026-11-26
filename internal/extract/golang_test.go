package extract

import (
	"reflect"
	"testing"
)

func TestSQLTables(t *testing.T) {
	got := sqlTables(`DELETE FROM instances WHERE id = $1 RETURNING (SELECT 1 FROM audit a JOIN public."users" u ON true)`)
	want := []sqlTable{{"instances", []string{"delete"}}, {"audit", []string{"select"}}, {"users", []string{"select"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	got = sqlTables(`INSERT INTO jobs (id) VALUES ($1) ON CONFLICT (id) DO UPDATE SET n = jobs.n + 1`)
	if len(got) != 1 || got[0].name != "jobs" || got[0].ops[0] != "insert" {
		t.Fatalf("upsert: %+v", got)
	}
}
