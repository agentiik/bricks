// Package render fills a text template with one item, the one way every brick of the catalog that
// writes text from an item does it: template, and smtp-send for a message's subject and body.
//
// The syntax is Go's text/template, which a reader can look up rather than learn here. Missing
// keys are errors: a template that rendered an absent field as "<no value>" or as nothing would
// send a mail with a hole in it, and the step would have succeeded.
package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/agentiik/bricks/internal/envelope"
)

// RepoDir is where the workflow repository tree is bound. A path parameter is resolved against it
// and never against the working directory, which is the brick's own.
const RepoDir = "/agk/repo"

// Parse reads a template, with the two functions every template of the catalog has: id, the
// item's identity, and json, a value as JSON.
func Parse(what, text, id string) (*template.Template, error) {
	t, err := template.New(what).Option("missingkey=error").Funcs(template.FuncMap{
		"id": func() string { return id },
		"json": func(v any) (string, error) {
			b, err := json.Marshal(v)
			return string(b), err
		},
	}).Parse(text)
	if err != nil {
		return nil, fmt.Errorf("the %s does not parse: %w", what, err)
	}
	return t, nil
}

// Render fills one template with one item: its data as dot, its identity as id.
func Render(what, text string, item envelope.Item) (string, error) {
	t, err := Parse(what, text, item.ID)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, item.Data); err != nil {
		return "", fmt.Errorf("the %s cannot be filled: %w", what, err)
	}
	return b.String(), nil
}

// Read takes a template out of the repository tree, refusing a path that leaves it. param is
// the parameter that named the path, for the refusal.
func Read(param, at string) (string, error) {
	if at == "" || filepath.IsAbs(at) {
		return "", fmt.Errorf("%s %q names no file of the repository: it is a path relative to %s", param, at, RepoDir)
	}
	full := filepath.Join(RepoDir, at)
	if !strings.HasPrefix(full, RepoDir+string(filepath.Separator)) {
		return "", fmt.Errorf("%s %s leaves %s: a template is a file of the commit this run pinned", param, at, RepoDir)
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("the template could not be read at %s: %w", full, err)
	}
	return string(b), nil
}
