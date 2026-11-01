// Command template renders a text template per item, into a field of its data or a file it
// carries.
//
// The syntax is Go's text/template, the one this brick is written in, which a reader can look up
// rather than learn here. Missing keys are errors: a template that rendered an absent field as
// "<no value>" or as nothing would send a mail with a hole in it, and the step would have
// succeeded. Such an item is invalid input, 120, since it will be as incomplete on every attempt.
package main

import (
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/agentiik/bricks/internal/envelope"
	"github.com/agentiik/bricks/internal/render"
)

const (
	in  = "in"
	out = "out"
)

type settings struct {
	text      string
	into      string
	field     string
	name      string
	mediaType string
}

func main() {
	task := envelope.Read()
	s, err := configure()
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}
	batch, err := task.In(in)
	if err != nil {
		envelope.Die(envelope.Invalid, "%s", err)
	}

	published := make([]envelope.Item, 0, len(batch.Items))
	for _, item := range batch.Items {
		text, err := render.Render("template", s.text, item)
		if err != nil {
			envelope.Die(envelope.Invalid, "item %s: %s", item.ID, err)
		}
		files := item.Files
		if files == nil {
			files = []envelope.File{}
		}
		data := item.Data
		if data == nil {
			data = map[string]any{}
		}
		if s.into == "field" {
			data[s.field] = text
		} else {
			name, err := render.Render("name", s.name, item)
			if err != nil {
				envelope.Die(envelope.Invalid, "item %s: %s", item.ID, err)
			}
			if name == "" || name != path.Base(name) || strings.HasPrefix(name, ".") {
				envelope.Die(envelope.Invalid, "item %s: the name %q is not a file name: an artifact is one file under its port, with no separator", item.ID, name)
			}
			file, err := task.Attach(out, name, s.mediaType, []byte(text))
			if err != nil {
				envelope.Die(envelope.Failed, "%s", err)
			}
			files = append(append([]envelope.File{}, files...), file)
		}
		published = append(published, envelope.Item{ID: item.ID, Data: data, Files: files})
	}
	if err := task.Out(out, published); err != nil {
		envelope.Die(envelope.Failed, "%s", err)
	}
	fmt.Fprintf(os.Stderr, "%d items rendered into %s\n", len(published), s.into)
}

func configure() (settings, error) {
	params, err := envelope.Params()
	if err != nil {
		return settings{}, err
	}
	s := settings{into: "field", field: "text", mediaType: "text/plain; charset=utf-8"}
	inline, err := envelope.Into(params, "template", &s.text)
	if err != nil {
		return s, err
	}
	var at string
	fromFile, err := envelope.Into(params, "template_path", &at)
	if err != nil {
		return s, err
	}
	switch {
	case inline && fromFile:
		return s, fmt.Errorf("template and template_path were both supplied: a step gives the template inline or names a file of the repository, and one of the two")
	case !inline && !fromFile:
		return s, fmt.Errorf("no template: template renders one per item, given inline as template or as template_path relative to %s", render.RepoDir)
	case fromFile:
		if s.text, err = render.Read("template_path", at); err != nil {
			return s, err
		}
	}
	for name, v := range map[string]*string{"into": &s.into, "field": &s.field, "name": &s.name, "media_type": &s.mediaType} {
		if _, err := envelope.Into(params, name, v); err != nil {
			return s, err
		}
	}
	switch s.into {
	case "field":
		if s.field == "" {
			return s, fmt.Errorf("field is empty: with into field, it names the field the text is written to")
		}
	case "artifact":
		if s.name == "" {
			return s, fmt.Errorf("no name: with into artifact, name says what each item's file is called, {{.ref}}.txt for instance")
		}
	default:
		return s, fmt.Errorf("into is %q: the text goes into a field or an artifact", s.into)
	}
	// Parsed once here, so that a template that does not parse is refused before any item is
	// read, naming the step's mistake rather than the first item's.
	for _, t := range []struct{ what, text string }{{"template", s.text}, {"name", s.name}} {
		if _, err := render.Parse(t.what, t.text, ""); err != nil {
			return s, err
		}
	}
	return s, nil
}
