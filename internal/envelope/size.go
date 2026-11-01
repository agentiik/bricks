package envelope

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Limits are the size rules an envelope is held to when it is collected. A runner refuses an
// envelope breaking any of them with exit code 121, charged to the brick, so a brick that keeps
// to them itself is told what went wrong in its own words rather than the runner's.
//
// The values are the documented defaults. The contract gives a brick no way to read the ones
// its installation chose, so a brick takes these and a step that runs under tighter ones says
// so through a parameter, as csv and http-request read inline_max_bytes.
type Limits struct {
	// InlineMax is inline_max_bytes: above it, a value of data travels as an artifact.
	InlineMax int
	// EnvelopeMax is envelope_max_bytes, the most a serialised envelope may weigh.
	EnvelopeMax int
	// MaxItems is max_items, the most items one envelope may hold.
	MaxItems int
}

// DefaultLimits are the documentation's: 256 KiB inline, 4 MiB an envelope, 100 000 items.
var DefaultLimits = Limits{InlineMax: 262144, EnvelopeMax: 4194304, MaxItems: 100000}

// orDefault reads a limit of zero as the default, so that a Task written as a literal in a
// test is held to the same rules as one Read from the environment.
func (l Limits) orDefault() Limits {
	if l.InlineMax <= 0 {
		l.InlineMax = DefaultLimits.InlineMax
	}
	if l.EnvelopeMax <= 0 {
		l.EnvelopeMax = DefaultLimits.EnvelopeMax
	}
	if l.MaxItems <= 0 {
		l.MaxItems = DefaultLimits.MaxItems
	}
	return l
}

// ErrTooLarge is what Out answers for an envelope that would break envelope_max_bytes or
// max_items once its heavy values are spilled. There is nothing a brick can spill further,
// and nothing it may do with the rest without changing what its port means, so it fails
// rather than publish what the runner would refuse: a step that meets it splits its batch
// upstream, by fan_out or by a smaller page.
var ErrTooLarge = errors.New("the envelope breaks the size rules")

// spill moves every top-level value of data heavier than InlineMax into an artifact named
// <item>-<field>.json, holding the value's JSON encoding, and references it in files[]. It is
// the rule the engine spills its own values by, field for field and name for name, so that an
// item a brick spilled reads the same as one the engine did.
//
// The unit is a top-level field because it is the unit a port's schema names and a consumer
// asks for; a field under the threshold holds nothing above it either, so moving the heavy
// fields leaves no value inline the runner would refuse.
func (t Task) spill(port string, items []Item, inlineMax int) error {
	for i := range items {
		item := &items[i]
		fields := make([]string, 0, len(item.Data))
		for k := range item.Data {
			fields = append(fields, k)
		}
		sort.Strings(fields)
		for _, field := range fields {
			b, err := json.Marshal(item.Data[field])
			if err != nil {
				return fmt.Errorf("item %s: the field %s cannot be encoded: %w", item.ID, field, err)
			}
			if len(b) <= inlineMax {
				continue
			}
			file, err := t.Attach(port, t.spilledName(item.ID, i, field), "application/json", b)
			if err != nil {
				return err
			}
			delete(item.Data, field)
			item.Files = append(item.Files, file)
		}
	}
	return nil
}

// spilledName is <item>-<field>.json, each filed down to what one segment of a path may hold,
// and suffixed -2, -3 where the name is already an artifact of this task: /agk/out/files is
// one directory for every port, so the name has to be free across all of them.
func (t Task) spilledName(id string, rank int, field string) string {
	if id == "" {
		id = fmt.Sprintf("item-%d", rank)
	}
	base := segment(id) + "-" + segment(field)
	name := base + ".json"
	for n := 2; ; n++ {
		if _, err := os.Stat(filepath.Join(t.at(outFiles), name)); errors.Is(err, os.ErrNotExist) {
			return name
		}
		name = fmt.Sprintf("%s-%d.json", base, n)
	}
}

// segment files a string down to letters, digits, dot, underscore and hyphen, at most 64 of
// them, as the engine files the name of a value it spills.
func segment(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 64 {
			break
		}
	}
	out := b.String()
	if strings.Trim(out, ".") == "" {
		out = "field"
	}
	return out
}
