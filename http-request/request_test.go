package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentiik/bricks/internal/envelope"
)

// where is a task whose artifacts land in a directory of this test rather than at /agk/out,
// which is the one thing a brick cannot have on the machine it is built on.
//
// The path is the contract's and the contract is absolute, so the package reads it from a
// variable that is the constant everywhere but here. A test that wrote to /agk/out would need a
// container, and what is under test is where the bytes go rather than the mount they go through.
func where(t *testing.T) envelope.Task {
	t.Helper()
	return envelope.Task{RunID: "01M2AAZ9G62NQXFAFCXKRPJEH5", Step: "fetch", Attempt: 1,
		Ports: []string{out, fail}, Root: t.TempDir()}
}

// item is one item of a batch, as little of one as a test needs.
func item(id string) envelope.Item {
	return envelope.Item{ID: id, Data: map[string]any{}, Files: []envelope.File{}}
}

// This brick is the one of the four that cannot be held to its cases yet: its manifest declares
// network: egress, and the driver refuses that mode until the proxy that would filter it exists.
// So the request itself is held here instead, against a server in this process, which is the
// half of the brick that has anything to get wrong. The contract half is the same code every
// other brick of the catalog runs and is covered by theirs.

// A step configures every item and an item configures itself, and where they disagree the item
// wins. Headers are the exception worth testing: they merge, because a step setting Accept for
// the batch and an item adding its own If-Match is the case, and replacing the map would make
// the item's one header cost it the step's.
func TestAnItemOverridesTheStepAndHeadersMerge(t *testing.T) {
	s := settings{
		URL:     "https://api.example.com/orders",
		Method:  "GET",
		Headers: map[string]string{"Accept": "application/json", "X-Tenant": "acme"},
	}
	got, err := merge(s, request{
		URL:     "https://api.example.com/orders/a1",
		Method:  "patch",
		Headers: map[string]string{"X-Tenant": "finance", "If-Match": "\"7\""},
		Query:   map[string]string{"expand": "lines", "at": "2026-01-31"},
	})
	if err != nil {
		t.Fatalf("merge: %s", err)
	}
	if got.Method != http.MethodPatch {
		t.Errorf("the method is %q, and a method is upper case whatever the item wrote", got.Method)
	}
	// Sorted, because two runs of one item have to build one url.
	if got.URL != "https://api.example.com/orders/a1?at=2026-01-31&expand=lines" {
		t.Errorf("the url is %q", got.URL)
	}
	if got.Headers["Accept"] != "application/json" {
		t.Errorf("the step's Accept was lost: %v", got.Headers)
	}
	if got.Headers["X-Tenant"] != "finance" {
		t.Errorf("the item did not win on X-Tenant: %v", got.Headers)
	}
	if got.Headers["If-Match"] != "\"7\"" {
		t.Errorf("the item's own header was lost: %v", got.Headers)
	}
}

// A url nobody supplied is a fault of the step and not of one response, and so is a scheme this
// brick does not speak: a step reaching file:// is a step reaching past the egress list the
// proxy will be enforcing.
func TestAUrlThatIsNotThereOrIsNotHttpIsRefused(t *testing.T) {
	if _, err := merge(settings{}, request{}); err == nil {
		t.Fatal("a request with no url was accepted")
	}
	_, err := merge(settings{}, request{URL: "file:///etc/passwd"})
	if err == nil || !strings.Contains(err.Error(), "file") {
		t.Fatalf("a file url was answered with %v", err)
	}
}

// The bearer reaches one place. It is read from a file the runner bound and written into the
// Authorization header, and a test is the only way to say that it is not also somewhere else.
func TestTheBearerTravelsInTheAuthorizationHeaderAndNowhereElse(t *testing.T) {
	var seen *http.Request
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Clone(r.Context())
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"charge-1","total":1200}`))
	}))
	defer server.Close()

	spec, err := merge(settings{URL: server.URL, Method: "POST"}, request{
		Body: json.RawMessage(`{"ref":"a1"}`),
	})
	if err != nil {
		t.Fatalf("merge: %s", err)
	}
	res, err := send(server.Client(), spec, "bk_live_secret")
	if err != nil {
		t.Fatalf("send: %s", err)
	}

	if got := seen.Header.Get("Authorization"); got != "Bearer bk_live_secret" {
		t.Errorf("the Authorization header is %q", got)
	}
	if string(body) != `{"ref":"a1"}` {
		t.Errorf("the body arrived as %q", body)
	}
	// A body the step did not type a media type for is JSON, because the item's body is a
	// JSON document and nothing else can be sent here.
	if got := seen.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("the Content-Type is %q", got)
	}
	if res.Status != http.StatusCreated {
		t.Errorf("the status is %d", res.Status)
	}
	// 201 is a response that arrived, so it leaves on out.
	if port(res.Status) != out {
		t.Errorf("a 201 leaves on %s", port(res.Status))
	}
	v, ok := decode(res.Headers["Content-Type"], res.Body)
	if !ok {
		t.Fatalf("a JSON body did not decode: %q", res.Body)
	}
	if v.(map[string]any)["total"] != float64(1200) {
		t.Errorf("the decoded body is %v", v)
	}
}

// A step that set its own Content-Type keeps it. A brick overriding it would make a vendor media
// type unsayable.
func TestAStepsOwnContentTypeIsNotOverridden(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Content-Type")
	}))
	defer server.Close()

	spec, err := merge(settings{URL: server.URL, Method: "POST",
		Headers: map[string]string{"Content-Type": "application/vnd.acme.order+json"}}, request{
		Body: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("merge: %s", err)
	}
	if _, err := send(server.Client(), spec, ""); err != nil {
		t.Fatalf("send: %s", err)
	}
	if seen != "application/vnd.acme.order+json" {
		t.Errorf("the Content-Type arrived as %q", seen)
	}
}

// 4xx and 5xx arrived, so they are answers and not failures: they leave on error with their
// status and their body, and the step does not fail for them.
func TestAStatusThatIsNotSuccessLeavesOnTheErrorPort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"title":"amount is not positive"}`))
	}))
	defer server.Close()

	spec, _ := merge(settings{URL: server.URL}, request{})
	res, err := send(server.Client(), spec, "")
	if err != nil {
		t.Fatalf("send: %s", err)
	}
	if port(res.Status) != fail {
		t.Errorf("a 422 leaves on %s", port(res.Status))
	}
	// A +json media type is JSON, which is what a problem document is written as.
	if _, ok := decode(res.Headers["Content-Type"], res.Body); !ok {
		t.Errorf("a problem document did not decode: %q", res.Body)
	}
	data := payload(spec, res)
	if data["status"] != http.StatusUnprocessableEntity {
		t.Errorf("the payload says status %v", data["status"])
	}
}

// A body above the threshold is attached rather than carried, which is the size rule and not
// this brick's idea. Below it, it travels in the envelope.
func TestABodyAboveTheThresholdIsAttached(t *testing.T) {
	big := strings.Repeat("x", 200)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(big))
	}))
	defer server.Close()

	spec, _ := merge(settings{URL: server.URL}, request{})
	res, err := send(server.Client(), spec, "")
	if err != nil {
		t.Fatalf("send: %s", err)
	}

	// Inline while it fits.
	data := payload(spec, res)
	attached, _, err := carry(where(t), item("order-a1"), res, 1024, out, data)
	if err != nil {
		t.Fatalf("carry: %s", err)
	}
	if attached || data["body"] != big {
		t.Errorf("a body of %d bytes under the threshold was attached: %v", len(big), attached)
	}

	// Attached once it does not.
	data = payload(spec, res)
	attached, file, err := carry(where(t), item("order a1!"), res, 64, out, data)
	if err != nil {
		t.Fatalf("carry: %s", err)
	}
	if !attached {
		t.Fatal("a body above the threshold was carried in the envelope")
	}
	// The name is derived from the item, so two items of one batch do not collide on one
	// port. An identity that is not a file name is made into one and carries a digest of
	// itself, because "order a1!" and "order-a1" reduce to the same name and the runner
	// refuses two artifacts of one name carrying different bytes.
	if file.Name != "response-order-a1-cbd861c5.txt" {
		t.Errorf("the artifact is called %q", file.Name)
	}
	if plain := safe("order-a1"); plain != "order-a1" {
		t.Errorf("an identity that is already a file name was rewritten to %q", plain)
	}
	if file.Size != int64(len(big)) || file.MediaType != "text/plain" {
		t.Errorf("the artifact is %+v", file)
	}
	if data["body_attached"] != file.Name {
		t.Errorf("the payload does not name the artifact: %v", data)
	}
	if _, carried := data["body"]; carried {
		t.Errorf("the body is both attached and carried: %v", data)
	}
}

// A timeout is the language's own spelling of a duration and nothing else, so that a step does
// not learn one syntax here and another everywhere else.
func TestATimeoutIsADurationTheLanguageAllows(t *testing.T) {
	for written, want := range map[string]time.Duration{
		"500ms": 500 * time.Millisecond,
		"2s":    2 * time.Second,
		"10m":   10 * time.Minute,
	} {
		got, err := parseDuration(written)
		if err != nil || got != want {
			t.Errorf("%s read as %s, %v", written, got, err)
		}
	}
	for _, written := range []string{"1h30m", "30", "", "2 s", "-5s", "0s"} {
		if _, err := parseDuration(written); err == nil {
			t.Errorf("%q was accepted as a duration", written)
		}
	}
}

// A request that did not arrive carries no status, and a zero would make a filter on status a
// filter that misses it.
func TestARequestThatDidNotArriveCarriesNoStatus(t *testing.T) {
	refused := trouble(item("order-a1"), "https://api.example.com/orders/a1", "GET", "connection refused")
	if _, ok := refused.Data["status"]; ok {
		t.Errorf("a request that did not arrive carries a status: %v", refused.Data)
	}
	for _, key := range []string{"url", "method", "message"} {
		if _, ok := refused.Data[key]; !ok {
			t.Errorf("the item on the error port does not say %s: %v", key, refused.Data)
		}
	}
}

// A request that could not be made at all is an answer of a different kind: it leaves on the
// error port with what happened, and the step does not fail for it. The server is started and
// closed so that the address is one nothing is listening on, which is the failure a real batch
// meets when a host is down.
func TestARequestThatCannotBeMadeIsRoutedAndNotRaised(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.URL
	server.Close()

	spec, err := merge(settings{URL: address}, request{})
	if err != nil {
		t.Fatalf("merge: %s", err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	if _, err := send(client, spec, ""); err == nil {
		t.Fatal("a request to a closed address came back with a response")
	} else {
		refused := trouble(item("order-a1"), spec.URL, spec.Method, reason(err))
		if _, ok := refused.Data["status"]; ok {
			t.Errorf("a request that did not arrive carries a status: %v", refused.Data)
		}
		message, _ := refused.Data["message"].(string)
		if message == "" {
			t.Errorf("the item on the error port says nothing about what happened: %v", refused.Data)
		}
		// The url is the item's own member, and Go writes the whole request into its
		// error, so the message is the cause and not the request repeated.
		if strings.Contains(message, address) {
			t.Errorf("the message repeats the url the item already carries: %q", message)
		}
	}
}
