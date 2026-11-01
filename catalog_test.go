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

// notBricks are the directories of the repository that are not a brick: the shared Go package,
// and the workflow that runs the catalog through the engine. Any other directory is one, and one
// missing its manifest fails rather than being skipped, since a brick nobody checks is the one
// that ships broken.
var notBricks = []string{"internal", "testdata"}

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

// Every manifest names the same version, the one the catalog is released under: a tag publishes
// every brick under it, changed or not, and publish.yml refuses a tag the manifests disagree with,
// so a disagreement is caught here first rather than by a failed release.
func TestEveryManifestNamesOneVersion(t *testing.T) {
	seen := map[string][]string{}
	for _, b := range bricks(t) {
		v, _ := lookup(b.manifest, "metadata", "version").(string)
		seen[v] = append(seen[v], b.name)
	}
	if len(seen) > 1 {
		t.Errorf("the manifests name more than one version, and one version names the catalog: %v", seen)
	}
}

var fromLine = regexp.MustCompile(`(?m)^FROM\s+(?:--platform=\S+\s+)?(\S+)`)

// Every base image is pinned by digest, so that a brick's contents change only when its source
// does: a tag is a pointer its publisher moves, and an image built on a moved tag is a different
// brick under the same version. scratch is no image and has nothing to pin. Dependabot moves the
// digests, each move a pull request somebody reads.
func TestEveryBaseImageIsPinnedByDigest(t *testing.T) {
	dockerfiles, err := filepath.Glob("*/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range append(dockerfiles, "Dockerfile") {
		doc, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range fromLine.FindAllSubmatch(doc, -1) {
			if base := string(m[1]); base != "scratch" && !strings.Contains(base, "@sha256:") {
				t.Errorf("%s builds from %s, a tag: pin it by digest", path, base)
			}
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

// Every parameter, port and definition a manifest publishes carries a description and examples,
// and every member of an object it describes carries a description of its own. The manifest is the
// only source of the catalog page, brick.get and agentiik://brick/{name}, so what it leaves unsaid
// is unsaid everywhere a person or a client looks for it. Each example is also held to the schema
// it illustrates, since an example the brick would refuse teaches the wrong thing with authority.
//
// The members need no examples of their own: the example of the object they belong to shows them.
func TestEveryKeywordIsDocumented(t *testing.T) {
	for _, b := range bricks(t) {
		j, err := json.Marshal(b.manifest)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(j))
		if err != nil {
			t.Fatal(err)
		}
		c := jsonschema.NewCompiler()
		c.DefaultDraft(jsonschema.Draft2020)
		if err := c.AddResource("brick.json", doc); err != nil {
			t.Fatal(err)
		}
		m := doc.(map[string]any)

		// explained holds one schema to a description and, where asked, examples it accepts.
		explained := func(where, pointer string, s map[string]any, examples bool) {
			if d, _ := s["description"].(string); strings.TrimSpace(d) == "" {
				t.Errorf("%s/brick.yaml: %s has no description", b.name, where)
			}
			members(t, b.name, where, s)
			if !examples {
				return
			}
			ex, _ := s["examples"].([]any)
			if len(ex) == 0 {
				t.Errorf("%s/brick.yaml: %s has no examples", b.name, where)
				return
			}
			schema, err := c.Compile("brick.json#" + pointer)
			if err != nil {
				t.Errorf("%s/brick.yaml: %s does not compile: %s", b.name, where, err)
				return
			}
			for i, e := range ex {
				if err := schema.Validate(e); err != nil {
					t.Errorf("%s/brick.yaml: example %d of %s is one the schema refuses:\n%s", b.name, i, where, err)
				}
			}
		}

		params, _ := lookup(m, "spec", "params").(map[string]any)
		for name, p := range params {
			p, _ := p.(map[string]any)
			// required: true is the manifest's own keyword and not JSON Schema's, so the schema the
			// examples are held to is the parameter without it, added as a resource of its own.
			plain := map[string]any{}
			for k, v := range p {
				if _, flag := v.(bool); k == "required" && flag {
					continue
				}
				plain[k] = v
			}
			if err := c.AddResource("param-"+name+".json", plain); err != nil {
				t.Fatal(err)
			}
			if d, _ := p["description"].(string); strings.TrimSpace(d) == "" {
				t.Errorf("%s/brick.yaml: the parameter %s has no description", b.name, name)
			}
			ex, _ := p["examples"].([]any)
			if len(ex) == 0 {
				t.Errorf("%s/brick.yaml: the parameter %s has no examples", b.name, name)
				continue
			}
			schema, err := c.Compile("param-" + name + ".json")
			if err != nil {
				t.Errorf("%s/brick.yaml: the parameter %s does not compile: %s", b.name, name, err)
				continue
			}
			for i, e := range ex {
				if err := schema.Validate(e); err != nil {
					t.Errorf("%s/brick.yaml: example %d of the parameter %s is one its schema refuses:\n%s", b.name, i, name, err)
				}
			}
		}

		for _, side := range []string{"inputs", "outputs"} {
			ports, _ := lookup(m, "spec", side).(map[string]any)
			for port := range ports {
				where := "the port " + port + " of " + side
				s, _ := lookup(ports, port, "schema").(map[string]any)
				if s == nil {
					t.Errorf("%s/brick.yaml: %s has no schema, and the catalog has nothing to show of it", b.name, where)
					continue
				}
				// A port that refers to a definition is documented by the definition, which is
				// held below.
				if _, ok := s["$ref"]; ok {
					continue
				}
				explained(where, "/spec/"+side+"/"+port+"/schema", s, true)
			}
		}

		definitions, _ := lookup(m, "spec", "definitions").(map[string]any)
		for name, d := range definitions {
			d, _ := d.(map[string]any)
			explained("the definition "+name, "/spec/definitions/"+name, d, true)
		}
	}
}

// members holds every member of an object schema to a description, through nested objects and
// arrays of them.
func members(t *testing.T, brick, where string, s map[string]any) {
	t.Helper()
	props, _ := s["properties"].(map[string]any)
	for name, p := range props {
		p, _ := p.(map[string]any)
		if d, _ := p["description"].(string); strings.TrimSpace(d) == "" {
			t.Errorf("%s/brick.yaml: the member %s of %s has no description", brick, name, where)
		}
		members(t, brick, where+"."+name, p)
	}
	if items, ok := s["items"].(map[string]any); ok {
		members(t, brick, where+"[]", items)
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
