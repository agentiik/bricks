package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agentiik/bricks/internal/envelope"
	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// source is a connection string read as the driver that opens it wants it.
type source struct {
	dialect string
	driver  string
	name    string
}

// refusal is a fault in what the step or an item supplied, which no retry will mend.
type refusal struct{ msg string }

func (r *refusal) Error() string { return r.msg }

func refuse(format string, args ...any) error { return &refusal{fmt.Sprintf(format, args...)} }

// errUnreachable is a database that did not answer, which is the one fault worth a retry: the far
// side having a bad minute, rather than a statement that will be just as wrong next time.
var errUnreachable = errors.New("the database could not be reached")

// parse reads the secret as a URL whose scheme names the database.
//
// One spelling for the three, rather than each driver's own, so that the secret a person writes
// is the one every client of the database documents: postgres://, mysql:// and sqlite://.
func parse(dsn string) (source, error) {
	u, err := url.Parse(dsn)
	// A driver's own form, user:password@tcp(host)/db, parses as a scheme named after the user
	// and an opaque rest, and naming that scheme back would print part of the credential.
	if err != nil || u.Scheme == "" || u.Opaque != "" {
		// The value is a credential, so the error names its shape and never repeats it.
		return source{}, refuse("the secret dsn is not a URL: it is postgres://, mysql:// or sqlite:// followed by the database")
	}
	switch u.Scheme {
	case "postgres", "postgresql":
		return source{dialect: "postgres", driver: "pgx", name: dsn}, nil
	case "mysql":
		return mysqlSource(u)
	case "sqlite":
		return sqliteSource(u)
	}
	return source{}, refuse("the secret dsn names the scheme %s, and sql-query reads postgres, mysql and sqlite", u.Scheme)
}

// mysqlSource turns mysql://user:password@host:port/database into the driver's own form.
//
// The one query parameter read is tls, and a value that loosens verification is refused: a brick
// that connected without verifying would make network: egress and an allow list a decoration.
// Any other parameter is refused rather than passed on, since the driver sets each one as a
// session variable and a typo would fail at connection time with a message about the server.
func mysqlSource(u *url.URL) (source, error) {
	c := mysql.NewConfig()
	c.Net = "tcp"
	c.Addr = u.Host
	if u.Port() == "" {
		c.Addr = net.JoinHostPort(u.Hostname(), "3306")
	}
	c.User = u.User.Username()
	c.Passwd, _ = u.User.Password()
	c.DBName = strings.TrimPrefix(u.Path, "/")
	// DATETIME and TIMESTAMP as times rather than as the bytes of their text.
	c.ParseTime = true
	c.Loc = time.UTC
	for k, v := range u.Query() {
		if k != "tls" {
			return source{}, refuse("the secret dsn carries the parameter %s, and the one sql-query reads for MySQL is tls", k)
		}
		switch v[len(v)-1] {
		case "true", "preferred", "false":
			c.TLSConfig = v[len(v)-1]
		default:
			return source{}, refuse("the secret dsn sets tls to %s, and it is true, preferred or false: a value that skips verification is not offered", v[len(v)-1])
		}
	}
	return source{dialect: "mysql", driver: "mysql", name: c.FormatDSN()}, nil
}

// sqliteSource opens a database file read-only and immutable.
//
// Read-only because a database file a container can reach is one the workflow carries in its
// repository, mounted read-only at /agk/repo, or one an item attached, mounted read-only at
// /agk/in: a write would fail, and would vanish with the container if it did not. Immutable
// because SQLite otherwise wants to create a lock or a write-ahead index beside the file, which a
// read-only mount refuses.
func sqliteSource(u *url.URL) (source, error) {
	path := u.Path
	if u.Host != "" || !strings.HasPrefix(path, "/") {
		return source{}, refuse("the secret dsn names a SQLite database that is not an absolute path: it is sqlite:// followed by one, as sqlite:///agk/repo/data/orders.db")
	}
	// Checked here, since SQLite answers a missing file as a database it could not open, which
	// would read as one worth retrying.
	if _, err := os.Stat(path); err != nil {
		return source{}, refuse("the SQLite database %s cannot be read: %s", path, errors.Unwrap(err))
	}
	return source{dialect: "sqlite", driver: "sqlite", name: "file:" + path + "?mode=ro&immutable=1"}, nil
}

// run runs the statement once per item, or once where nothing is wired to in, inside one
// transaction, and returns every row as an item.
func run(ctx context.Context, db *sql.DB, src source, s settings, batch []envelope.Item, wired bool) ([]envelope.Item, error) {
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("%w: %s", errUnreachable, err)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: src.dialect == "sqlite"})
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errUnreachable, err)
	}
	defer tx.Rollback()

	var published []envelope.Item
	if !wired {
		rows, err := query(ctx, tx, s, nil, "")
		if err != nil {
			return nil, err
		}
		published = rows
	}
	for _, item := range batch {
		args := make([]any, len(s.Args))
		for i, field := range s.Args {
			v, ok := item.Data[field]
			if !ok {
				return nil, refuse("item %s has no %s, which args binds to placeholder %d", item.ID, field, i+1)
			}
			if args[i], err = bind(v); err != nil {
				return nil, refuse("item %s: %s %s", item.ID, field, err)
			}
		}
		rows, err := query(ctx, tx, s, args, item.ID)
		if err != nil {
			return nil, fmt.Errorf("item %s: %w", item.ID, err)
		}
		published = append(published, rows...)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("the transaction could not be committed, and nothing it wrote was kept: %w", err)
	}
	return published, nil
}

// bind is a value of an item's data as a placeholder takes it: a scalar as itself, and an object
// or an array as its JSON, which is what a json or jsonb column takes.
func bind(v any) (any, error) {
	switch v := v.(type) {
	case map[string]any, []any:
		b, err := json.Marshal(v)
		return string(b), err
	case float64:
		// A whole number travels as an integer, so that an INTEGER column compared with 42 is
		// compared with 42 and not with 42.0, which some databases refuse to cast implicitly.
		if v == float64(int64(v)) {
			return int64(v), nil
		}
	}
	return v, nil
}

// query runs the statement once and reads every row it returns.
//
// A row's identity is the value of id_column where the step names one, which keeps a row's
// identity across runs whatever order the database returns it in, and otherwise its position:
// the item's identity followed by #0, #1 and so on, as split names the items it makes, or the
// bare position where no item asked.
func query(ctx context.Context, tx *sql.Tx, s settings, args []any, from string) ([]envelope.Item, error) {
	rows, err := tx.QueryContext(ctx, s.Query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, t := range types {
		if seen[t.Name()] {
			return nil, refuse("the query returns two columns named %s, and a row becomes an item whose data has one member per name: alias one of them", t.Name())
		}
		seen[t.Name()] = true
	}
	if s.IDColumn != "" && len(types) > 0 && !seen[s.IDColumn] {
		return nil, refuse("id_column is %s, and the query returns no column of that name", s.IDColumn)
	}

	var items []envelope.Item
	for n := 0; rows.Next(); n++ {
		raw := make([]any, len(types))
		ptrs := make([]any, len(types))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		data := make(map[string]any, len(types))
		for i, t := range types {
			data[t.Name()] = value(raw[i], t.DatabaseTypeName())
		}
		id := strconv.Itoa(n)
		if from != "" {
			id = from + "#" + id
		}
		if s.IDColumn != "" {
			v := data[s.IDColumn]
			if v == nil {
				return nil, refuse("row %d has a null %s, and id_column names the column that identifies a row", n, s.IDColumn)
			}
			id = fmt.Sprint(v)
		}
		items = append(items, envelope.Item{ID: id, Data: data, Files: []envelope.File{}})
	}
	return items, rows.Err()
}

// value is a column's value as an item's data carries it.
//
// The drivers disagree on what they hand back: MySQL gives the bytes of a number's text where
// PostgreSQL gives the number. The column's declared type settles it, so that an INT is a number
// in an item whichever database it came from. A DECIMAL or NUMERIC stays the string the database
// wrote, since a JSON number is a double and money read as one is money with an error in it. A
// JSON column is its value rather than the text of it. Binary is base64, the one way bytes travel
// in JSON, and a time is RFC 3339 in UTC.
func value(v any, dbType string) any {
	t := strings.ToUpper(dbType)
	switch v := v.(type) {
	case nil, int64, float64, bool:
		return v
	case float32:
		return float64(v)
	case time.Time:
		return v.UTC().Format(time.RFC3339Nano)
	case []byte:
		if binary[t] || !utf8.Valid(v) {
			return base64.StdEncoding.EncodeToString(v)
		}
		return typed(string(v), t)
	case string:
		return typed(v, t)
	}
	return fmt.Sprint(v)
}

// typed reads a value that arrived as text by the type its column declares.
func typed(s, t string) any {
	t = strings.TrimPrefix(t, "UNSIGNED ")
	switch {
	case integer[t]:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
		if n, err := strconv.ParseUint(s, 10, 64); err == nil {
			return n
		}
	case float[t]:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	case t == "JSON" || t == "JSONB":
		var v any
		if json.Unmarshal([]byte(s), &v) == nil {
			return v
		}
	}
	return s
}

// The declared types read as numbers and as bytes, by the name each driver reports. Exact names
// rather than a substring, since INTERVAL contains INT and POINT does too.
var (
	integer = set("INT", "INTEGER", "TINYINT", "SMALLINT", "MEDIUMINT", "BIGINT", "YEAR", "INT2", "INT4", "INT8")
	float   = set("FLOAT", "DOUBLE", "REAL", "FLOAT4", "FLOAT8")
	binary  = set("BYTEA", "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB", "BINARY", "VARBINARY", "BIT")
)

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}
