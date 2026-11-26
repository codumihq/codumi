package cli

import "testing"

func TestReadOnlySQL(t *testing.T) {
	ok := []string{
		"select name from decls",
		"  WITH x AS (select 1) select * from x",
		"select 'attach is just text' as a",
		"select count(*) from refs -- attach in a comment",
		"select 1;",
	}
	bad := []string{
		"attach database '/tmp/x.db' as x",
		"select 1; attach database '/tmp/x.db' as x",
		"vacuum into '/tmp/copy.db'",
		"pragma table_info(decls)",
		"select load_extension('x')",
		"delete from decls",
		"with x as (select 1) insert into decls select * from x",
	}
	for _, q := range ok {
		if err := readOnlySQL(q); err != nil {
			t.Errorf("%q should pass: %v", q, err)
		}
	}
	for _, q := range bad {
		if readOnlySQL(q) == nil {
			t.Errorf("%q should be refused", q)
		}
	}
}
