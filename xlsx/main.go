// Command xlsx converts between spreadsheets and items, both ways, as csv does for comma-separated
// text: to_items reads the workbook each item attaches and publishes one item per row, to_table
// writes the whole batch into one workbook attached to one item.
//
// A cell keeps its type, which is what an xlsx file has over a CSV one: a number is a number in
// the item's data, a boolean a boolean, an empty cell null. The table is always an artifact, never
// inline: a step below reads a workbook as a file whatever its size.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/agentiik/bricks/internal/envelope"
)

const (
	in      = "in"
	out     = "out"
	inDir   = "/agk/in"
	toItems = "to_items"
	toTable = "to_table"

	mediaType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
)

func main() {
	task := envelope.Read()
	params, err := envelope.Params()
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}
	var direction, sheet, file string
	tableName := "table.xlsx"
	header := true
	for name, v := range map[string]any{"direction": &direction, "sheet": &sheet, "file": &file, "table_name": &tableName, "header": &header} {
		if _, err := envelope.Into(params, name, v); err != nil {
			envelope.Die(envelope.Invalid, "%s", err)
		}
	}
	batch, err := task.In(in)
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}

	var published []envelope.Item
	switch direction {
	case toItems:
		published, err = rows(batch.Items, file, sheet, header)
	case toTable:
		published, err = table(task, batch.Items, tableName, sheet, header)
	case "":
		envelope.Die(envelope.Invalid, "no direction: xlsx converts a workbook into items as %s or items into a workbook as %s, and a step says which", toItems, toTable)
	default:
		envelope.Die(envelope.Invalid, "direction %q is neither %s nor %s", direction, toItems, toTable)
	}
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}
	if err := task.Out(out, published); err != nil {
		envelope.Die(envelope.Failed, "%s", err)
	}
	fmt.Fprintf(os.Stderr, "%s: %d items in, %d out\n", direction, len(batch.Items), len(published))
}

// rows turns the workbook each item attaches into one item per row, identified as the item it came
// from and the row's place, so that two runs over one workbook publish the same identities.
func rows(items []envelope.Item, named, sheet string, header bool) ([]envelope.Item, error) {
	var published []envelope.Item
	for _, item := range items {
		name, err := attachment(item, named)
		if err != nil {
			return nil, err
		}
		content, err := os.ReadFile(filepath.Join(inDir, in, name))
		if err != nil {
			return nil, fmt.Errorf("item %s: the workbook %s could not be read: %w", item.ID, name, err)
		}
		table, err := readSheet(content, sheet)
		if err != nil {
			return nil, fmt.Errorf("item %s: %s: %w", item.ID, name, err)
		}
		if len(table) == 0 {
			continue
		}
		var columns []string
		body := table
		if header {
			for _, v := range table[0] {
				columns = append(columns, text(v))
			}
			body = table[1:]
		}
		for i := range columns {
			if columns[i] == "" {
				// A header cell left empty still names a column, by its letter, so that its values
				// are not dropped and no two columns share the key "".
				columns[i] = letters(i)
			}
		}
		for n, row := range body {
			data := make(map[string]any, len(row))
			for i, v := range row {
				key := "c" + strconv.Itoa(i)
				if header {
					key = columns[i]
				}
				data[key] = v
			}
			published = append(published, envelope.Item{ID: item.ID + "#" + strconv.Itoa(n), Data: data, Files: []envelope.File{}})
		}
	}
	return published, nil
}

// table writes the batch into one workbook: one column per key any item carries, sorted, one row
// per item, and the header unless the step asks for none.
func table(task envelope.Task, items []envelope.Item, name, sheet string, header bool) ([]envelope.Item, error) {
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return nil, fmt.Errorf("table_name %q is not a file name: an artifact is named under its port and carries no separator", name)
	}
	if sheet == "" {
		sheet = "Sheet1"
	}
	data := make([]map[string]any, len(items))
	for i, item := range items {
		data[i] = item.Data
	}
	columns := keys(data)
	var grid [][]cell
	if header {
		row := make([]cell, len(columns))
		for i, c := range columns {
			row[i] = c
		}
		grid = append(grid, row)
	}
	for _, d := range data {
		row := make([]cell, len(columns))
		for i, c := range columns {
			row[i] = d[c]
		}
		grid = append(grid, row)
	}
	content, err := writeSheet(sheet, grid)
	if err != nil {
		return nil, err
	}
	file, err := task.Attach(out, name, mediaType, content)
	if err != nil {
		return nil, err
	}
	cols := make([]any, len(columns))
	for i, c := range columns {
		cols[i] = c
	}
	// The identity is the digest of the workbook, which epoch keeps the same for the same batch.
	return []envelope.Item{{
		ID:    "table-" + envelope.Digest(content)[:12],
		Data:  map[string]any{"rows": len(items), "columns": cols, "bytes": len(content)},
		Files: []envelope.File{file},
	}}, nil
}

// attachment is the workbook an item carries, by name or by being its only file.
func attachment(item envelope.Item, named string) (string, error) {
	var names []string
	for _, f := range item.Files {
		names = append(names, f.Name)
		if named != "" && f.Name == named {
			return named, nil
		}
	}
	switch {
	case named != "":
		return "", fmt.Errorf("item %s attaches no file called %s: it carries %v", item.ID, named, names)
	case len(names) == 1:
		return names[0], nil
	case len(names) == 0:
		return "", fmt.Errorf("item %s attaches no file: xlsx reads a workbook an item attached", item.ID)
	}
	return "", fmt.Errorf("item %s attaches %d files, %v: the step names which one is the workbook as file", item.ID, len(names), names)
}

// text is a header cell as a column's name.
func text(v cell) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}
