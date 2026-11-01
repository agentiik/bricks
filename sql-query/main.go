// Command sql-query runs one SQL statement against PostgreSQL, MySQL or SQLite and publishes each
// row it returns as an item.
//
// The connection string is the secret dsn and never a parameter: it carries a password, a
// parameter is printed in the run's detail, and a secret is mounted on a tmpfs, masked in the log
// and redeemed for this task alone. The statement is the step's, and the item is what fills its
// placeholders, so that a thousand orders are one step's worth of configuration.
//
// Every statement of a step runs in one transaction, committed once every item has run. A batch
// that fails halfway leaves the database as it found it, which is what makes a retry of the step
// safe: an insert per item retried after the fortieth failed would otherwise insert the first
// thirty-nine twice.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/agentiik/bricks/internal/envelope"
)

const (
	in  = "in"
	out = "out"

	// dsnPath is where the runner binds the secret of that name: the manifest's own mount point.
	dsnPath = "/agk/secrets/dsn"
)

// settings is what the step supplied.
type settings struct {
	Query     string
	Args      []string
	IDColumn  string
	InlineMax int
}

func main() {
	task := envelope.Read()

	s, err := configure()
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}
	task.Limits.InlineMax = s.InlineMax

	batch, wired, err := input(task)
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}
	if !wired && len(s.Args) > 0 {
		envelope.Die(envelope.Invalid, "args names %s, and no item arrives on in to read them from: a step with no input runs its statement once, with nothing to bind", strings.Join(s.Args, ", "))
	}
	// A step with nothing to do connects to nothing, which is also what lets an empty batch pass
	// on a runner that holds no credential for the database.
	if wired && len(batch) == 0 {
		fmt.Fprintln(os.Stderr, "0 items in, nothing run")
		return
	}

	dsn, err := os.ReadFile(dsnPath)
	if errors.Is(err, os.ErrNotExist) {
		envelope.Die(envelope.Invalid, "no secret dsn is mounted at %s: sql-query reads the connection string from it, and the workflow names the secret of its namespace that answers", dsnPath)
	}
	if err != nil {
		envelope.Die(envelope.Failed, "the secret dsn could not be read: %s", err)
	}
	src, err := parse(strings.TrimSpace(string(dsn)))
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}
	db, err := sql.Open(src.driver, src.name)
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}
	defer db.Close()

	rows, err := run(context.Background(), db, src, s, batch, wired)
	var refused *refusal
	switch {
	case errors.As(err, &refused):
		envelope.Die(envelope.Invalid, "%s", err)
	case errors.Is(err, errUnreachable):
		envelope.Die(envelope.Transient, "%s", err)
	case err != nil:
		envelope.Die(envelope.Failed, "%s", err)
	}
	if err := task.Out(out, rows); err != nil {
		envelope.Die(envelope.Failed, "%s", err)
	}
	fmt.Fprintf(os.Stderr, "%d items in, %d rows out, against %s\n", len(batch), len(rows), src.dialect)
}

// configure reads the step's parameters.
func configure() (settings, error) {
	s := settings{InlineMax: envelope.DefaultLimits.InlineMax}
	params, err := envelope.Params()
	if err != nil {
		return s, err
	}
	if _, err := envelope.Into(params, "query", &s.Query); err != nil {
		return s, err
	}
	if strings.TrimSpace(s.Query) == "" {
		return s, errors.New("no query: sql-query runs one statement, and the step supplies it")
	}
	if _, err := envelope.Into(params, "args", &s.Args); err != nil {
		return s, err
	}
	for _, a := range s.Args {
		if a == "" {
			return s, errors.New("args names an empty field: each entry is a field of an item's data, bound to the placeholder of its position")
		}
	}
	if _, err := envelope.Into(params, "id_column", &s.IDColumn); err != nil {
		return s, err
	}
	if _, err := envelope.Into(params, "inline_max_bytes", &s.InlineMax); err != nil {
		return s, err
	}
	if s.InlineMax < 0 {
		return s, fmt.Errorf("inline_max_bytes is %d, and a size is not below zero", s.InlineMax)
	}
	return s, nil
}

// input reads the port in, and says whether anything is wired to it at all. A port nobody fed and
// a port fed an empty batch read the same through Task.In, and they are not the same here: the
// first runs the statement once, as the first step of a workflow reading a table does, and the
// second runs it for none.
func input(task envelope.Task) ([]envelope.Item, bool, error) {
	if _, err := os.Stat("/agk/in/" + in + "/envelope.json"); errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	batch, err := task.In(in)
	if err != nil {
		return nil, true, err
	}
	return batch.Items, true, nil
}
