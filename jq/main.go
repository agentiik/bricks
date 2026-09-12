// Command jq is the catalog's transformation and filtering brick: it runs one jq expression
// over every item of its input port and publishes what comes back.
//
// The expression sees the item's data as its input, which is what makes the ordinary
// expressions the ordinary ones: {ref, total: .amount} reshapes, select(.amount > 0) filters,
// .lines[] turns one item into several. The identity and the attachments of the item travel
// beside it as $id and $files, so an expression can filter on an identity without the brick
// inventing a field for it.
//
// A value that is not an object cannot be an item's data, and an expression that yields one is
// refused rather than wrapped: wrapping would mint a key nobody wrote, and the expression that
// was meant is one character longer. The message says which.
//
// There is no error port. An expression that fails on an item fails the step, because jq
// already has try and catch and a brick that swallowed a runtime error would be deciding
// something the author of the expression is better placed to decide.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/itchyny/gojq"

	"github.com/agentiik/bricks/internal/envelope"
)

// The ports this brick declares, spelled once so the manifest and the code cannot disagree.
const (
	in  = "in"
	out = "out"
)

func main() {
	task := envelope.Read()

	params, err := envelope.Params()
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}
	var expression string
	supplied, err := envelope.Into(params, "expression", &expression)
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}
	if !supplied || expression == "" {
		envelope.Die(envelope.Invalid, "no expression: jq runs one jq expression over every item and the expression is what the step supplies")
	}

	query, err := gojq.Parse(expression)
	if err != nil {
		// Invalid rather than Failed: an expression that does not parse will not parse
		// on the next attempt either, and retrying it wastes a minute to say the same
		// thing.
		envelope.Die(envelope.Invalid, "the expression does not parse: %s", err)
	}
	// Compiled once for the whole batch and not once per item. The variables are declared
	// here and bound per item below, which is what gojq asks for and what keeps the cost
	// of a thousand items one compilation.
	code, err := gojq.Compile(query, gojq.WithVariables([]string{"$id", "$files"}))
	if err != nil {
		envelope.Die(envelope.Invalid, "the expression cannot be compiled: %s", err)
	}

	batch, err := task.In(in)
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}

	published := make([]envelope.Item, 0, len(batch.Items))
	for _, item := range batch.Items {
		values, err := run(code, item)
		if err != nil {
			envelope.Die(envelope.Failed, "item %s: %s", item.ID, err)
		}
		for n, value := range values {
			data, ok := value.(map[string]any)
			if !ok {
				envelope.Die(envelope.Invalid,
					"item %s yielded %s, and an item's data is an object: an expression producing a bare value is written {value: .} or wrapped in an object of its own",
					item.ID, describe(value))
			}
			published = append(published, envelope.Item{
				ID:   identity(item.ID, n, len(values)),
				Data: data,
				// The artifacts travel with the item. A transformation of the
				// payload is not a reason to drop what the payload refers to,
				// and an expression that wants them gone says files: [] by
				// publishing an item the step below reads differently.
				Files: item.Files,
			})
		}
	}

	if err := task.Out(out, published); err != nil {
		envelope.Die(envelope.Failed, "%s", err)
	}
	fmt.Fprintf(os.Stderr, "%s in, %s out\n", counted(len(batch.Items)), counted(len(published)))
}

// run evaluates the expression against one item and collects the whole stream.
//
// The stream is collected rather than streamed because the envelope is one document: a brick
// that wrote items as it found them would have to know how many there were before it started.
func run(code *gojq.Code, item envelope.Item) ([]any, error) {
	data := item.Data
	if data == nil {
		// An item with no data is an empty object and not null. null would make every
		// ordinary expression fail on it, and an item that carries only attachments is
		// a legitimate item.
		data = map[string]any{}
	}
	files, err := asAny(item.Files)
	if err != nil {
		return nil, err
	}
	iter := code.Run(data, item.ID, files)

	var values []any
	for {
		value, ok := iter.Next()
		if !ok {
			return values, nil
		}
		if err, ok := value.(error); ok {
			var halt *gojq.HaltError
			if errors.As(err, &halt) && halt.Value() == nil {
				// halt with no value is the expression saying it is done with
				// this item, which is not a failure.
				return values, nil
			}
			return nil, err
		}
		values = append(values, value)
	}
}

// identity is what a published item is called.
//
// One output keeps the identity it came from, which is what makes a reshaping brick invisible
// to anything matching on identities. Several outputs from one item need telling apart, so the
// index follows the identity they were derived from, and two runs over the same batch therefore
// publish the same identities.
func identity(id string, n, total int) string {
	if total <= 1 {
		return id
	}
	return id + "#" + strconv.Itoa(n)
}

// counted is a number and the word for it, because "1 items in" is a line that says the brick
// was written carelessly whatever else it says.
func counted(n int) string {
	if n == 1 {
		return "1 item"
	}
	return strconv.Itoa(n) + " items"
}

// asAny puts the attachments into the shape a jq expression reads, which is the shape they
// have in the envelope and not the Go one.
func asAny(files []envelope.File) (any, error) {
	if len(files) == 0 {
		return []any{}, nil
	}
	b, err := json.Marshal(files)
	if err != nil {
		return nil, fmt.Errorf("the attachments could not be given to the expression: %w", err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("the attachments could not be given to the expression: %w", err)
	}
	return v, nil
}

// describe names what an expression yielded, for the message that refuses it. The type is what
// the author has to change, so the type is what it says.
func describe(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case float64, int:
		return "a number"
	case string:
		return "a string"
	case []any:
		return "an array"
	}
	return fmt.Sprintf("a %T", v)
}
