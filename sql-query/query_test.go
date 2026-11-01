package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/agentiik/bricks/internal/envelope"
)

// What the brick does with a statement, held against SQLite in the test's own process, and
// against PostgreSQL and MySQL where SQL_QUERY_POSTGRES and SQL_QUERY_MYSQL name one, as CI does.
// The contract half, what a container is given and what it writes, is the cases'.

// orders is a SQLite database file holding three orders, opened as the brick opens one.
func orders(t *testing.T) (*sql.DB, source) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orders.db")
	w, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE orders (ref TEXT PRIMARY KEY, customer TEXT, cents INTEGER, rate REAL, note TEXT, scan BLOB)`,
		`INSERT INTO orders VALUES ('a1', 'acme', 1200, 0.2, NULL, x'00ff')`,
		`INSERT INTO orders VALUES ('b2', 'acme', 3400, 0.2, 'late', NULL)`,
		`INSERT INTO orders VALUES ('c3', 'globex', 900, 0.055, NULL, NULL)`,
	} {
		if _, err := w.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	src, err := parse("sqlite://" + path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(src.driver, src.name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, src
}

func item(id string, data map[string]any) envelope.Item {
	return envelope.Item{ID: id, Data: data, Files: []envelope.File{}}
}

func TestAStepWithNoInputRunsItsStatementOnce(t *testing.T) {
	db, src := orders(t)
	got, err := run(context.Background(), db, src, settings{Query: "SELECT ref, cents, rate, note, scan FROM orders WHERE customer = 'acme' ORDER BY ref"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []envelope.Item{
		item("0", map[string]any{"ref": "a1", "cents": int64(1200), "rate": 0.2, "note": nil, "scan": "AP8="}),
		item("1", map[string]any{"ref": "b2", "cents": int64(3400), "rate": 0.2, "note": "late", "scan": nil}),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestEachItemFillsThePlaceholders(t *testing.T) {
	db, src := orders(t)
	batch := []envelope.Item{
		item("cust-acme", map[string]any{"customer": "acme", "floor": float64(1000)}),
		item("cust-globex", map[string]any{"customer": "globex", "floor": float64(0)}),
	}
	s := settings{Query: "SELECT ref FROM orders WHERE customer = ? AND cents > ? ORDER BY ref", Args: []string{"customer", "floor"}}
	got, err := run(context.Background(), db, src, s, batch, true)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, i := range got {
		ids = append(ids, i.ID)
	}
	if want := []string{"cust-acme#0", "cust-acme#1", "cust-globex#0"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("the rows are identified as %v, and want %v", ids, want)
	}

	s.IDColumn = "ref"
	got, err = run(context.Background(), db, src, s, batch, true)
	if err != nil {
		t.Fatal(err)
	}
	ids = ids[:0]
	for _, i := range got {
		ids = append(ids, i.ID)
	}
	if want := []string{"a1", "b2", "c3"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("with id_column, the rows are identified as %v, and want %v", ids, want)
	}
}

func TestWhatNoRetryMendsIsRefused(t *testing.T) {
	db, src := orders(t)
	for _, c := range []struct {
		name  string
		s     settings
		batch []envelope.Item
		says  string
	}{
		{"an item without a field args binds", settings{Query: "SELECT ref FROM orders WHERE customer = ?", Args: []string{"customer"}}, []envelope.Item{item("x", map[string]any{})}, "item x has no customer"},
		{"two columns of one name", settings{Query: "SELECT ref, customer AS ref FROM orders"}, nil, "two columns named ref"},
		{"an id_column the query does not return", settings{Query: "SELECT cents FROM orders", IDColumn: "ref"}, nil, "no column of that name"},
		{"a null id_column", settings{Query: "SELECT note FROM orders ORDER BY ref", IDColumn: "note"}, nil, "null note"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := run(context.Background(), db, src, c.s, c.batch, c.batch != nil)
			var r *refusal
			if !errors.As(err, &r) || !strings.Contains(err.Error(), c.says) {
				t.Errorf("got %v, and want a refusal saying %q", err, c.says)
			}
		})
	}
}

func TestTheConnectionStringIsReadAsAURL(t *testing.T) {
	db := filepath.Join(t.TempDir(), "x.db")
	os.WriteFile(db, nil, 0o600)
	for _, c := range []struct {
		dsn, dialect, name string
	}{
		{"postgres://app:s3cret@db.example.com:5432/billing?sslmode=require", "postgres", "postgres://app:s3cret@db.example.com:5432/billing?sslmode=require"},
		{"postgresql://app@db.example.com/billing", "postgres", "postgresql://app@db.example.com/billing"},
		{"mysql://app:s3cret@db.example.com/billing?tls=true", "mysql", "app:s3cret@tcp(db.example.com:3306)/billing?parseTime=true&tls=true"},
		{"mysql://app:p%40ss@db.example.com:3307/billing", "mysql", "app:p@ss@tcp(db.example.com:3307)/billing?parseTime=true"},
		{"sqlite://" + db, "sqlite", "file:" + db + "?mode=ro&immutable=1"},
	} {
		got, err := parse(c.dsn)
		if err != nil {
			t.Errorf("%s: %s", c.dsn, err)
			continue
		}
		if got.dialect != c.dialect || got.name != c.name {
			t.Errorf("%s is read as %s %q, and want %s %q", c.dsn, got.dialect, got.name, c.dialect, c.name)
		}
	}
	for _, c := range []struct{ dsn, says string }{
		{"app:s3cret@tcp(db)/billing", "not a URL"},
		{"oracle://db/billing", "the scheme oracle"},
		{"mysql://db/billing?tls=skip-verify", "skips verification"},
		{"mysql://db/billing?timeout=5s", "the parameter timeout"},
		{"sqlite://data/orders.db", "not an absolute path"},
		{"sqlite:///nowhere/orders.db", "cannot be read"},
	} {
		_, err := parse(c.dsn)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: got %v, and want a refusal saying %q", c.dsn, err, c.says)
		}
		if err != nil && strings.Contains(err.Error(), "s3cret") {
			t.Errorf("%s: the refusal repeats the credential: %s", c.dsn, err)
		}
	}
}

func TestAValueIsReadByItsColumnsType(t *testing.T) {
	for _, c := range []struct {
		v      any
		dbType string
		want   any
	}{
		{[]byte("42"), "INT", int64(42)},
		{[]byte("18446744073709551615"), "UNSIGNED BIGINT", uint64(18446744073709551615)},
		{[]byte("0.5"), "DOUBLE", 0.5},
		{[]byte("1290.50"), "DECIMAL", "1290.50"},
		{"1290.50", "NUMERIC", "1290.50"},
		{"1 day", "INTERVAL", "1 day"},
		{[]byte(`{"a":[1]}`), "JSON", map[string]any{"a": []any{float64(1)}}},
		{`{"a":1}`, "JSONB", map[string]any{"a": float64(1)}},
		{[]byte{0, 0xff}, "VARBINARY", "AP8="},
		{[]byte("plain"), "BLOB", "cGxhaW4="},
		{[]byte{0xff}, "", "/w=="},
		{[]byte("text"), "VARCHAR", "text"},
		{float32(0.5), "FLOAT", 0.5},
	} {
		if got := value(c.v, c.dbType); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v as %s is %#v, and want %#v", c.v, c.dbType, got, c.want)
		}
	}
}

// TestAFailingBatchLeavesTheDatabaseAsItWas runs against a database that takes writes: a batch
// whose second insert breaks a constraint commits nothing, which is what makes a retry safe.
func TestAFailingBatchLeavesTheDatabaseAsItWas(t *testing.T) {
	for _, env := range []string{"SQL_QUERY_POSTGRES", "SQL_QUERY_MYSQL"} {
		t.Run(env, func(t *testing.T) {
			db, src := server(t, env)
			mustExec(t, db, "DROP TABLE IF EXISTS charges")
			mustExec(t, db, "CREATE TABLE charges (ref VARCHAR(16) PRIMARY KEY, cents INT)")
			insert := "INSERT INTO charges (ref, cents) VALUES (?, ?)"
			if src.dialect == "postgres" {
				insert = "INSERT INTO charges (ref, cents) VALUES ($1, $2)"
			}
			batch := []envelope.Item{
				item("a", map[string]any{"ref": "a1", "cents": float64(1200)}),
				item("b", map[string]any{"ref": "a1", "cents": float64(3400)}),
			}
			if _, err := run(context.Background(), db, src, settings{Query: insert, Args: []string{"ref", "cents"}}, batch, true); err == nil {
				t.Fatal("a batch inserting one key twice succeeded")
			}
			var n int
			if err := db.QueryRow("SELECT count(*) FROM charges").Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("the failed batch left %d rows behind", n)
			}
		})
	}
}

// TestEachDatabaseIsReadTheSameWay holds the two servers to one reading of the same columns.
func TestEachDatabaseIsReadTheSameWay(t *testing.T) {
	for _, env := range []string{"SQL_QUERY_POSTGRES", "SQL_QUERY_MYSQL"} {
		t.Run(env, func(t *testing.T) {
			db, src := server(t, env)
			q := "SELECT CAST(42 AS SIGNED) AS n, CAST(1290.50 AS DECIMAL(10,2)) AS price, CAST('{\"a\": 1}' AS JSON) AS doc, CAST('2026-10-01 04:03:13' AS DATETIME) AS at, 'x' AS s, NULL AS nothing"
			if src.dialect == "postgres" {
				q = "SELECT 42::bigint AS n, 1290.50::numeric(10,2) AS price, '{\"a\": 1}'::jsonb AS doc, '2026-10-01 04:03:13'::timestamp AS at, 'x' AS s, NULL AS nothing"
			}
			got, err := run(context.Background(), db, src, settings{Query: q}, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"n": int64(42), "price": "1290.50", "doc": map[string]any{"a": float64(1)}, "at": "2026-10-01T04:03:13Z", "s": "x", "nothing": nil}
			if len(got) != 1 || !reflect.DeepEqual(got[0].Data, want) {
				t.Errorf("got %v\nwant %v", got, want)
			}
		})
	}
}

func server(t *testing.T, env string) (*sql.DB, source) {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("%s names no database", env)
	}
	src, err := parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(src.driver, src.name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, src
}

func mustExec(t *testing.T, db *sql.DB, stmt string) {
	t.Helper()
	if _, err := db.Exec(stmt); err != nil {
		t.Fatal(err)
	}
}
