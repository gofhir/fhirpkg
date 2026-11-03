#!/usr/bin/env bash
# Descarga la muestra de Synthea R4 (datos sintéticos, no reales) de
# synthetichealth/synthea-sample-data, fijada por commit y por SHA-256
# (scripts/synthea.sha256), a .cache/synthea/. Nunca entra al repo. Fallar
# cerrado: un archivo que no coincide se descarga otra vez y, si sigue sin
# coincidir, el script falla. Necesita curl y shasum.
set -euo pipefail
here=$(cd "$(dirname "$0")/.." && pwd)
dir=$here/.cache/synthea
list=$here/scripts/synthea.sha256
# El commit de synthea-sample-data que trae el zip (19-11-2021).
commit=a59acd49f50d240ce6bacc22a719fff8b25b8dc3
mkdir -p "$dir"
while read -r sum file; do
  [[ -z $file ]] && continue
  if [[ -f $dir/$file ]] && ! (cd "$dir" && echo "$sum  $file" | shasum -a 256 -c --status); then
    echo "$file does not match its SHA-256: downloading again"
    rm -f "$dir/$file"
  fi
  if [[ ! -f $dir/$file ]]; then
    url=https://github.com/synthetichealth/synthea-sample-data/raw/$commit/downloads/$file
    echo "downloading $file ($url)"
    curl -sSfL --max-time 900 -o "$dir/$file.part" "$url"
    mv "$dir/$file.part" "$dir/$file"
  fi
done < "$list"
(cd "$dir" && shasum -a 256 -c "$list")
