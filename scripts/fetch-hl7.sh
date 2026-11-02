#!/usr/bin/env bash
# Descarga lo que usa make validator-graph y verifica su SHA-256: los packages
# de scripts/hl7-packages.sha256 (los que el validador carga por defecto, para
# que corra sin red) a .cache/packages/, y el validator_cli de
# scripts/validator.sha256 a .cache/hl7/. Necesita curl y shasum.
set -euo pipefail
here=$(cd "$(dirname "$0")/.." && pwd)
"$here/scripts/fetch-packages.sh" "$here/scripts/hl7-packages.sha256" >/dev/null
read -r sum name url < "$here/scripts/validator.sha256"
dir=$here/.cache/hl7
mkdir -p "$dir"
if [[ -f $dir/$name ]] && ! (cd "$dir" && echo "$sum  $name" | shasum -a 256 -c --status); then
  echo "$name does not match its SHA-256: downloading again"
  rm -f "$dir/$name"
fi
if [[ ! -f $dir/$name ]]; then
  echo "downloading $name ($url)"
  curl -sSfL --max-time 600 -o "$dir/$name.part" "$url"
  mv "$dir/$name.part" "$dir/$name"
fi
(cd "$dir" && echo "$sum  $name" | shasum -a 256 -c)
