package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// What holds the catalog together as a catalog, rather than as four programs that happen to share
// a repository: every brick is a directory laid out the same way, its manifest is one the engine
// will accept, and its cases say what each of its ports carries. agk brick test runs the cases
// against the image; this holds the documents before any image exists, so that a manifest the
// engine would refuse fails here and not on somebody's first pull.

// notBricks are the directories of the repository that are not a brick. Any other directory is
// one, and one missing its manifest fails rather than being skipped, since a brick nobody checks
// is the one that ships broken.
var notBricks = []string{"internal"}

// brick is one directory of the catalog and what its documents say.
type brick struct {
	name     string
	manifest map[string]any
	cases    []string
}

func bricks(t *testing.T) []brick {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []brick
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || slices.Contains(notBricks, e.Name()) {
			continue
		}
		b := brick{name: e.Name()}
		doc, err := os.ReadFile(filepath.Join(b.name, "brick.yaml"))
		if err != nil {
			t.Errorf("%s is a directory of the catalog with no brick.yaml: every directory but %s is a brick, and a brick carries its manifest", b.name, strings.Join(notBricks, ", "))
			continue
		}
		if err := yaml.Unmarshal(doc, &b.manifest); err != nil {
			t.Errorf("%s/brick.yaml is not YAML: %s", b.name, err)
			continue
		}
		cases, _ := os.ReadDir(filepath.Join(b.name, "cases"))
		for _, c := range cases {
			if c.IsDir() {
				b.cases = append(b.cases, c.Name())
			}
		}
		out = append(out, b)
	}
	if len(out) == 0 {
		t.Fatal("no brick was found, which means this test is reading the wrong directory")
	}
	return out
}

// Every brick is a directory holding its source, its manifest and its cases, and is named as its
// manifest names it, since the directory is what the matrix builds and the name is what a step
// pulls.
func TestEveryBrickIsLaidOutTheSameWay(t *testing.T) {
	for _, b := range bricks(t) {
		if name := lookup(b.manifest, "metadata", "name"); name != b.name {
			t.Errorf("%s/brick.yaml names the brick %v, and the directory is the name the image is published under", b.name, name)
		}
		if _, err := os.Stat(filepath.Join(b.name, "main.go")); err != nil {
			if _, err := os.Stat(filepath.Join(b.name, "Dockerfile")); err != nil {
				t.Errorf("%s has neither a main.go for the shared Dockerfile to build nor a Dockerfile of its own", b.name)
			}
		}
		if len(b.cases) == 0 {
			t.Errorf("%s has no cases: a brick with no example envelopes is one agk brick test cannot run and a catalog page cannot show", b.name)
		}
	}
}

// Every manifest validates against the brick schema of agentiik/schemas, the one the engine
// reads a manifest with, so that the refusals are the engine's and not a second list kept here.
// The schema refuses a root account among the rest.
//
// The schema is read from BRICK_SCHEMA, which CI sets to a checkout of agentiik/schemas at the
// matching ref, or else from a sibling checkout. Without either the test says so and skips, as
// building one brick on a laptop should not need the whole organisation; CI sets it, so the
// catalog is never published unchecked.
func TestEveryManifestIsOneTheEngineAccepts(t *testing.T) {
	path := os.Getenv("BRICK_SCHEMA")
	if path == "" {
		path = filepath.Join("..", "schemas", "brick.schema.json")
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			t.Skip("no brick schema: set BRICK_SCHEMA to agentiik/schemas/brick.schema.json, or check that repository out beside this one")
		}
	}
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("BRICK_SCHEMA: %s", err)
	}
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("%s is not JSON: %s", path, err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource("brick.schema.json", parsed); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile("brick.schema.json")
	if err != nil {
		t.Fatalf("the brick schema does not compile: %s", err)
	}
	for _, b := range bricks(t) {
		// Through JSON, since the validator reads JSON values and YAML integers arrive as
		// uint64.
		j, err := json.Marshal(b.manifest)
		if err != nil {
			t.Fatal(err)
		}
		v, err := jsonschema.UnmarshalJSON(bytes.NewReader(j))
		if err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(v); err != nil {
			t.Errorf("%s/brick.yaml is refused by the brick schema:\n%s", b.name, err)
		}
	}
}

var userLine = regexp.MustCompile(`(?m)^USER\s+(\S+)\s*$`)

// The account a manifest declares is the account its image runs as. The engine checks the
// manifest, and publication checks the image, so the two have to say the same thing or one of
// the checks is reading a fiction.
func TestEveryImageRunsAsItsManifestSays(t *testing.T) {
	for _, b := range bricks(t) {
		dockerfile := filepath.Join(b.name, "Dockerfile")
		if _, err := os.Stat(dockerfile); err != nil {
			dockerfile = "Dockerfile"
		}
		doc, err := os.ReadFile(dockerfile)
		if err != nil {
			t.Fatal(err)
		}
		users := userLine.FindAllSubmatch(doc, -1)
		if len(users) == 0 {
			t.Errorf("%s sets no USER, and an image with none runs %s as root", dockerfile, b.name)
			continue
		}
		image := string(users[len(users)-1][1])
		if declared := lookup(b.manifest, "spec", "runtime", "user"); declared != image {
			t.Errorf("%s/brick.yaml declares the account %v and %s runs it as %s", b.name, declared, dockerfile, image)
		}
	}
}

// Every port a manifest declares is one its cases show: an input some case feeds, an output some
// case expects. A declared output no case writes is either dead or untested, and the manifest is
// what the catalog page and brick.get publish, so a port it promises has an example behind it.
// The other way round, a case naming a port the manifest does not declare is a case for some
// other brick.
func TestEveryPortHasAnExample(t *testing.T) {
	for _, b := range bricks(t) {
		for _, side := range []struct{ key, dir string }{{"inputs", "in"}, {"outputs", "out"}} {
			declared, _ := lookup(b.manifest, "spec", side.key).(map[string]any)
			shown := map[string]bool{}
			for _, c := range b.cases {
				files, _ := os.ReadDir(filepath.Join(b.name, "cases", c, side.dir))
				for _, f := range files {
					port, ok := strings.CutSuffix(f.Name(), ".json")
					if !ok {
						t.Errorf("%s/cases/%s/%s/%s is not an envelope: a port's file is <port>.json", b.name, c, side.dir, f.Name())
						continue
					}
					if _, ok := declared[port]; !ok {
						t.Errorf("%s/cases/%s/%s/%s names the port %s, which %s/brick.yaml does not declare under %s", b.name, c, side.dir, f.Name(), port, b.name, side.key)
					}
					var e struct {
						Meta  map[string]any `json:"meta"`
						Items []any          `json:"items"`
					}
					doc, err := os.ReadFile(filepath.Join(b.name, "cases", c, side.dir, f.Name()))
					if err == nil {
						err = json.Unmarshal(doc, &e)
					}
					if err != nil || e.Meta == nil || e.Items == nil {
						t.Errorf("%s/cases/%s/%s/%s is not an envelope with meta and items: %v", b.name, c, side.dir, f.Name(), err)
					}
					shown[port] = true
				}
			}
			for port := range declared {
				if !shown[port] {
					t.Errorf("%s declares the %s port %s and no case under %s/cases has a %s/%s.json", b.name, side.dir, port, b.name, side.dir, port)
				}
			}
		}
	}
}

// lookup follows keys through a decoded document, and answers nil where any of them is missing.
func lookup(doc any, keys ...string) any {
	for _, k := range keys {
		m, ok := doc.(map[string]any)
		if !ok {
			return nil
		}
		doc = m[k]
	}
	return doc
}
