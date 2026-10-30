#!/bin/sh
# Fails if the app's Dart code or translations contain the project name.
#
# Every build must take its name from the generated Brand class (lib/brand), so
# that rebranded apps never show "Flare". Strings in .arb files use the
# {appName} placeholder. Only lib/brand is exempt. Run from anywhere:
# `make brand-check`.
set -eu

cd "$(dirname "$0")/.."

dirs=""
for dir in lib l10n; do
  if [ -d "$dir" ]; then dirs="$dirs $dir"; fi
done
if [ -z "$dirs" ]; then
  echo "No app code yet, nothing to check."
  exit 0
fi

# shellcheck disable=SC2086  # $dirs is a short list of plain folder names
matches=$(grep -rn --include='*.dart' --include='*.arb' 'Flare' $dirs | grep -v '^lib/brand/' || true)
if [ -n "$matches" ]; then
  echo "$matches"
  echo >&2
  echo >&2 "error: the name \"Flare\" must not be written in app code or translations."
  echo >&2 "Use Brand.name in Dart, and the {appName} placeholder in .arb files."
  exit 1
fi
echo "OK: no hard-coded project name in$dirs."
