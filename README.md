# bricks

The standard catalog: one directory per brick, each producing its own image, its
manifest at `/agk/brick.yaml` and its example envelopes. Built and published as one
matrix to `ghcr.io/agentiik/<name>`, signed, with the OCI annotations the brick contract
requires.

A brick reads an envelope on standard input, writes one envelope per output port on
standard output, reads files under `/agk/in` and writes them under `/agk/out`, and ends
with an exit code. That is the whole contract, and it is what makes a brick in any
language, under any licence, as much a brick as the ones here.

Nothing is built yet. The contract is specified at <https://agentiik.github.io/docs>, and
the manifest schema comes from [`agentiik/schemas`](https://github.com/agentiik/schemas).

## Licence

Apache-2.0, see [LICENSE](LICENSE). Anyone must be able to copy a brick from this catalog
as the starting point of their own, whatever licence theirs carries.
[LICENSING.md](https://github.com/agentiik/.github/blob/main/LICENSING.md) has the reasoning.

## Contributing

[CONTRIBUTING.md](https://github.com/agentiik/.github/blob/main/CONTRIBUTING.md), under the Developer Certificate of Origin 1.1.
