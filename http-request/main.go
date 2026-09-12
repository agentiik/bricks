// Command http-request issues one HTTP request per item and publishes what came back.
//
// The step says what is true of every item, as url, method, headers and timeout; the item says
// what is true of itself, and the item wins. That is what a fan-out is for: a thousand
// containers each fetching one order share one step's configuration and differ by one field.
//
// A response that arrived is a response, whatever its status. 2xx and 3xx leave on out, 4xx and
// 5xx leave on error, and a request that could not be made at all leaves on error too, naming
// what happened. The step does not fail for any of them, because a batch of a hundred where two
// were refused should not redo the ninety eight; a workflow that wants the strict reading reads
// error and fails on it, which is a line of YAML. The exit codes are kept for what is wrong with
// the step rather than with one response: a url nobody supplied, a timeout that is not a
// duration, a secret declared and not mounted.
//
// The bearer token is a file the runner bound at /agk/secrets/bearer and it reaches one place,
// the Authorization header. Nothing here logs it.
//
// A body above the documented inline_max_bytes is attached as an artifact rather than carried in
// the envelope, which is the size rule and not this brick's idea.
package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/agentiik/bricks/internal/envelope"
)

const (
	in    = "in"
	out   = "out"
	fail  = "error"
	limit = 262144

	// bearerPath is where the runner binds the optional secret of that name. It is the
	// manifest's own mount point, and the manifest is what the workflow answers with a
	// secret of its namespace.
	bearerPath = "/agk/secrets/bearer"
)

// duration is the one spelling the language allows: one number and one unit. A compound such as
// 1h30m is refused everywhere else in the system, so it is refused here.
var duration = regexp.MustCompile(`^([0-9]+)(ms|s|m|h|d)$`)

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

	client := &http.Client{
		Timeout: s.Timeout,
		// The default transport with nothing loosened. A brick that skipped
		// verification would make network: egress and an allow list a decoration, and
		// an installation that needs a private authority mounts it as a file of the
		// repository where its own tooling already looks for one.
		Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
	}

	var answered, refused []envelope.Item
	for _, item := range batch.Items {
		var asked request
		if err := remarshal(item.Data, &asked); err != nil {
			refused = append(refused, trouble(item, "", "", "the item is not a request: "+err.Error()))
			continue
		}
		spec, err := merge(s, asked)
		if err != nil {
			// A url the step and the item both failed to supply is a fault of the
			// step, not of the response, and it will be a fault on every item of
			// the batch. Refusing the whole batch says it once.
			envelope.Die(envelope.Invalid, "item %s: %s", item.ID, err)
		}

		res, err := send(client, spec, s.Bearer)
		if err != nil {
			refused = append(refused, trouble(item, spec.URL, spec.Method, reason(err)))
			continue
		}

		data := payload(spec, res)
		attached, file, err := carry(task, item, res, s.InlineMax, port(res.Status), data)
		if err != nil {
			envelope.Die(envelope.Failed, "item %s: %s", item.ID, err)
		}
		published := envelope.Item{ID: item.ID, Data: data, Files: []envelope.File{}}
		if attached {
			published.Files = []envelope.File{file}
		}
		if port(res.Status) == out {
			answered = append(answered, published)
			continue
		}
		refused = append(refused, published)
	}

	if err := task.Out(out, answered); err != nil {
		envelope.Die(envelope.Failed, "%s", err)
	}
	if err := task.Out(fail, refused); err != nil {
		envelope.Die(envelope.Failed, "%s", err)
	}
	fmt.Fprintf(os.Stderr, "%d requests, %d answered, %d on %s\n", len(batch.Items), len(answered), len(refused), fail)
}

// configure reads the step's parameters and the secret, where one is mounted.
func configure() (settings, error) {
	params, err := envelope.Params()
	if err != nil {
		return settings{}, err
	}

	s := settings{Method: http.MethodGet, Timeout: 30 * time.Second, InlineMax: limit}
	if _, err := envelope.Into(params, "url", &s.URL); err != nil {
		return settings{}, err
	}
	if _, err := envelope.Into(params, "method", &s.Method); err != nil {
		return settings{}, err
	}
	if _, err := envelope.Into(params, "headers", &s.Headers); err != nil {
		return settings{}, err
	}
	if _, err := envelope.Into(params, "inline_max_bytes", &s.InlineMax); err != nil {
		return settings{}, err
	}
	if s.InlineMax < 0 {
		return settings{}, fmt.Errorf("inline_max_bytes says %d: it is the size above which a body is attached, and it is never negative", s.InlineMax)
	}

	var written string
	if supplied, err := envelope.Into(params, "timeout", &written); err != nil {
		return settings{}, err
	} else if supplied {
		s.Timeout, err = parseDuration(written)
		if err != nil {
			return settings{}, err
		}
	}

	// The secret is optional in the manifest, so a file that is not there is a step that
	// declared no bearer rather than a fault. A file that is there and cannot be read is a
	// fault, because something mounted it and meant it.
	value, err := os.ReadFile(bearerPath)
	switch {
	case err == nil:
		s.Bearer = strings.TrimSpace(string(value))
	case !os.IsNotExist(err):
		return settings{}, fmt.Errorf("the bearer secret is mounted at %s and could not be read: %w", bearerPath, err)
	}
	return s, nil
}

// parseDuration reads the language's own spelling of a duration and nothing else.
func parseDuration(written string) (time.Duration, error) {
	m := duration.FindStringSubmatch(written)
	if m == nil {
		return 0, fmt.Errorf("timeout %q is not a duration: one number and one unit of ms, s, m, h or d, and 1h30m is two", written)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("timeout %q is not a duration above zero", written)
	}
	units := map[string]time.Duration{
		"ms": time.Millisecond, "s": time.Second, "m": time.Minute,
		"h": time.Hour, "d": 24 * time.Hour,
	}
	return time.Duration(n) * units[m[2]], nil
}

// port is where a response leaves, by the only thing that decides it.
func port(status int) string {
	if status >= 200 && status < 400 {
		return out
	}
	return fail
}

// carry puts the body where it belongs: in the envelope while it is small enough, and in an
// artifact once it is not.
//
// The name of the artifact is derived from the item so that two items of one batch do not
// collide on one port, and the extension follows the media type so that whatever opens it next
// is not guessing.
func carry(task envelope.Task, item envelope.Item, res response, inlineMax int, on string, data map[string]any) (bool, envelope.File, error) {
	contentType := res.Headers["Content-Type"]
	if len(res.Body) <= inlineMax {
		if v, ok := decode(contentType, res.Body); ok {
			data["body"] = v
			return false, envelope.File{}, nil
		}
		data["body"] = string(res.Body)
		return false, envelope.File{}, nil
	}
	name := "response-" + safe(item.ID) + extension(contentType)
	file, err := task.Attach(on, name, media(contentType), res.Body)
	if err != nil {
		return false, envelope.File{}, err
	}
	data["body_attached"] = name
	return true, file, nil
}

// trouble is the item an error leaves on the error port.
//
// It carries what was asked and what happened, and never a status, because there was none: a
// request that did not arrive has no status code and inventing a zero would make a filter on
// status >= 400 miss it.
func trouble(item envelope.Item, url, method, message string) envelope.Item {
	data := map[string]any{"message": message}
	if url != "" {
		data["url"] = url
	}
	if method != "" {
		data["method"] = method
	}
	return envelope.Item{ID: item.ID, Data: data, Files: []envelope.File{}}
}

// reason is a transport failure as one sentence, with the url taken out of it.
//
// Go writes the whole request into the error, so the message would otherwise repeat the url the
// item already carries, and a url with a query string in it is the longest part of the line.
func reason(err error) string {
	var e *url.Error
	if errors.As(err, &e) && e.Err != nil {
		return e.Err.Error()
	}
	return err.Error()
}

// remarshal reads an item's data as the request it declares. The data is already a decoded
// document, so this is the one way to read it as a structure without asking the caller to have
// kept the bytes.
func remarshal(data map[string]any, into any) error {
	if len(data) == 0 {
		return nil
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, into)
}

// extension is what an attached body is called, by what it is.
func extension(contentType string) string {
	switch {
	case isJSON(contentType):
		return ".json"
	case strings.HasPrefix(media(contentType), "text/csv"):
		return ".csv"
	case strings.HasPrefix(media(contentType), "text/"):
		return ".txt"
	}
	return ".bin"
}

// media is the media type without its parameters, or the one to assume when a server sent none.
func media(contentType string) string {
	m := strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	if m == "" {
		return "application/octet-stream"
	}
	return strings.ToLower(m)
}

// safe is an identity as a file name: an item may be called anything, and an artifact's name is
// a single path element the runner holds to a pattern.
//
// An identity that had to be rewritten carries a digest of the identity it came from, because
// rewriting is not injective: "order a1!" and "order-a1" both reduce to order-a1, and two items
// of one batch attaching different bytes under one name is refused by the collection. The digest
// is of the identity and not of the bytes, so the same item named twice is named the same way.
func safe(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == id {
		return name
	}
	short := envelope.Digest([]byte(id))[:8]
	if name == "" {
		return "item-" + short
	}
	return name + "-" + short
}
