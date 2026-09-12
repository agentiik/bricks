// Package envelope is the two documents a brick of this catalog reads and the two it writes.
//
// It exists because four bricks in one repository would otherwise carry four copies of the
// same thirty lines, and not because the contract needs a library. The contract is files: an
// envelope arrives on standard input and under /agk/in/<port>/envelope.json, parameters arrive
// at /agk/params.json, an envelope leaves at /agk/out/ports/<port>.json, an artifact leaves
// under /agk/out/files/, and the exit code decides the step's verdict. A shell script that
// reads one file and writes another is as much a brick as anything here, which is the point of
// "no Agentiik library is required" and the reason this package is internal: it is this
// catalog's convenience and not an interface anybody else should depend on.
//
// Nothing here reaches the engine's own packages. A brick that imported the engine would make
// the catalog a downstream of the server it runs under, and an operator upgrading one would be
// told to upgrade the other.
package envelope
