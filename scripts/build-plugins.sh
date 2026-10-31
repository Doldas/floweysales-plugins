#!/usr/bin/env bash
# Builds provider plugins into store release directories and a local store.
#
#   scripts/build-plugins.sh                        all plugins, this platform, dist/plugins + index.json
#   scripts/build-plugins.sh -p linux/amd64,windows/amd64 yahoo
#   scripts/build-plugins.sh --no-index -o dist yahoo   (release CI)
#
# Each plugin lands in <out>/<id>-<version>/ with plugin.json, SHA256SUMS and
# floweysales-plugin-<id>-<os>-<arch>[.exe]. Point the backend at the local
# store with PLUGIN_STORE_URL=file://<abs out>/index.json.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/dist/plugins"
platforms="$(go env GOOS)/$(go env GOARCH)"
index=1
ids=()
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -p) platforms="$2"; shift 2 ;;
    --no-index) index=0; shift ;;
    -h|--help) sed -n '2,11p' "$0"; exit 0 ;;
    *) ids+=("$1"); shift ;;
  esac
done
if [ ${#ids[@]} -eq 0 ]; then
  for manifest in "$root"/*/plugin.json; do ids+=("$(basename "$(dirname "$manifest")")"); done
fi
mkdir -p "$out"
out="$(cd "$out" && pwd)"

checksum() {
  if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}

for id in "${ids[@]}"; do
  manifest="$root/$id/plugin.json"
  [ -f "$manifest" ] || { echo "unknown plugin: $id" >&2; exit 1; }
  version="$(node -p "require(process.argv[1]).version" "$manifest")"
  dir="$out/$id-$version"
  rm -rf "$dir"
  mkdir -p "$dir"
  cp "$manifest" "$dir/plugin.json"
  IFS=',' read -r -a targets <<< "$platforms"
  for target in "${targets[@]}"; do
    goos="${target%/*}"; goarch="${target#*/}"
    suffix=""; [ "$goos" = windows ] && suffix=".exe"
    name="floweysales-plugin-$id-$goos-$goarch$suffix"
    echo "build $id $version $goos/$goarch"
    (cd "$root" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      go build -trimpath -ldflags "-s -w" -o "$dir/$name" "./$id")
  done
  (cd "$dir" && checksum floweysales-plugin-* > SHA256SUMS)
done

if [ "$index" = 1 ]; then
  node "$root/scripts/plugin-index.mjs" local "$out" > "$out/index.json"
  echo "store: file://$out/index.json"
fi
