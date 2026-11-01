package catalog

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The exit code is the one verdict a brick gives, and the runner reads its band and nothing
// else: 1 to 99 an application failure, 100 to 119 transient and retried, 120 invalid input and
// never retried, the rest the runner's and the runtime's. These hold every brick to leaving by
// those bands alone and to showing, in a case agk brick test runs, that it says 120 for input it
// cannot use.

var (
	osExit   = regexp.MustCompile(`\bos\.Exit\(`)
	die      = regexp.MustCompile(`\benvelope\.Die\(\s*([^,]+),`)
	constant = regexp.MustCompile(`^envelope\.(Failed|Transient|Invalid)$`)
)

// A brick leaves through envelope.Die and one of its named codes, never os.Exit and never a
// number: Die is where a code outside the bands is caught, and a name says which band was meant.
func TestEveryBrickLeavesThroughANamedBand(t *testing.T) {
	sources, err := filepath.Glob("*/*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		doc, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if osExit.Match(doc) {
			t.Errorf("%s calls os.Exit: a brick leaves through envelope.Die, which refuses a code outside its bands", path)
		}
		for _, m := range die.FindAllSubmatch(doc, -1) {
			if !constant.Match(m[1]) {
				t.Errorf("%s leaves with %s: name the band, envelope.Failed, envelope.Transient or envelope.Invalid", path, m[1])
			}
		}
	}
}

// Every case's exit file names a code a brick may leave by, and every brick has a case that
// leaves with 120, since every brick is given parameters and input it can refuse and the
// runner never retries that refusal.
func TestEveryCaseExpectsABandAndEveryBrickRefusesBadInput(t *testing.T) {
	manifests, err := filepath.Glob("*/brick.yaml")
	if err != nil {
		t.Fatal(err)
	}
	refuses := map[string]bool{}
	exits, err := filepath.Glob("*/cases/*/exit")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range exits {
		doc, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		code, err := strconv.Atoi(strings.TrimSpace(string(doc)))
		if err != nil || code < 0 || code > 120 {
			t.Errorf("%s expects %q: a brick leaves with 0, 1 to 99, 100 to 119 or 120, and the rest is the runner's or the runtime's", path, strings.TrimSpace(string(doc)))
			continue
		}
		if code == 120 {
			refuses[strings.Split(filepath.ToSlash(path), "/")[0]] = true
		}
	}
	for _, m := range manifests {
		if b := filepath.Dir(m); !refuses[b] {
			t.Errorf("%s has no case leaving with 120: show the input it refuses, so that the refusal is one the runner will not retry", b)
		}
	}
}
