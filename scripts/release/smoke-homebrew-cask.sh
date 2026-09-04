#!/usr/bin/env bash
set -euo pipefail

candidate="${1:?usage: smoke-homebrew-cask.sh CANDIDATE_DIR VERSION}"
version="${2:?usage: smoke-homebrew-cask.sh CANDIDATE_DIR VERSION}"
tap_name=flowbaton/smoke
cask=flowbaton
if [[ "$version" == *-* ]]; then
  cask=flowbaton-beta
fi
installed=0
cleanup() {
  if [[ "$installed" == 1 ]]; then
    brew uninstall --cask "$cask" >/dev/null 2>&1 || true
  fi
  brew untap "$tap_name" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

if brew list --cask "$cask" >/dev/null 2>&1; then
  echo "Homebrew smoke host already has a $cask cask installed" >&2
  exit 1
fi
brew tap-new "$tap_name"
tap="$(brew --repo "$tap_name")"
base_url="$(python3 -c 'from pathlib import Path; import sys; print(Path(sys.argv[1]).resolve().as_uri())' "$candidate")"
scripts/release/render-homebrew-cask.py \
  --version "$version" \
  --candidate "$candidate" \
  --base-url "$base_url" \
  --output "$tap/Casks/$cask.rb"

brew install --cask "$tap_name/$cask"
installed=1
binary="$(brew --prefix)/bin/flowbaton"
actual="$($binary --version)"
[[ "$actual" == "flowbaton ${version}" ]] || {
  echo "Homebrew-installed binary reported '$actual', expected 'flowbaton ${version}'" >&2
  exit 1
}
codesign --verify --strict --verbose=2 "$binary"
spctl --assess --type execute --verbose=4 "$binary"
