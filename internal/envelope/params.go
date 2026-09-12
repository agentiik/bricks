package envelope

import (
	"encoding/json"
	"fmt"
	"os"
)

// Params is /agk/params.json: what the step chose, as the manifest declared it.
//
// It is a raw map rather than a typed structure because every brick declares its own
// parameters, and each unmarshals the map into what it expects. A step that supplied nothing
// leaves the file absent, which is an empty map and not an error: whether a parameter is
// required was decided when the workflow was validated, and deciding it again here would put
// the refusal inside a container where nobody is reading.
func Params() (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(paramsAt)
	if os.IsNotExist(err) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("the parameters could not be read at %s: %w", paramsAt, err)
	}
	var p map[string]json.RawMessage
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("the parameters at %s are not a JSON object: %w", paramsAt, err)
	}
	return p, nil
}

// Into unmarshals one parameter, answering whether the step supplied it at all.
//
// The two answers are kept apart on purpose. A brick that cannot tell "not supplied" from
// "supplied as the zero value" cannot implement a default, and a default silently applied over
// an explicit zero is the kind of bug a person reads the manifest to rule out.
func Into(p map[string]json.RawMessage, name string, v any) (bool, error) {
	raw, ok := p[name]
	if !ok {
		return false, nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return true, fmt.Errorf("the parameter %s is not the shape this brick declares: %w", name, err)
	}
	return true, nil
}
