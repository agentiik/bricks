package envelope

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// The paths the contract names. They are constants rather than flags because a brick that
// took them from its command line would be a brick whose contract depends on how a step was
// written.
const (
	inDir    = "/agk/in"
	outPorts = "/agk/out/ports"
	outFiles = "/agk/out/files"
	paramsAt = "/agk/params.json"
)

// Envelope is what travels on a port: what the runner knows about the batch, and the items.
//
// The shape is the documentation's, field for field, including the two members a brick cannot
// invent. meta.produced_at is stamped by the runner, because a port is published when the
// emitting step ends and only the runner is in a position to know that moment; it is written
// as the zero time here so that the document is a valid envelope before it is collected.
type Envelope struct {
	Meta  Meta   `json:"meta"`
	Items []Item `json:"items"`
}

// Meta is the batch. run_id, step, port and attempt are the metadata the container was given
// and not the metadata it chose: the runner refuses an envelope that disagrees with them
// rather than quietly correcting it, which is why they are read from the environment here and
// never from a parameter.
type Meta struct {
	RunID      string `json:"run_id"`
	Step       string `json:"step"`
	Port       string `json:"port"`
	Attempt    int    `json:"attempt"`
	Count      int    `json:"count"`
	ProducedAt string `json:"produced_at"`
}

// Item is one unit of work with an identity, a payload and the files it carries.
//
// The identity is the brick's to mint and it is worth minting deliberately: an item whose id
// is derived from the payload it came from makes two runs over the same inputs comparable,
// which is the property v0.1.0 is defined by.
type Item struct {
	ID    string         `json:"id"`
	Data  map[string]any `json:"data"`
	Files []File         `json:"files"`
}

// File is an artifact the item refers to rather than carries. The uri is the logical one the
// documentation defines, agk://run/<run>/<step>/<port>/<name>, and the runner resolves it to a
// content-addressed key when it uploads the bytes.
type File struct {
	Name      string `json:"name"`
	URI       string `json:"uri"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

// Task is what the environment says about this container, which is everything a brick needs to
// write metadata it will not be refused for.
type Task struct {
	RunID   string
	Step    string
	Attempt int
	Shard   string
	Ports   []string

	// Root is prefixed to every path of the contract, and it is empty in a container.
	//
	// It exists for the one case the contract cannot serve: a test on the machine a brick
	// is built on, which may not write to /agk/out and should not need a container to be
	// told where bytes go. A brick never sets it, Read never fills it, and the paths stay
	// the constants above rather than becoming configuration.
	Root string
}

// Read takes the task from the environment.
//
// A missing AGK_ATTEMPT is attempt 1 rather than an error. The runner always sets it, and a
// brick run by hand to see what it does is worth more than a refusal that says nothing about
// the brick.
func Read() Task {
	attempt, err := strconv.Atoi(os.Getenv("AGK_ATTEMPT"))
	if err != nil || attempt < 1 {
		attempt = 1
	}
	return Task{
		RunID:   os.Getenv("AGK_RUN_ID"),
		Step:    os.Getenv("AGK_STEP"),
		Attempt: attempt,
		Shard:   os.Getenv("AGK_SHARD"),
		Ports:   split(os.Getenv("AGK_OUT_PORTS")),
	}
}

// at is a path of the contract, under Root where a test set one.
func (t Task) at(path string) string {
	if t.Root == "" {
		return path
	}
	return filepath.Join(t.Root, path)
}

// In reads the envelope that arrived on one input port.
//
// Under /agk/in/<port>/envelope.json and not from standard input, because standard input
// carries the envelope of one port alone and a brick with two inputs could not tell them
// apart. A port that was never written is a batch of nothing rather than a failure: an edge
// above that published nothing is the ordinary case, and a brick that refused it would fail a
// run for the absence of work.
func (t Task) In(port string) (Envelope, error) {
	path := t.at(filepath.Join(inDir, port, "envelope.json"))
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Envelope{Meta: Meta{RunID: t.RunID, Step: t.Step, Port: port, Attempt: t.Attempt}}, nil
	}
	if err != nil {
		return Envelope{}, fmt.Errorf("the envelope on port %s could not be read at %s: %w", port, path, err)
	}
	var e Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return Envelope{}, fmt.Errorf("the envelope on port %s is not an envelope: %w", port, err)
	}
	return e, nil
}

// Out writes one envelope on one output port.
//
// count is set from the items here rather than taken from the caller, because count is how
// many items the envelope holds and the runner refuses an envelope that says otherwise. A
// brick with nothing to say on a port does not call this at all: a port nobody wrote publishes
// an empty envelope, which is what lets a brick write only the port it has news on.
func (t Task) Out(port string, items []Item) error {
	if items == nil {
		items = []Item{}
	}
	e := Envelope{
		Meta: Meta{
			RunID:   t.RunID,
			Step:    t.Step,
			Port:    port,
			Attempt: t.Attempt,
			Count:   len(items),
			// The runner stamps the real moment. A brick that guessed one would be
			// guessing at something it cannot see: the port is published when the
			// step ends, which is after this process is gone.
			ProducedAt: "1970-01-01T00:00:00Z",
		},
		Items: items,
	}
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("the envelope for port %s could not be written: %w", port, err)
	}
	ports := t.at(outPorts)
	if err := os.MkdirAll(ports, 0o755); err != nil {
		return fmt.Errorf("%s could not be created: %w", ports, err)
	}
	path := filepath.Join(ports, port+".json")
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("the envelope for port %s could not be written at %s: %w", port, path, err)
	}
	return nil
}

// Attach writes an artifact under /agk/out/files/ and answers with the File an item refers to
// it by.
//
// The digest and the size are computed here rather than left to the runner because they
// travel in the envelope: an item says what it attached, and the runner holds it to that.
func (t Task) Attach(port, name, mediaType string, content []byte) (File, error) {
	files := t.at(outFiles)
	if err := os.MkdirAll(files, 0o755); err != nil {
		return File{}, fmt.Errorf("%s could not be created: %w", files, err)
	}
	path := filepath.Join(files, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return File{}, fmt.Errorf("the artifact %s could not be written at %s: %w", name, path, err)
	}
	return File{
		Name:      name,
		URI:       "agk://run/" + t.RunID + "/" + t.Step + "/" + port + "/" + name,
		MediaType: mediaType,
		Size:      int64(len(content)),
		SHA256:    Digest(content),
	}, nil
}

// Declared says whether the manifest declared a port, which is what AGK_OUT_PORTS carries.
//
// A brick asks before writing a port it only sometimes has: writing one the step does not
// declare is refused by the collection, and refused is better than dropped, so a brick that
// knows it may be configured without a port checks rather than hopes.
func (t Task) Declared(port string) bool {
	for _, p := range t.Ports {
		if p == port {
			return true
		}
	}
	return false
}

// Keys is the data of an item, in a stable order, for a message that names what it holds.
func Keys(data map[string]any) []string {
	names := make([]string, 0, len(data))
	for k := range data {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// split reads AGK_OUT_PORTS, which is comma separated and may be empty.
func split(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}
