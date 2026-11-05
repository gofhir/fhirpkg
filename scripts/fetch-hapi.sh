#!/usr/bin/env bash
# Descarga a .cache/hapi los jar de hapi-fhir-structures (8.10.0, Maven Central)
# con el SHA-256 de scripts/hapi.sha256 y valida el jar de validator_cli (que trae
# hapi-fhir-base y los modelos). Necesita curl y shasum. Plan 005, D7.
set -euo pipefail
here=$(cd "$(dirname "$0")/.." && pwd)
"$here/scripts/fetch-hl7.sh" >/dev/null
dir=$here/.cache/hapi
mkdir -p "$dir"
while read -r sum name url; do
  if [[ -f $dir/$name ]] && ! (cd "$dir" && echo "$sum  $name" | shasum -a 256 -c --status); then rm -f "$dir/$name"; fi
  if [[ ! -f $dir/$name ]]; then
    echo "downloading $name"
    curl -sSfL --max-time 600 -o "$dir/$name.part" "$url"
    mv "$dir/$name.part" "$dir/$name"
  fi
  (cd "$dir" && echo "$sum  $name" | shasum -a 256 -c)
done < "$here/scripts/hapi.sha256"
