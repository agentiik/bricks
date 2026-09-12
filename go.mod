module github.com/agentiik/bricks

go 1.27

require (
	// jq, for the brick of that name. The brick is a jq expression applied to every item,
	// so it needs a jq: this is the implementation that is pure Go, which is what lets the
	// image be a static binary on scratch with no shell and no C library in it. The
	// alternative was an Alpine image carrying the C jq, which is a shell, a package
	// manager and a libc more than this brick needs.
	github.com/itchyny/gojq v0.12.17
	// JSON Schema 2020-12, for schema-validate. It is the implementation the engine
	// validates a workflow's inputs with, and using the same one is the point: a brick that
	// accepted what the engine refuses, or refused what it accepts, would turn one question
	// into two answers.
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
)

require (
	github.com/itchyny/timefmt-go v0.1.6 // indirect
	golang.org/x/text v0.39.0 // indirect
)
