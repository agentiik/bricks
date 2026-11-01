module github.com/agentiik/bricks

go 1.27

require (
	// MySQL, for sql-query. The driver nearly every Go program talking to MySQL uses, pure Go,
	// so the brick stays a static binary.
	github.com/go-sql-driver/mysql v1.10.1
	// YAML, for catalog_test.go alone, which reads every brick.yaml to hold it to the brick
	// schema. It is the parser the engine reads a manifest with, so the document checked here
	// is the document the engine sees, and no brick links it.
	github.com/goccy/go-yaml v1.19.2
	// jq, for the brick of that name. The brick is a jq expression applied to every item,
	// so it needs a jq: this is the implementation that is pure Go, which is what lets the
	// image be a static binary on scratch with no shell and no C library in it. The
	// alternative was an Alpine image carrying the C jq, which is a shell, a package
	// manager and a libc more than this brick needs.
	github.com/itchyny/gojq v0.12.17
	// PostgreSQL, for sql-query. The driver the engine itself talks to its database with, so one
	// PostgreSQL client is trusted across the project rather than two.
	github.com/jackc/pgx/v5 v5.12.0
	// JSON Schema 2020-12, for schema-validate. It is the implementation the engine
	// validates a workflow's inputs with, and using the same one is the point: a brick that
	// accepted what the engine refuses, or refused what it accepts, would turn one question
	// into two answers.
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	// SQLite, for sql-query. SQLite translated to Go, rather than the C library through cgo,
	// which is what keeps the image a static binary with no C library in it, at the cost of a
	// larger binary and a slower engine than the C one, neither of which a query step feels.
	modernc.org/sqlite v1.61.1
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/itchyny/timefmt-go v0.1.6 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.3 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	modernc.org/libc v1.79.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)
