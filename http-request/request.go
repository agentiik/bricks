package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// spec is one request, as the step and the item together decide it.
//
// The step says what is true of every item, the item says what is true of itself, and the item
// wins. That is what makes a fan-out worth anything: a thousand containers each fetching one
// order have one step's worth of configuration and one item's worth of difference.
type spec struct {
	URL     string
	Method  string
	Headers map[string]string
	Query   map[string]string
	Body    json.RawMessage
}

// request is what an item asks for, as the in port's schema declares it. Every member is
// optional, because an item that adds nothing to the step's own parameters is the ordinary
// case.
type request struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Query   map[string]string `json:"query"`
	Body    json.RawMessage   `json:"body"`
}

// settings are the step's parameters, which every item starts from.
type settings struct {
	URL     string
	Method  string
	Headers map[string]string
	Timeout time.Duration
	Bearer  string
	// InlineMax is the size above which a response body is attached rather than carried,
	// which is the documented inline_max_bytes.
	InlineMax int
}

// merge is the step's settings with the item's own on top.
//
// Headers merge rather than replace, key by key: a step setting Accept for every item and an
// item adding an If-Match of its own is the case that matters, and replacing the map would make
// the item's one header cost it the step's.
func merge(s settings, r request) (spec, error) {
	out := spec{
		URL:     s.URL,
		Method:  strings.ToUpper(s.Method),
		Headers: map[string]string{},
		Query:   map[string]string{},
		Body:    r.Body,
	}
	for k, v := range s.Headers {
		out.Headers[k] = v
	}
	if r.URL != "" {
		out.URL = r.URL
	}
	if r.Method != "" {
		out.Method = strings.ToUpper(r.Method)
	}
	for k, v := range r.Headers {
		out.Headers[k] = v
	}
	for k, v := range r.Query {
		out.Query[k] = v
	}
	if out.Method == "" {
		out.Method = http.MethodGet
	}
	if out.URL == "" {
		return spec{}, fmt.Errorf("no url: the step supplies one as the url parameter, or every item carries its own")
	}
	parsed, err := url.Parse(out.URL)
	if err != nil {
		return spec{}, fmt.Errorf("the url %q does not parse: %w", out.URL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return spec{}, fmt.Errorf("the url %q is %s: this brick speaks http and https, and a step reaching anything else is a step reaching past its egress list", out.URL, scheme(parsed.Scheme))
	}
	if len(out.Query) > 0 {
		q := parsed.Query()
		// Sorted, so that two runs of one item produce the same url and a cache, a log
		// and a signature downstream see the same string.
		for _, k := range sorted(out.Query) {
			q.Set(k, out.Query[k])
		}
		parsed.RawQuery = q.Encode()
		out.URL = parsed.String()
	}
	return out, nil
}

// response is what came back, as the out port's schema declares it.
type response struct {
	Status   int
	Headers  map[string]string
	Body     []byte
	Duration time.Duration
}

// send makes one request.
//
// The body is the item's, sent as JSON, and Content-Type is set only where the step did not set
// one: a step talking to something that wants a vendor media type says so, and a brick
// overriding it would make that unsayable.
func send(client *http.Client, s spec, bearer string) (response, error) {
	var body io.Reader
	if len(s.Body) > 0 {
		body = bytes.NewReader(s.Body)
	}
	req, err := http.NewRequest(s.Method, s.URL, body)
	if err != nil {
		return response{}, fmt.Errorf("the request could not be built: %w", err)
	}
	for _, k := range sorted(s.Headers) {
		req.Header.Set(k, s.Headers[k])
	}
	if len(s.Body) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		// The value reaches the brick as a file the runner bound, never as a variable,
		// and it goes no further than this header. Nothing writes it to a log: the
		// runner masks it on the way out, and a brick that printed it would be asking
		// the masker to save it from itself.
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	start := time.Now()
	res, err := client.Do(req)
	if err != nil {
		return response{}, err
	}
	defer res.Body.Close()
	read, err := io.ReadAll(res.Body)
	if err != nil {
		return response{}, fmt.Errorf("the response body could not be read: %w", err)
	}
	return response{
		Status:   res.StatusCode,
		Headers:  single(res.Header),
		Body:     read,
		Duration: time.Since(start),
	}, nil
}

// payload is the response as an item's data carries it.
//
// A JSON body travels as JSON, because a step downstream reading .body.total should not have to
// parse a string first. Anything else travels as text where it is text, and as nothing where the
// body was attached instead.
func payload(s spec, r response) map[string]any {
	data := map[string]any{
		"url":         s.URL,
		"method":      s.Method,
		"status":      r.Status,
		"headers":     asAny(r.Headers),
		"duration_ms": r.Duration.Milliseconds(),
	}
	return data
}

// decode reads a body as JSON where it is JSON, and answers whether it was.
//
// Declared by the response's own Content-Type and then tried, rather than tried blindly: a
// server sending text/plain that happens to be a bare number is sending text, and reading it as
// a number would change what the step receives on the strength of a coincidence.
func decode(contentType string, body []byte) (any, bool) {
	if !isJSON(contentType) {
		return nil, false
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, false
	}
	return v, true
}

// isJSON reads a media type, ignoring its parameters and accepting the +json suffix.
func isJSON(contentType string) bool {
	media := strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	media = strings.ToLower(media)
	return media == "application/json" || media == "text/json" || strings.HasSuffix(media, "+json")
}

// single is the response headers as one value each, which is what a payload can carry.
//
// The first value of a repeated header and not all of them, because an item's data is a flat
// object and Set-Cookie is the header this loses, which is not a header a deterministic tool
// should be acting on anyway.
func single(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

// sorted is a map's keys in order, so that two runs build one request the same way.
func sorted(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// asAny is a string map as a payload carries it.
func asAny(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// scheme names what a url asked for, for the message that refuses it.
func scheme(s string) string {
	if s == "" {
		return "a url with no scheme"
	}
	return s
}
