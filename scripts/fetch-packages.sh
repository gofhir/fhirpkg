#!/usr/bin/env bash
# Descarga los packages FHIR oficiales listados en scripts/packages.sha256 a
# .cache/packages/ y verifica sus SHA-256 (fallar cerrado: un archivo que no
# coincide se descarga otra vez y, si sigue sin coincidir, el script falla).
# La URL de descarga es la que publica el registro packages.fhir.org en la
# metadata del package (dist.tarball), como hace npm. Necesita curl, shasum y
# python3.
set -euo pipefail
here=$(cd "$(dirname "$0")/.." && pwd)
dir=$here/.cache/packages
list=${1:-$here/scripts/packages.sha256}
mkdir -p "$dir"
while read -r sum file; do
  [[ -z $file ]] && continue
  name=${file%-*}; version=${file##*-}; version=${version%.tgz}
  # Un archivo que no coincide con su SHA-256 (descarga cortada, corrupto) se
  # vuelve a descargar.
  if [[ -f $dir/$file ]] && ! (cd "$dir" && echo "$sum  $file" | shasum -a 256 -c --status); then
    echo "$file does not match its SHA-256: downloading again"
    rm -f "$dir/$file"
  fi
  if [[ ! -f $dir/$file ]]; then
    url=$(curl -sSf --max-time 60 "https://packages.fhir.org/$name" |
      python3 -I -c 'import json,sys; print(json.load(sys.stdin)["versions"][sys.argv[1]]["dist"]["tarball"])' "$version")
    echo "downloading $name $version ($url)"
    curl -sSfL --max-time 600 -o "$dir/$file.part" "$url"
    mv "$dir/$file.part" "$dir/$file"
  fi
done < "$list"
(cd "$dir" && shasum -a 256 -c "$list")
