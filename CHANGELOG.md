# Changelog

The releases of `bricks`.

Every repository of the project carries the same version and is tagged at the same moment, even where nothing changed, so an entry here may say that nothing was built. [Versioning](https://agentiik.github.io/docs#versioning) sets out why, and what a version promises before and after `1.0.0`.

`0.y.z` promises nothing beyond itself: what a release here describes may be gone in the next one.

## v0.1.1, 2026-09-13

This file, and nothing else.

`v0.1.0` was tagged before its changelog was written, and the fix for that is not to move the tag. Within minutes of the push, `sum.golang.org` had recorded the tagged commit of `agentiik` and `bricks` in a public append-only log and `proxy.golang.org` had cached it, so moving `v0.1.0` would have left `go get` serving the old code for ever and made a direct fetch fail with a checksum mismatch that reads as a supply-chain attack. A tag is a name somebody else pins, and a name that quietly comes to mean something else is worse than a second name.

So `v0.1.0` stays exactly where it is, describing exactly what it shipped, and this release adds the description. Every repository gets it at the same version on the same day, as every release here does. From now on a version's entry is merged before its tag is placed, which is written down in the conventions the documentation fixes.

## v0.1.0, 2026-09-12

The first four bricks of the standard catalog, and the layout the rest will follow.

- `jq` runs one jq expression over every item. The identity and the attachments travel beside the payload as `$id` and `$files`, so an expression can filter on an identity without the brick inventing a field for it.
- `schema-validate` holds every item to a JSON Schema, given inline or as a file of the workflow repository, and sends the ones that do not meet it to a port of their own with the payload unchanged beside what was wrong with it.
- `csv` converts in both directions: a table an item attached becomes one item per row, and a batch becomes a table, carried in the envelope while it fits and attached as an artifact once it does not.
- `http-request` issues one request per item, the step configuring every item and the item configuring itself. It is built and its manifest validates, and it cannot run yet: it declares `network: egress`, which the driver refuses until the runner has the proxy that enforces a step's `egress.allow` list.

A brick is a directory: a manifest, the brick, its example envelopes and, where the image needs more than its binary, a Dockerfile of its own. The images are `scratch` with one static binary in them, which leaves no shell, no package manager and no libc for anything to take advantage of.

Nineteen cases run in continuous integration against real containers, with the item identities compared rather than held aside, because every brick here derives an identity from the payload it came from. Nothing is published to a registry yet: publishing, signing and the OCI annotations the brick contract requires are v0.8.0 work.

No Agentiik library is required to be a brick, and none is used here beyond the thirty lines four bricks in one repository would otherwise carry four copies of. Nothing in this module imports the engine.
