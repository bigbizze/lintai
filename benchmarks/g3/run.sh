#!/usr/bin/env bash
# Arguments: parent|new, built checkout, copied fixture, installed consumer, new output dir, [parent output].
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
export G3_VARIANT=${1:?parent or new}
G3_REPO=$(cd "$2" && pwd)
G3_FIXTURE=$(cd "$3" && pwd)
G3_INSTALL=$(cd "$4" && pwd)
test -f "$G3_FIXTURE/g3-copy.txt"
G3_HOST_INSTALL=$G3_INSTALL
test -x "$G3_REPO/lintai"
test ! -e "$5"
root="$G3_FIXTURE/zod"
case "$G3_VARIANT" in
  parent)
    test ! -e "$root/lintai.toml"
    test -L "$G3_FIXTURE/rules/node_modules/@lintai/sdk"
    ln -sfn "$G3_INSTALL/node_modules/@lintai/sdk" "$G3_FIXTURE/rules/node_modules/@lintai/sdk" ;;
  new)
    test -f "${6:?parent results}/engine.jsonl"
    test -f "$6/eslint.jsonl"
    G3_HOST_INSTALL=$(cat "$6/host-install.txt")
    # Zod's tsconfig includes TS/MTS/CTS. Both rules contain plain JS, so
    # these byte-identical entries add no analyzed application sources.
    entries="$root/g3-rules"
    mkdir -p "$entries/node_modules/@lintai"
    for name in functions modules; do cp "$G3_FIXTURE/rules/$name.ts" "$entries/$name.mjs"; done
    ln -sfn "$G3_INSTALL/node_modules/@lintai/sdk" "$entries/node_modules/@lintai/sdk"
    cp "$here/lintai.toml" "$root/lintai.toml" ;;
  *) echo 'expected parent or new' >&2; exit 2 ;;
esac
mkdir "$5"
G3_OUT=$(cd "$5" && pwd)
export G3_REPO G3_FIXTURE G3_INSTALL G3_HOST_INSTALL G3_OUT NODE_DISABLE_COMPILE_CACHE=1
echo "$G3_HOST_INSTALL" > "$G3_OUT/host-install.txt"
unset G3_TRACE
export TMPDIR="$G3_OUT/temp"
mkdir "$TMPDIR"
python3 -B "$here/assets.py" "$G3_FIXTURE" "$G3_INSTALL" "$G3_HOST_INSTALL" "$G3_REPO/lintai" "$G3_OUT"
if [[ $G3_VARIANT = new ]]; then cmp "$6/inputs.json" "$G3_OUT/inputs.json"; fi
go version -m "$G3_REPO/lintai" > "$G3_OUT/tools.txt"
uname -a >> "$G3_OUT/tools.txt"
python3 -B "$here/count-build.py" "$G3_REPO" "$G3_OUT/probe"
for pass in release counted; do
  if [[ $pass = release ]]; then
    export G3_BINARY="$G3_REPO/lintai"
  else
    export G3_TRACE="$G3_OUT/trace.log" G3_BINARY="$G3_OUT/probe/lintai"
    export NODE_OPTIONS="${NODE_OPTIONS:-} --import=\"$here/reads.mjs\""
  fi
  node "$here/native.mjs"
  node "$here/projection.mjs"
  node "$here/host.mjs"
  node "$here/raw.mjs"
done
if [[ $G3_VARIANT = new ]]; then
  cmp "$6/engine.jsonl" "$G3_OUT/engine.jsonl"
  cmp "$6/eslint.jsonl" "$G3_OUT/eslint.jsonl"
  if [[ -f "$6/oxlint.jsonl" ]]; then cmp "$6/oxlint.jsonl" "$G3_OUT/oxlint.jsonl"; fi
fi
unset G3_TRACE NODE_OPTIONS
node "$here/edits.mjs"
