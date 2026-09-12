// Command csv converts between tables and items, in both directions.
//
// to_items reads a CSV attached to each input item and publishes one item per row. to_table
// reads the whole batch and publishes one item carrying the table, inline while it is small
// enough to travel in an envelope and as an artifact once it is not. That threshold is the
// documented inline_max_bytes and it is a parameter here, so a step on an installation that
// tuned the limit can say so rather than guess wrong in one direction or the other.
//
// A cell is text and stays text. A CSV carries no types, and a brick that guessed them would
// turn a reference of 007 into the number 7 and a postcode into a float. An item that wants a
// number says so downstream, where jq or a schema is already deciding what the payload means.
//
// A column set is the sorted union of every item's keys, so two runs over the same batch write
// the same table with the same columns in the same order. A value that is not a scalar is
// written as its JSON, because a cell holds one string and {"a":1} is the honest one.
package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/agentiik/bricks/internal/envelope"
)

const (
	in  = "in"
	out = "out"

	// toItems and toTable are the two directions, spelled as the manifest spells them.
	toItems = "to_items"
	toTable = "to_table"

	// inDir is where the artifacts of an input port are laid down, beside that port's
	// envelope and under their own names.
	inDir = "/agk/in"

	// defaultInlineMax is the documented inline_max_bytes: above it, a value travels as an
	// artifact and is referenced rather than carried. The default is the documentation's,
	// and a step on an installation that tuned the setting passes its own.
	defaultInlineMax = 262144

	// defaultTable is what the artifact is called when a step does not name it.
	defaultTable = "table.csv"
)

func main() {
	task := envelope.Read()

	params, err := envelope.Params()
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}

	var direction string
	supplied, err := envelope.Into(params, "direction", &direction)
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}
	if !supplied {
		envelope.Die(envelope.Invalid, "no direction: csv converts a table into items as %s or items into a table as %s, and a step says which", toItems, toTable)
	}

	delimiter, err := separator(params)
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}

	batch, err := task.In(in)
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}

	var published []envelope.Item
	switch direction {
	case toItems:
		published, err = rows(task, params, batch, delimiter)
	case toTable:
		published, err = table(task, params, batch, delimiter)
	default:
		envelope.Die(envelope.Invalid, "direction %q is neither %s nor %s", direction, toItems, toTable)
	}
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}

	if err := task.Out(out, published); err != nil {
		envelope.Die(envelope.Failed, "%s", err)
	}
	fmt.Fprintf(os.Stderr, "%s: %s in, %s out\n", direction, counted(len(batch.Items)), counted(len(published)))
}

// rows turns every item's attached table into items of its own.
//
// One item per row and not one per file, because a row is the unit a workflow fans out over:
// a table of four hundred orders is four hundred containers downstream if the step says so,
// and an item holding four hundred rows is one.
func rows(task envelope.Task, params map[string]json.RawMessage, batch envelope.Envelope, delimiter rune) ([]envelope.Item, error) {
	var named string
	if _, err := envelope.Into(params, "file", &named); err != nil {
		return nil, err
	}
	header := true
	if _, err := envelope.Into(params, "header", &header); err != nil {
		return nil, err
	}

	var published []envelope.Item
	for _, item := range batch.Items {
		name, err := attachment(item, named)
		if err != nil {
			return nil, err
		}
		path := filepath.Join(inDir, in, name)
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("item %s: the table %s could not be read at %s: %w", item.ID, name, path, err)
		}
		records, err := parse(content, delimiter)
		if err != nil {
			return nil, fmt.Errorf("item %s: the table %s does not parse: %w", item.ID, name, err)
		}
		if len(records) == 0 {
			continue
		}

		columns := records[0]
		body := records[1:]
		if !header {
			// Without a header the columns are named by position, because an item's
			// data is an object and a row has to reach it under some key. c0 and not
			// 0, so that the key is a name rather than a number read as one.
			columns = make([]string, len(records[0]))
			for i := range columns {
				columns[i] = "c" + strconv.Itoa(i)
			}
			body = records
		}
		for n, record := range body {
			data := make(map[string]any, len(columns))
			for i, column := range columns {
				if i < len(record) {
					data[column] = record[i]
					continue
				}
				// A short row is a row the table is ragged on. The column is
				// present and empty rather than absent, because a schema
				// downstream can say required and mean it.
				data[column] = ""
			}
			published = append(published, envelope.Item{
				// Derived from the item the table came from and the row's place
				// in it, so that two runs over the same table publish the same
				// identities and a replay rejoins what it rejoined before.
				ID:    item.ID + "#" + strconv.Itoa(n),
				Data:  data,
				Files: []envelope.File{},
			})
		}
	}
	return published, nil
}

// table turns the whole batch into one table, carried inline or attached.
func table(task envelope.Task, params map[string]json.RawMessage, batch envelope.Envelope, delimiter rune) ([]envelope.Item, error) {
	name := defaultTable
	if _, err := envelope.Into(params, "table_name", &name); err != nil {
		return nil, err
	}
	if name != filepath.Base(name) || name == "" || strings.HasPrefix(name, ".") {
		return nil, fmt.Errorf("table_name %q is not a file name: an artifact is named under the port it is attached to and carries no separator", name)
	}
	header := true
	if _, err := envelope.Into(params, "header", &header); err != nil {
		return nil, err
	}
	inlineMax := defaultInlineMax
	if _, err := envelope.Into(params, "inline_max_bytes", &inlineMax); err != nil {
		return nil, err
	}
	if inlineMax < 0 {
		return nil, fmt.Errorf("inline_max_bytes says %d: it is the size above which a value travels as an artifact, and it is never negative", inlineMax)
	}

	columns := union(batch.Items)
	content, err := write(batch.Items, columns, delimiter, header)
	if err != nil {
		return nil, err
	}

	// The identity is the digest of the table. Two runs over the same batch therefore
	// publish one identity and the store holds one object, which is the property the whole
	// system is built to keep.
	digest := envelope.Digest(content)
	data := map[string]any{
		"rows":    len(batch.Items),
		"columns": asStrings(columns),
		"bytes":   len(content),
	}
	item := envelope.Item{ID: "table-" + digest[:12], Data: data, Files: []envelope.File{}}

	if len(content) <= inlineMax {
		// Small enough to travel in the envelope, which is what the size rules allow
		// and what saves a downstream step from opening a file to read four rows.
		data["table"] = string(content)
		data["attached"] = false
		return []envelope.Item{item}, nil
	}
	file, err := task.Attach(out, name, "text/csv", content)
	if err != nil {
		return nil, err
	}
	data["attached"] = true
	item.Files = []envelope.File{file}
	return []envelope.Item{item}, nil
}

// attachment is the table an item carries, by name or by being its only one.
func attachment(item envelope.Item, named string) (string, error) {
	if named != "" {
		for _, f := range item.Files {
			if f.Name == named {
				return named, nil
			}
		}
		return "", fmt.Errorf("item %s attaches no file called %s: it carries %s", item.ID, named, names(item.Files))
	}
	switch len(item.Files) {
	case 1:
		return item.Files[0].Name, nil
	case 0:
		return "", fmt.Errorf("item %s attaches no file: csv reads a table an item attached, and the step names it as file where an item carries more than one", item.ID)
	}
	return "", fmt.Errorf("item %s attaches %d files, %s: the step names which one is the table as file", item.ID, len(item.Files), names(item.Files))
}

// union is every key any item carries, sorted.
//
// The union and not the first item's keys, because a batch whose items differ is ordinary and
// a table missing a column for half of them would lose what it was converting. Sorted, because
// the order has to be the same on the next run.
func union(items []envelope.Item) []string {
	seen := map[string]bool{}
	for _, item := range items {
		for key := range item.Data {
			seen[key] = true
		}
	}
	columns := make([]string, 0, len(seen))
	for key := range seen {
		columns = append(columns, key)
	}
	sort.Strings(columns)
	return columns
}

// write renders the items as a table.
func write(items []envelope.Item, columns []string, delimiter rune, header bool) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Comma = delimiter
	if header {
		if err := w.Write(columns); err != nil {
			return nil, fmt.Errorf("the header could not be written: %w", err)
		}
	}
	for _, item := range items {
		record := make([]string, len(columns))
		for i, column := range columns {
			record[i] = cell(item.Data[column])
		}
		if err := w.Write(record); err != nil {
			return nil, fmt.Errorf("item %s could not be written as a row: %w", item.ID, err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("the table could not be written: %w", err)
	}
	return buf.Bytes(), nil
}

// cell is one value as one string.
//
// A number is written as the shortest form that reads back as itself, not as Go's default
// formatting of a float: JSON has one number type and 1200 arriving as 1.2e+03 in a spreadsheet
// is a cell somebody has to explain.
func cell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// parse reads a table, accepting the ragged rows a real file has.
func parse(content []byte, delimiter rune) ([][]string, error) {
	r := csv.NewReader(bytes.NewReader(content))
	r.Comma = delimiter
	// A table written by somebody else is allowed rows of different lengths. The columns
	// are the header's, and a row that is short is padded where it is read rather than
	// refused here, because a refusal would lose the whole table for one line.
	r.FieldsPerRecord = -1
	return r.ReadAll()
}

// separator reads the delimiter parameter, which is one character.
func separator(params map[string]json.RawMessage) (rune, error) {
	s := ","
	if _, err := envelope.Into(params, "delimiter", &s); err != nil {
		return 0, err
	}
	runes := []rune(s)
	if len(runes) != 1 {
		return 0, fmt.Errorf("delimiter %q is %d characters: a delimiter is one", s, len(runes))
	}
	return runes[0], nil
}

// counted is a number and the word for it, because "1 items in" is a line that says the brick
// was written carelessly whatever else it says.
func counted(n int) string {
	if n == 1 {
		return "1 item"
	}
	return strconv.Itoa(n) + " items"
}

// names lists what an item attached, for a message that has to say what it found.
func names(files []envelope.File) string {
	if len(files) == 0 {
		return "nothing"
	}
	var out []string
	for _, f := range files {
		out = append(out, f.Name)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// asStrings is the columns as the payload carries them.
func asStrings(columns []string) []any {
	out := make([]any, len(columns))
	for i, c := range columns {
		out[i] = c
	}
	return out
}
