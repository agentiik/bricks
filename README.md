# bricks

The standard catalog: one directory per brick, each producing its own image, its manifest at `/agk/brick.yaml` and its example envelopes. The images are published to `ghcr.io/agentiik/<name>` from v0.8.0, as described under Publishing; signing them and carrying the OCI annotations the brick contract requires is the rest of v0.8.0's work.

A brick reads an envelope at `/agk/in/<port>/envelope.json`, writes one envelope per output port at `/agk/out/ports/<port>.json`, attaches artifacts under `/agk/out/files/`, and ends with an exit code the contract's table reads. That is the whole contract, and it is what makes a brick in any language, under any licence, as much a brick as the ones here. The contract is specified at <https://agentiik.github.io/docs#brick>.

## What is here

| | |
| --- | --- |
| [`jq`](jq) | Transforms and filters items through a jq expression. |
| [`schema-validate`](schema-validate) | Holds every item to a JSON Schema and routes the ones that do not meet it to a port of their own. |
| [`csv`](csv) | Converts a table into one item per row, and a batch of items into a table. |
| [`xlsx`](xlsx) | Converts a spreadsheet into one item per row, a cell keeping its type, and a batch of items into a spreadsheet. The standard library alone reads and writes it. |
| [`http-request`](http-request) | Issues one HTTP request per item and publishes the response. |
| [`split`](split) | Splits one item into several, one per element of an array field. A shell script and jq, with no helper. |

The rest of the catalog the documentation names, `sql-query`, `s3`, `smtp-send`, `imap-fetch`, `template`, `aggregate`, `wait` and `exec`, is v0.8.0 work. The first four are v0.1.0 because they are what a workflow needs to do anything at all.

## A brick is a directory

    <name>/brick.yaml       the manifest, copied to /agk/brick.yaml in the image
    <name>/main.go          the brick, or a script its own Dockerfile copies in
    <name>/cases/<case>/    one directory per example, read by agk brick test
    <name>/Dockerfile       only where the image needs more than its binary

A case is documents and nothing else, which is what makes one writable by hand:

    <case>/in/<port>.json   the envelope the brick is given on that input port
    <case>/out/<port>.json  the envelope expected on that output port
    <case>/files/<name>     the bytes of an artifact an input envelope attaches
    <case>/params.json      what the brick reads at /agk/params.json
    <case>/repo/            the workflow repository tree, bound read-only at /agk/repo
    <case>/exit             the exit code expected, default 0

A port the manifest declares and a case writes no expectation for is expected to publish nothing.

`catalog_test.go` holds every directory to this layout before any image is built. Each directory but `internal` is a brick named as its manifest names it; the manifest validates against the brick schema of [`agentiik/schemas`](https://github.com/agentiik/schemas), which refuses a root account; the image runs as the account the manifest declares; and every port the manifest declares has a case that feeds it or expects it. It reads the schema from `BRICK_SCHEMA`, or from a checkout of `agentiik/schemas` beside this one, and skips without either; CI always sets it.

## Exit codes and size rules

A brick leaves through `envelope.Die` and a named band: `Failed` (1) for an application failure, `Transient` (100) for a far side that may answer next time, `Invalid` (120) for input it will refuse on every attempt. `Die` turns any other code into 1, since 121 and above are the runner's and the runtime's. `exits_test.go` holds every brick to that and to a case leaving with 120; no brick of the catalog has a transient failure yet, and `http-request` answers a failed request on its `error` port rather than by its exit code.

`envelope.Out` holds every envelope to the size rules before writing it, so that a brick never leaves what the runner would refuse with 121. A value of `data` above `inline_max_bytes` is written to `/agk/out/files/<item>-<field>.json` and referenced in `files[]`, the name and the bytes the engine itself spills by; an envelope still above `envelope_max_bytes`, or holding more than `max_items`, fails the step as an application failure naming the rule, and the step splits its batch upstream. The limits are the documented defaults, since the contract gives a brick no way to read its installation's, and `csv` and `http-request` take `inline_max_bytes` as a parameter.

## Building and testing one

    docker build --build-arg BRICK=jq -t ghcr.io/agentiik/jq:dev .
    cd jq && agk brick test --image ghcr.io/agentiik/jq:dev \
      --ignore meta.run_id,meta.produced_at,files.uri

One `Dockerfile` at the root serves every brick whose image is only its binary, selected by `BRICK`. A brick needing more carries its own beside its manifest and says in it what it needs: `http-request` does, for the certificate authorities it cannot verify a server without.

The images are `scratch` with one static binary in them. No shell, no package manager, no libc: a brick is given a read-only root filesystem, no network by default and an unprivileged account, and an image with none of those three in it has nothing left to take advantage of.

The identities are compared rather than held aside, which `--ignore` above is saying by leaving `items.id` out of it. Every brick here derives an item's identity from the payload it came from, because that is what makes two runs over one batch comparable and a replay rejoin what it rejoined before.

## Publishing

`.github/workflows/publish.yml` builds every directory holding a `brick.yaml` from one matrix, each into one index for `linux/amd64` and `linux/arm64`, the architectures a step selects with `runs_on: [arch=...]`. A tag `vX.Y.Z` publishes every brick as `X.Y.Z` and `vX.Y.Z`, and `latest` where it is the highest release, whether the brick changed or not; a push to `main` publishes `dev`; any other branch goes through the same steps against a registry inside the job. A release refuses a manifest whose `metadata.version` is not the tag's, so the manifests are moved to the next version on `main` before tagging, like the changelog.

## http-request cannot be run yet

Its manifest declares `network: egress`, and the driver refuses that posture until the runner has the proxy that enforces a step's `egress.allow` list: opening the network and calling it filtered is the one thing it will not do. Its four cases are committed and will run when the proxy lands in v0.9.0. What it does with a request is held in `http-request/request_test.go`, against a server in the test's own process.

## No Agentiik library

`internal/envelope` exists because the Go bricks in one repository would otherwise carry copies of the same thirty lines. It is not a dependency any brick needs: the contract is files, and a shell script that reads one and writes another is as much a brick as anything here. [`split`](split) is one, `sh` and `jq` on Alpine, tested by the same cases job as the rest; it keeps to the size rules by measuring its envelope itself, and spills nothing because every element it publishes travelled inline already. Nothing in this module imports the engine, so an operator upgrading one is never told to upgrade the other.

Three dependencies, each with its reason in `go.mod`: a pure Go jq, so that the image can be a static binary on scratch; the JSON Schema implementation the engine validates workflow inputs with, so that a brick cannot accept what the engine refuses; and the YAML parser the engine reads manifests with, for `catalog_test.go` alone, which no brick links.

## Licence

Apache-2.0, see [LICENSE](LICENSE). Anyone must be able to copy a brick from this catalog as the starting point of their own, whatever licence theirs carries. [LICENSING.md](https://github.com/agentiik/.github/blob/main/LICENSING.md) has the reasoning.

## Contributing

[CONTRIBUTING.md](https://github.com/agentiik/.github/blob/main/CONTRIBUTING.md), under the Developer Certificate of Origin 1.1. Every commit of a pull request from a fork carries a `Signed-off-by` line for its author, which `git commit -s` adds, and the `dco` workflow refuses one that does not.
