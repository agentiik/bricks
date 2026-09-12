package envelope

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The four bricks of this catalog read and write through this package, so what it gets wrong
// they all get wrong. What is worth holding it to is the half of the contract a brick cannot see
// the consequence of: the metadata the runner refuses an envelope for, and the count.

// count is how many items the envelope holds, and the runner refuses an envelope that says
// otherwise. It is set here rather than taken from the caller for exactly that reason.
func TestTheCountIsTheItemsAndNotWhatAnybodySaid(t *testing.T) {
	task := Task{RunID: "01M2AAZ9G62NQXFAFCXKRPJEH5", Step: "charge", Attempt: 2, Root: t.TempDir()}
	items := []Item{
		{ID: "a", Data: map[string]any{"ref": "a1"}, Files: []File{}},
		{ID: "b", Data: map[string]any{"ref": "b2"}, Files: []File{}},
	}
	if err := task.Out("ok", items); err != nil {
		t.Fatalf("Out: %s", err)
	}

	var e Envelope
	read(t, filepath.Join(task.Root, "agk/out/ports/ok.json"), &e)
	if e.Meta.Count != 2 {
		t.Errorf("count says %d and the envelope holds %d", e.Meta.Count, len(e.Items))
	}
	// The metadata a container writes is the metadata it was given, and an envelope that
	// disagrees on any of these four is refused rather than quietly corrected.
	if e.Meta.RunID != task.RunID || e.Meta.Step != task.Step || e.Meta.Attempt != 2 || e.Meta.Port != "ok" {
		t.Errorf("the metadata is %+v", e.Meta)
	}
}

// A port written with no items is an empty envelope rather than a null one, because the document
// has to be an envelope before it is collected and items: null is not one.
func TestAPortWithNothingOnItIsAnEmptyEnvelopeAndNotANullOne(t *testing.T) {
	task := Task{RunID: "01M2AAZ9G62NQXFAFCXKRPJEH5", Step: "charge", Attempt: 1, Root: t.TempDir()}
	if err := task.Out("rejected", nil); err != nil {
		t.Fatalf("Out: %s", err)
	}
	written, err := os.ReadFile(filepath.Join(task.Root, "agk/out/ports/rejected.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `"items":[]`) {
		t.Errorf("the envelope reads %s", written)
	}
}

// An input port nobody wrote is a batch of nothing and not a failure. An edge above that
// published nothing is the ordinary case, and a brick that refused it would fail a run for the
// absence of work.
func TestAnInputPortThatIsNotThereIsABatchOfNothing(t *testing.T) {
	task := Task{RunID: "01M2AAZ9G62NQXFAFCXKRPJEH5", Step: "charge", Attempt: 1, Root: t.TempDir()}
	batch, err := task.In("in")
	if err != nil {
		t.Fatalf("a port that was never written was refused: %s", err)
	}
	if len(batch.Items) != 0 || batch.Meta.Port != "in" {
		t.Errorf("the batch is %+v", batch)
	}
}

// An artifact's digest and size travel in the item that attached it, and the store is addressed
// by exactly that digest: a second computation of the same bytes has to answer the same way or
// it is a second object for one content.
func TestAnAttachedFileCarriesItsOwnDigestAndLogicalUri(t *testing.T) {
	task := Task{RunID: "01M2AAZ9G62NQXFAFCXKRPJEH5", Step: "total", Attempt: 1, Root: t.TempDir()}
	content := []byte("ref=a1\namount=1200\n")
	file, err := task.Attach("ok", "receipt.txt", "text/plain", content)
	if err != nil {
		t.Fatalf("Attach: %s", err)
	}
	if file.Size != int64(len(content)) {
		t.Errorf("the size is %d and the bytes are %d", file.Size, len(content))
	}
	if file.SHA256 != Digest(content) || len(file.SHA256) != 64 {
		t.Errorf("the digest is %q", file.SHA256)
	}
	if file.URI != "agk://run/01M2AAZ9G62NQXFAFCXKRPJEH5/total/ok/receipt.txt" {
		t.Errorf("the uri is %q", file.URI)
	}
	landed, err := os.ReadFile(filepath.Join(task.Root, "agk/out/files/receipt.txt"))
	if err != nil || string(landed) != string(content) {
		t.Errorf("the bytes landed as %q, %v", landed, err)
	}
}

// A parameter that was not supplied and one supplied as a zero are two different things, and a
// brick that cannot tell them apart cannot implement a default.
func TestAParameterThatIsAbsentIsNotAParameterThatIsZero(t *testing.T) {
	params := map[string]json.RawMessage{"retries": json.RawMessage("0")}
	n := 3
	supplied, err := Into(params, "retries", &n)
	if err != nil || !supplied || n != 0 {
		t.Errorf("an explicit zero read as %d, supplied %v, %v", n, supplied, err)
	}
	n = 3
	supplied, err = Into(params, "timeout", &n)
	if err != nil || supplied || n != 3 {
		t.Errorf("an absent parameter changed the value to %d, supplied %v, %v", n, supplied, err)
	}
	// A value of the wrong shape is the step's mistake and says so, rather than arriving as
	// a zero the brick then acts on.
	var s string
	if _, err := Into(params, "retries", &s); err == nil {
		t.Error("a number read into a string was accepted")
	}
}

// AGK_OUT_PORTS is comma separated, and a brick asks before writing a port it only sometimes
// has: a file under a name the step does not declare is refused by the collection.
func TestTheDeclaredPortsAreReadFromTheEnvironment(t *testing.T) {
	t.Setenv("AGK_RUN_ID", "01M2AAZ9G62NQXFAFCXKRPJEH5")
	t.Setenv("AGK_STEP", "charge")
	t.Setenv("AGK_ATTEMPT", "7")
	t.Setenv("AGK_OUT_PORTS", "ok,rejected")
	task := Read()
	if task.Attempt != 7 {
		t.Errorf("the attempt is %d", task.Attempt)
	}
	if !task.Declared("ok") || !task.Declared("rejected") || task.Declared("error") {
		t.Errorf("the declared ports are %v", task.Ports)
	}

	// A missing attempt is the first one rather than an error, so that a brick run by hand
	// to see what it does says something about the brick.
	t.Setenv("AGK_ATTEMPT", "")
	if got := Read().Attempt; got != 1 {
		t.Errorf("an absent attempt read as %d", got)
	}
}

func read(t *testing.T, path string, into any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatal(err)
	}
}
