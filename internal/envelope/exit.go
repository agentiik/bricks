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
//
// A code outside the bands a brick may leave by, 0 or anything above 120, is a bug in the brick
// and not something to hand the runner: 121 to 124 are the runner's and 125 and above are the
// runtime's, and a brick leaving with one would be read as a broken contract or a broken machine.
// Die says so and leaves as an application failure instead.
func Die(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(Leaves(code))
}

// Leaves is the code Die leaves with for the code it was given: the code itself where a brick
// may leave by it, and Failed where it may not.
func Leaves(code int) int {
	if code < Failed || code > Invalid {
		fmt.Fprintf(os.Stderr, "exit code %d is not a brick's to leave with: 1 to 99 is a failure, 100 to 119 transient, 120 invalid input, and the rest is the runner's or the runtime's\n", code)
		return Failed
	}
	return code
}
