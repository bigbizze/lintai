#!/usr/bin/env bash
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
src=$(cd "${1:?existing G3 fixture}" && pwd)
dest=${2:?new destination directory}
test "$(git -C "$src/zod" rev-parse HEAD)" = b1f6ba06c5de0e600640917615e5df70fa73f060
cmp "$src/zod/pnpm-lock.yaml" "$here/zod-resolved-lock.yaml"
cmp "$src/host-tools/package-lock.json" "$here/host-tools/package-lock.json"
for name in functions modules; do cmp "$src/rules/$name.ts" "$here/rules/$name.ts"; done
mkdir "$dest"
# Preserve pnpm's relative links and mtimes. Fresh mtimes affect Quick validation.
cp -Rp "$src/zod" "$src/rules" "$src/host-tools" "$dest/"
echo "$src" > "$dest/g3-copy.txt"
