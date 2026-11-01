package envelope

import "testing"

// The bands of the table a brick may leave by, and what Die makes of the rest: 0 is not a
// failure, and 121 and above belong to the runner and the runtime.
func TestABrickLeavesOnlyByItsOwnBands(t *testing.T) {
	for code, want := range map[int]int{
		Failed: Failed, 99: 99, Transient: Transient, 119: 119, Invalid: Invalid,
		0: Failed, -1: Failed, 121: Failed, 124: Failed, 125: Failed, 137: Failed, 255: Failed,
	} {
		if got := Leaves(code); got != want {
			t.Errorf("Die(%d) leaves with %d, and the table says %d", code, got, want)
		}
	}
}
