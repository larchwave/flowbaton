#!/usr/bin/env bash
set -euo pipefail

repo="${1:?usage: verify-tag.sh OWNER/REPO TAG EXPECTED_COMMIT}"
tag="${2:?usage: verify-tag.sh OWNER/REPO TAG EXPECTED_COMMIT}"
expected_commit="${3:?usage: verify-tag.sh OWNER/REPO TAG EXPECTED_COMMIT}"

case "$tag" in
  v[0-9]*) ;;
  *) echo "release tag must start with v followed by a digit: $tag" >&2; exit 1 ;;
esac

command -v gh >/dev/null 2>&1 || { echo "gh is required" >&2; exit 1; }

ref_json="$(gh api "repos/${repo}/git/ref/tags/${tag}")"
tag_sha="$(printf '%s' "$ref_json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["object"]["sha"])')"
ref_type="$(printf '%s' "$ref_json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["object"]["type"])')"
if [[ "$ref_type" != "tag" ]]; then
  echo "release tag $tag is lightweight; an annotated signed tag is required" >&2
  exit 1
fi

tag_json="$(gh api "repos/${repo}/git/tags/${tag_sha}")"
verified="$(printf '%s' "$tag_json" | python3 -c 'import json,sys; print(str(json.load(sys.stdin)["verification"]["verified"]).lower())')"
reason="$(printf '%s' "$tag_json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["verification"]["reason"])')"
object_type="$(printf '%s' "$tag_json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["object"]["type"])')"
tag_commit="$(printf '%s' "$tag_json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["object"]["sha"])')"

if [[ "$verified" != "true" || "$reason" != "valid" ]]; then
  echo "GitHub did not validate the signature on $tag (verified=$verified reason=$reason)" >&2
  exit 1
fi
if [[ "$object_type" != "commit" ]]; then
  echo "release tag $tag points to $object_type, not a commit" >&2
  exit 1
fi
if [[ "$tag_commit" != "$expected_commit" ]]; then
  echo "release tag $tag points to $tag_commit, workflow is running $expected_commit" >&2
  exit 1
fi
if [[ "$(git rev-parse "${tag}^{commit}")" != "$expected_commit" ]]; then
  echo "checked-out tag does not resolve to the workflow commit" >&2
  exit 1
fi

printf 'verified signed tag %s at %s\n' "$tag" "$expected_commit"
