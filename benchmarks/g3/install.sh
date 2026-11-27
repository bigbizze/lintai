#!/usr/bin/env bash
# Install built adapter packages and the pinned raw host executables into a fresh consumer.
set -euo pipefail
repo=$(cd "$1" && pwd)
mkdir "$2"
dest=$(cd "$2" && pwd)
mkdir -p "$dest/tarballs" "$dest/temp"
export TMPDIR="$dest/temp" npm_config_cache="$dest/npm-cache" XDG_CACHE_HOME="$dest/cache"
for pkg in sdk authoring-rules client eslint-plugin oxlint-plugin; do
  (cd "$repo/packages/$pkg" && node "${PNPM_JS:?set PNPM_JS to pnpm 10.9.0 bin/pnpm.cjs}" pack --pack-destination "$dest/tarballs")
done
cd "$dest"
echo '{"name":"g3-consumer","private":true,"type":"module"}' > package.json
set -- "$dest"/tarballs/*.tgz eslint@10.15.0 oxlint@1.90.0 typescript@5.9.3
if node -e 'process.exit(require(process.argv[1]).dependencies?.esbuild ? 0 : 1)' "$repo/packages/client/package.json"; then
  set -- "$@" esbuild@0.25.12
fi
npm install --no-audit --no-fund --ignore-scripts --save-exact "$@"
