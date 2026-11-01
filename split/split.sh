#!/bin/sh
# split: one item into several, one per element of the array field the step names.
#
# Nothing here comes from Agentiik but the contract: the envelope at /agk/in/in/envelope.json,
# the parameters at /agk/params.json, the variables the runner sets, one envelope written at
# /agk/out/ports/out.json, and the exit code. jq is the only tool, and any brick author could
# have chosen it.
#
# The size rules hold without spilling anything: every element was part of an array that
# travelled inline, so it is under inline_max_bytes too, and the other fields are the ones the
# item arrived with. What can still break them is the number of items and the weight of the
# envelope, which are measured below before the step is left to the runner's refusal.
set -eu

in=/agk/in/in/envelope.json
params=/agk/params.json
out=/agk/out/ports/out.json
max_items=100000
envelope_max_bytes=4194304

# 120 is invalid input, never retried: what the step or the batch says will be as wrong on the
# next attempt. 1 is an application failure.
invalid() { echo "$*" >&2; exit 120; }
failed() { echo "$*" >&2; exit 1; }

[ -f "$params" ] || invalid "no field: split splits the array a field of each item holds, and the step names the field"
field=$(jq -r 'if (.field | type) == "string" and .field != "" then .field else empty end' "$params") \
  || invalid "the parameters at $params are not a JSON object"
[ -n "$field" ] || invalid "no field: split splits the array a field of each item holds, and the step names the field"

# A port nobody fed is a batch of nothing, and a port nobody writes publishes an empty envelope.
[ -f "$in" ] || exit 0

# Refused before anything is written, naming the first item at fault.
fault=$(jq -r --arg field "$field" '
  first(.items[] | select((.data[$field] | type) != "array")
    | "item \(.id): its \($field) is \(.data[$field] | type), and split splits an array") // empty
' "$in") || invalid "the envelope on port in is not JSON"
[ -z "$fault" ] || invalid "$fault"

mkdir -p "$(dirname "$out")"
jq -c --arg field "$field" --arg run "${AGK_RUN_ID:-}" --arg step "${AGK_STEP:-}" --argjson attempt "${AGK_ATTEMPT:-1}" '
  [ .items[] as $item
    | $item.data[$field] | to_entries[]
    | {id: "\($item.id)#\(.key)", data: ($item.data + {($field): .value}), files: ($item.files // [])} ]
  | {meta: {run_id: $run, step: $step, port: "out", attempt: $attempt, count: length, produced_at: "1970-01-01T00:00:00Z"}, items: .}
' "$in" > "$out" || failed "the items could not be written at $out"

count=$(jq '.meta.count' "$out")
[ "$count" -le "$max_items" ] || { rm -f "$out"; failed "the envelope on port out holds $count items and max_items is $max_items: split the batch upstream"; }
bytes=$(wc -c < "$out")
[ "$bytes" -le "$envelope_max_bytes" ] || { rm -f "$out"; failed "the envelope on port out weighs $bytes bytes and envelope_max_bytes is $envelope_max_bytes: split the batch upstream"; }

echo "$(jq '.items | length' "$in") in, $count out" >&2
