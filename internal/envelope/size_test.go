package envelope

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The size rules are measured by the runner on what a brick left, and a breach is exit code 121
// charged to the brick. These hold the half a brick owes: nothing inline above inline_max_bytes,
// and no envelope the runner would refuse.

func TestAValueAboveTheThresholdIsSpilledAndOneAtItStaysInline(t *testing.T) {
	task := Task{RunID: "01M2AAZ9G62NQXFAFCXKRPJEH5", Step: "render", Attempt: 1, Root: t.TempDir(), Limits: Limits{InlineMax: 16}}
	at := strings.Repeat("a", 14)    // "aaaaaaaaaaaaaa" is 16 bytes encoded
	above := strings.Repeat("b", 15) // 17 bytes encoded
	items := []Item{{ID: "inv/7", Data: map[string]any{"at": at, "body": above}, Files: []File{}}}
	if err := task.Out("out", items); err != nil {
		t.Fatalf("Out: %s", err)
	}

	var e Envelope
	read(t, filepath.Join(task.Root, "agk/out/ports/out.json"), &e)
	got := e.Items[0]
	if got.Data["at"] != at {
		t.Errorf("a value of exactly inline_max_bytes left data: %v", got.Data)
	}
	if _, ok := got.Data["body"]; ok {
		t.Errorf("a value above inline_max_bytes stayed inline: %v", got.Data)
	}
	if len(got.Files) != 1 {
		t.Fatalf("the spilled value is not one files[] entry: %+v", got.Files)
	}
	f := got.Files[0]
	// The engine's own name for it, the identifier and the field filed down to a segment.
	if f.Name != "inv_7-body.json" || f.MediaType != "application/json" || f.URI != "agk://run/01M2AAZ9G62NQXFAFCXKRPJEH5/render/out/inv_7-body.json" {
		t.Errorf("the spilled value is filed as %+v", f)
	}
	b, err := os.ReadFile(filepath.Join(task.Root, "agk/out/files", f.Name))
	if err != nil {
		t.Fatal(err)
	}
	var back string
	if err := json.Unmarshal(b, &back); err != nil || back != above {
		t.Errorf("the artifact holds %q and not the JSON encoding of the value", b)
	}
	if f.Size != int64(len(b)) || f.SHA256 != Digest(b) {
		t.Errorf("the entry says %d bytes and %s, and the artifact is %d bytes", f.Size, f.SHA256, len(b))
	}
}

// /agk/out/files is one directory for every port, so a name one port spilled under is taken
// for the next.
func TestTwoPortsNeverSpillToOneName(t *testing.T) {
	task := Task{RunID: "r", Step: "s", Attempt: 1, Root: t.TempDir(), Limits: Limits{InlineMax: 4}}
	for _, port := range []string{"ok", "invalid"} {
		items := []Item{{ID: "a", Data: map[string]any{"note": port + "-heavy"}, Files: []File{}}}
		if err := task.Out(port, items); err != nil {
			t.Fatalf("Out(%s): %s", port, err)
		}
	}
	var ok, invalid Envelope
	read(t, filepath.Join(task.Root, "agk/out/ports/ok.json"), &ok)
	read(t, filepath.Join(task.Root, "agk/out/ports/invalid.json"), &invalid)
	if ok.Items[0].Files[0].Name != "a-note.json" || invalid.Items[0].Files[0].Name != "a-note-2.json" {
		t.Errorf("the two ports spilled to %s and %s", ok.Items[0].Files[0].Name, invalid.Items[0].Files[0].Name)
	}
}

func TestMoreItemsThanMaxItemsAreRefusedBeforeAnythingIsWritten(t *testing.T) {
	task := Task{RunID: "r", Step: "s", Attempt: 1, Root: t.TempDir(), Limits: Limits{MaxItems: 2}}
	items := []Item{{ID: "a", Data: map[string]any{}}, {ID: "b", Data: map[string]any{}}, {ID: "c", Data: map[string]any{}}}
	err := task.Out("out", items)
	if !errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), "max_items") {
		t.Fatalf("three items under max_items 2 were answered %v", err)
	}
	if _, err := os.Stat(filepath.Join(task.Root, "agk/out/ports/out.json")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the refused envelope was written all the same, and the runner would have refused it with 121")
	}
}

// What spilling cannot fix: many items, each under the threshold, together over the envelope's
// own limit.
func TestAnEnvelopeHeavierThanEnvelopeMaxIsRefused(t *testing.T) {
	task := Task{RunID: "r", Step: "s", Attempt: 1, Root: t.TempDir(), Limits: Limits{InlineMax: 64, EnvelopeMax: 512}}
	var items []Item
	for i := range 20 {
		items = append(items, Item{ID: strings.Repeat("x", i+1), Data: map[string]any{"v": "0123456789"}})
	}
	err := task.Out("out", items)
	if !errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), "envelope_max_bytes") {
		t.Fatalf("twenty items over envelope_max_bytes 512 were answered %v", err)
	}
}

func TestATaskReadFromTheEnvironmentIsHeldToTheDocumentedDefaults(t *testing.T) {
	if got := Read().Limits; got != (Limits{InlineMax: 262144, EnvelopeMax: 4194304, MaxItems: 100000}) {
		t.Errorf("the defaults are %+v", got)
	}
}
