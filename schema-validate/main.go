// Command schema-validate holds every item of its input to a JSON Schema and sends the ones
// that do not meet it to a port of their own.
//
// Routing rather than failing is the whole point. A batch of a thousand orders with three bad
// ones is not a failed step: the nine hundred and ninety seven travel on ok, the three travel
// on invalid with what was wrong with them, and the workflow decides whether that matters. A
// step that wants the strict reading reads invalid and fails on it, which is a line of YAML.
//
// The schema is the step's, given inline or as a path in the workflow repository, which is
// where the documentation's own example keeps them. Exactly one of the two, because a brick
// given both would have to decide which wins and neither answer is guessable from the outside.
//
// The validator is the one the engine validates workflow inputs with, deliberately: a brick
// that accepted what the engine refuses, or refused what it accepts, would turn one question
// into two answers.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/agentiik/bricks/internal/envelope"
)

const (
	in      = "in"
	ok      = "ok"
	invalid = "invalid"

	// repoDir is where the workflow repository tree is bound, AGK_REPO by default. A path
	// parameter is resolved against it and never against the working directory, because
	// the working directory is this brick's own and holds nothing the step wrote.
	repoDir = "/agk/repo"
)

func main() {
	task := envelope.Read()

	params, err := envelope.Params()
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}

	schema, err := compile(params)
	if err != nil {
		// Invalid rather than Failed: a schema that does not compile will not compile on
		// the next attempt, and the fault is in the step rather than in the batch.
		envelope.Die(envelope.Invalid, "%s", err)
	}

	batch, err := task.In(in)
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}

	var passed, refused []envelope.Item
	for _, item := range batch.Items {
		// The schema is applied to the item's data, which is the payload the workflow
		// wrote a schema for. The identity and the attachments are the envelope's
		// business and no schema of the step's describes them.
		data := any(item.Data)
		if item.Data == nil {
			data = map[string]any{}
		}
		if err := schema.Validate(data); err != nil {
			refused = append(refused, envelope.Item{
				ID: item.ID,
				// The payload travels unchanged under item, beside what was
				// wrong with it. A violation written into the payload would
				// hand the step below a document that is neither the original
				// nor the schema's.
				Data:  map[string]any{"item": item.Data, "violations": violations(err)},
				Files: item.Files,
			})
			continue
		}
		passed = append(passed, item)
	}

	if err := task.Out(ok, passed); err != nil {
		envelope.Die(envelope.Failed, "%s", err)
	}
	if err := task.Out(invalid, refused); err != nil {
		envelope.Die(envelope.Failed, "%s", err)
	}
	fmt.Fprintf(os.Stderr, "%s in, %d valid, %d invalid\n", counted(len(batch.Items)), len(passed), len(refused))
}

// counted is a number and the word for it, because "1 items in" is a line that says the brick
// was written carelessly whatever else it says.
func counted(n int) string {
	if n == 1 {
		return "1 item"
	}
	return strconv.Itoa(n) + " items"
}

// compile reads the schema the step supplied and compiles it as 2020-12.
//
// The inline form is the one a short schema is written in and the path is the one a shared
// schema lives at, under the repository every step is given read-only. A path is held inside
// that tree: a schema read from outside it would be a file the commit does not carry, and a
// run that pinned a commit would not be reading what that commit says.
func compile(params map[string]json.RawMessage) (*jsonschema.Schema, error) {
	var document json.RawMessage
	inline, err := envelope.Into(params, "schema", &document)
	if err != nil {
		return nil, err
	}

	var path string
	given, err := envelope.Into(params, "schema_path", &path)
	if err != nil {
		return nil, err
	}

	switch {
	case inline && given:
		return nil, fmt.Errorf("schema and schema_path were both supplied: a step gives the schema inline or names a file of the repository, and one of the two")
	case !inline && !given:
		return nil, fmt.Errorf("no schema: schema-validate holds every item to a schema, given inline as schema or as schema_path relative to %s", repoDir)
	case given:
		document, err = read(path)
		if err != nil {
			return nil, err
		}
	}

	var value any
	if err := json.Unmarshal(document, &value); err != nil {
		return nil, fmt.Errorf("the schema is not a JSON document: %w", err)
	}

	c := jsonschema.NewCompiler()
	// 2020-12 and not whatever the document's own $schema says. The language defines a
	// schema as a 2020-12 document, and a draft chosen by the file would let one step
	// validate under rules another step cannot.
	c.DefaultDraft(jsonschema.Draft2020)
	const name = "schema.json"
	if err := c.AddResource(name, value); err != nil {
		return nil, fmt.Errorf("the schema is not a schema: %w", err)
	}
	schema, err := c.Compile(name)
	if err != nil {
		return nil, fmt.Errorf("the schema does not compile: %w", err)
	}
	return schema, nil
}

// read takes a schema out of the repository tree, refusing a path that leaves it.
func read(path string) (json.RawMessage, error) {
	if path == "" {
		return nil, fmt.Errorf("schema_path is empty: it names a file of the repository relative to %s", repoDir)
	}
	if filepath.IsAbs(path) {
		return nil, fmt.Errorf("schema_path %s is absolute: it names a file of the repository relative to %s", path, repoDir)
	}
	full := filepath.Join(repoDir, path)
	// Checked on the cleaned path rather than on the text, because ../ is not the only
	// spelling of leaving a directory and Join is what settles the rest.
	if full != repoDir && !strings.HasPrefix(full, repoDir+string(filepath.Separator)) {
		return nil, fmt.Errorf("schema_path %s leaves %s: a schema is a file of the commit this run pinned", path, repoDir)
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("the schema could not be read at %s: %w", full, err)
	}
	return b, nil
}

// violations is what an item failed on, flattened into the shape the invalid port declares.
//
// One entry per leaf cause, because the validator's own error is a tree and the branches above
// the leaves carry a keyword that got there rather than a message worth reading. The detailed
// output is walked and not the basic one, for the reason the engine walks it too: the basic
// output flattens a $ref away and prints the reference's own "validation failed" over the
// message underneath, which is the message somebody reading a rejected item wants.
func violations(err error) []any {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []any{map[string]any{"path": "", "keyword": "", "message": err.Error()}}
	}
	units := leaves(*ve.DetailedOutput(), nil)
	if len(units) == 0 {
		return []any{map[string]any{"path": "", "keyword": "", "message": ve.Error()}}
	}
	out := make([]any, 0, len(units))
	for _, u := range units {
		out = append(out, map[string]any{
			"path":    u.InstanceLocation,
			"keyword": u.KeywordLocation,
			"message": u.Error.String(),
		})
	}
	// Sorted, so that an item refused on three fields is refused in the same order every
	// time it is refused. A port whose items reorder between two runs of the same batch is
	// a port nothing downstream can compare.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].(map[string]any), out[j].(map[string]any)
		if a["path"] != b["path"] {
			return a["path"].(string) < b["path"].(string)
		}
		return a["keyword"].(string) < b["keyword"].(string)
	})
	return out
}

// leaves collects the places the value actually departed from the schema.
func leaves(u jsonschema.OutputUnit, into []jsonschema.OutputUnit) []jsonschema.OutputUnit {
	if len(u.Errors) > 0 {
		for _, sub := range u.Errors {
			into = leaves(sub, into)
		}
		return into
	}
	if u.Error == nil {
		return into
	}
	return append(into, u)
}
