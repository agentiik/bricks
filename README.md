# bricks

The standard catalog: one directory per brick, each producing its own image, its manifest at `/agk/brick.yaml` and its example envelopes. Nothing is published yet: the images are built from this repository with the command below, and publishing them to `ghcr.io/agentiik/<name>`, signed and carrying the OCI annotations the brick contract requires, is v0.8.0 work.

A brick reads an envelope at `/agk/in/<port>/envelope.json`, writes one envelope per output port at `/agk/out/ports/<port>.json`, attaches artifacts under `/agk/out/files/`, and ends with an exit code the contract's table reads. That is the whole contract, and it is what makes a brick in any language, under any licence, as much a brick as the ones here. The contract is specified at <https://agentiik.github.io/docs#brick>.

## What is here

| | |
| --- | --- |
| [`jq`](jq) | Transforms and filters items through a jq expression. |
| [`schema-validate`](schema-validate) | Holds every item to a JSON Schema and routes the ones that do not meet it to a port of their own. |
| [`csv`](csv) | Converts a table into one item per row, and a batch of items into a table. |
| [`http-request`](http-request) | Issues one HTTP request per item and publishes the response. |

The rest of the catalog the documentation names, `sql-query`, `s3`, `smtp-send`, `imap-fetch`, `template`, `split`, `aggregate`, `wait` and `exec`, is v0.8.0 work. These four are v0.1.0 because they are what a workflow needs to do anything at all.

## A brick is a directory

    <name>/brick.yaml       the manifest, copied to /agk/brick.yaml in the image
    <name>/main.go          the brick
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

## Building and testing one

    docker build --build-arg BRICK=jq -t ghcr.io/agentiik/jq:0.1.0 .
    cd jq && agk brick test --image ghcr.io/agentiik/jq:0.1.0 \
      --ignore meta.run_id,meta.produced_at,files.uri

One `Dockerfile` at the root serves every brick whose image is only its binary, selected by `BRICK`. A brick needing more carries its own beside its manifest and says in it what it needs: `http-request` does, for the certificate authorities it cannot verify a server without.

The images are `scratch` with one static binary in them. No shell, no package manager, no libc: a brick is given a read-only root filesystem, no network by default and an unprivileged account, and an image with none of those three in it has nothing left to take advantage of.

The identities are compared rather than held aside, which `--ignore` above is saying by leaving `items.id` out of it. Every brick here derives an item's identity from the payload it came from, because that is what makes two runs over one batch comparable and a replay rejoin what it rejoined before.

## http-request cannot be run yet

Its manifest declares `network: egress`, and the driver refuses that posture until the runner has the proxy that enforces a step's `egress.allow` list: opening the network and calling it filtered is the one thing it will not do. Its four cases are committed and will run when the proxy lands in v0.2.0. What it does with a request is held in `http-request/request_test.go`, against a server in the test's own process.

## No Agentiik library

`internal/envelope` exists because four bricks in one repository would otherwise carry four copies of the same thirty lines. It is not a dependency any brick needs: the contract is files, and a shell script that reads one and writes another is as much a brick as anything here. Nothing in this module imports the engine, so an operator upgrading one is never told to upgrade the other.

Two dependencies, each with its reason in `go.mod`: a pure Go jq, so that the image can be a static binary on scratch, and the JSON Schema implementation the engine validates workflow inputs with, so that a brick cannot accept what the engine refuses.

## Licence

Apache-2.0, see [LICENSE](LICENSE). Anyone must be able to copy a brick from this catalog as the starting point of their own, whatever licence theirs carries. [LICENSING.md](https://github.com/agentiik/.github/blob/main/LICENSING.md) has the reasoning.

## Contributing

[CONTRIBUTING.md](https://github.com/agentiik/.github/blob/main/CONTRIBUTING.md), under the Developer Certificate of Origin 1.1.
