package envelope

import (
	"fmt"
	"os"
)

// The exit codes of the contract's table, as the bricks of this catalog use them.
//
// Only three of the bands are ever left deliberately. A brick knows when its input was
// unusable and when the far side was having a bad minute; everything else it does not know is
// an application failure, and inventing a finer distinction would be telling the runner
// something untrue about whether to retry.
const (
	// OK: the ports were written and the step succeeded.
	OK = 0
	// Failed: an application failure, not retried unless the step's retry.on says so.
	Failed = 1
	// Transient: retried according to the step's policy. It is for the far side being
	// briefly unavailable, never for a payload that will be just as wrong next time.
	Transient = 100
	// Invalid: the input was unusable and will be unusable on every attempt. Permanent,
	// whatever retry says, which is why a brick reaches for it only when the fault is in
	// what it was given rather than in what it was talking to.
	Invalid = 120
)

// Die writes one sentence on standard error and leaves with a code of the table.
//
// Standard error is the log the runner captures, masks and keeps; standard output is the
// shorthand for the single out port, so a diagnostic written there would arrive as a result.
// That is the whole reason a brick never prints its troubles on standard output.
func Die(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(code)
}
