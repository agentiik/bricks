// Command aggregate groups items by the value of one field, the other half of split.
//
// A group is published as one item: the key's value under the key's field, how many items it
// gathers as count, and their data in arrival order under the collect field. Its files are the
// files of every item it gathers, once each. Groups are published in the order their first item
// arrived, so the output of one batch is one sequence and not whatever order a map iterates in.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/agentiik/bricks/internal/envelope"
)

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
	var key string
	if _, err := envelope.Into(params, "key", &key); err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}
	if key == "" {
		envelope.Die(envelope.Invalid, "no key: aggregate groups items by the value of a field, and the step names the field")
	}
	collect := "items"
	if _, err := envelope.Into(params, "collect", &collect); err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}
	if collect == "" || collect == key || collect == "count" {
		envelope.Die(envelope.Invalid, "collect is %q: it names the field a group holds its items' data in, and that is neither empty, nor the key %q, nor count", collect, key)
	}

	batch, err := task.In(in)
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}
	groups, err := group(batch.Items, key, collect)
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}
	if err := task.Out(out, groups); err != nil {
		envelope.Die(envelope.Failed, "%s", err)
	}
	fmt.Fprintf(os.Stderr, "%d items in, %d groups out\n", len(batch.Items), len(groups))
}

// group gathers items by the JSON of their key's value, which is what makes 1 and "1" two groups
// and two equal strings one.
func group(items []envelope.Item, key, collect string) ([]envelope.Item, error) {
	var order []string
	byValue := map[string]*envelope.Item{}
	seen := map[string]map[string]bool{}
	for _, item := range items {
		value, ok := item.Data[key]
		if !ok {
			return nil, fmt.Errorf("item %s has no %s, and aggregate groups by it", item.ID, key)
		}
		switch value.(type) {
		case map[string]any, []any:
			return nil, fmt.Errorf("item %s has a %s that is %s, and a group is keyed by a string, a number, a boolean or null", item.ID, key, kind(value))
		}
		b, _ := json.Marshal(value)
		k := string(b)
		g, ok := byValue[k]
		if !ok {
			// The identity is the value itself, as a string for a string and as its JSON for
			// anything else, so that the group of order a1 is a1 on every run.
			id := k
			if s, isString := value.(string); isString {
				id = s
			}
			g = &envelope.Item{ID: id, Data: map[string]any{key: value, "count": 0, collect: []any{}}, Files: []envelope.File{}}
			byValue[k] = g
			seen[k] = map[string]bool{}
			order = append(order, k)
		}
		g.Data["count"] = g.Data["count"].(int) + 1
		g.Data[collect] = append(g.Data[collect].([]any), item.Data)
		for _, f := range item.Files {
			// One entry per artifact: the same bytes under the same name, carried by two items
			// of one group, are one file of the group.
			if id := f.Name + "\x00" + f.SHA256; !seen[k][id] {
				seen[k][id] = true
				g.Files = append(g.Files, f)
			}
		}
	}
	out := make([]envelope.Item, 0, len(order))
	for _, k := range order {
		out = append(out, *byValue[k])
	}
	return out, nil
}

func kind(v any) string {
	if _, ok := v.([]any); ok {
		return "an array"
	}
	return "an object"
}
