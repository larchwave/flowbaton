#!/usr/bin/env bash
set -euo pipefail

tag="${1:?usage: anonymous-release-probe.sh TAG COMMIT CANDIDATE_DIR PROVENANCE_BUNDLE GOVERNANCE_MANIFEST}"
commit="${2:?usage: anonymous-release-probe.sh TAG COMMIT CANDIDATE_DIR PROVENANCE_BUNDLE GOVERNANCE_MANIFEST}"
candidate="${3:?usage: anonymous-release-probe.sh TAG COMMIT CANDIDATE_DIR PROVENANCE_BUNDLE GOVERNANCE_MANIFEST}"
provenance_bundle="${4:?usage: anonymous-release-probe.sh TAG COMMIT CANDIDATE_DIR PROVENANCE_BUNDLE GOVERNANCE_MANIFEST}"
governance_manifest="${5:?usage: anonymous-release-probe.sh TAG COMMIT CANDIDATE_DIR PROVENANCE_BUNDLE GOVERNANCE_MANIFEST}"
repo=larchwave/flowbaton
tap_repo=larchwave/homebrew-flowbaton
version="${tag#v}"
cask=flowbaton
if [[ "$version" == *-* ]]; then
  cask=flowbaton-beta
fi

for credential in GH_TOKEN GITHUB_TOKEN HOMEBREW_TAP_TOKEN APPLE_NOTARY_PRIVATE_KEY_BASE64 APPLE_DEVELOPER_ID_CERTIFICATE_BASE64; do
  [[ -z "${!credential:-}" ]] || { echo "anonymous probe inherited credential: $credential" >&2; exit 1; }
done

tmp="$(mktemp -d)"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT INT TERM
mkdir -p "$tmp/download" "$tmp/home"
scripts/release/render-homebrew-cask.py \
  --version "$version" --candidate "$candidate" --output "$tmp/expected-cask.rb"

download_public_asset() {
  url="${1:?public asset URL is required}"
  output="${2:?public asset output is required}"
  effective_url="$(curl -fL --max-redirs 5 --proto '=https' --proto-redir '=https' \
    --output "$output" --silent --show-error --write-out '%{url_effective}' "$url")"
  python3 - "$effective_url" <<'PY'
import sys
from urllib.parse import urlparse
parsed=urlparse(sys.argv[1])
allowed={"github.com","objects.githubusercontent.com","release-assets.githubusercontent.com","github-releases.githubusercontent.com"}
if parsed.scheme != "https" or parsed.hostname not in allowed:
    raise SystemExit(f"release asset redirected to undeclared host: {sys.argv[1]}")
PY
}

python3 - "$governance_manifest" <<'PY'
import json,sys
manifest=json.load(open(sys.argv[1]))
required={item["id"] for item in manifest["surfaces"] if item["required_for_distributed_v1"]}
expected={"source-repository","release-assets","homebrew-tap","installers","project-documentation","release-attestation"}
if required != expected:
    raise SystemExit(f"anonymous probe coverage differs from governance manifest: required={sorted(required)} expected={sorted(expected)}")
for item in manifest["surfaces"]:
    if item["required_for_distributed_v1"] and (item["required_visibility"] != "public" or not item["anonymous_probe_profiles"]):
        raise SystemExit(f"required surface is not publicly probeable: {item['id']}")
PY

curl -fsSL --max-redirs 0 "https://api.github.com/repos/$repo" -o "$tmp/repository.json"
python3 - "$tmp/repository.json" <<'PY'
import json,sys
data=json.load(open(sys.argv[1]))
assert data.get("visibility") == "public", data.get("visibility")
assert data.get("private") is False, data.get("private")
assert (data.get("license") or {}).get("spdx_id") == "Apache-2.0", data.get("license")
PY

source_refs="$(git -c credential.helper= -c http.followRedirects=false ls-remote "https://github.com/${repo}.git" "refs/tags/$tag" "refs/tags/$tag^{}")"
peeled="$(printf '%s\n' "$source_refs" | awk -v ref="refs/tags/$tag^{}" '$2 == ref {print $1}')"
[[ "$peeled" == "$commit" ]] || { echo "anonymous tag resolved to '$peeled', expected '$commit'" >&2; exit 1; }
git -c credential.helper= -c http.followRedirects=false ls-remote "https://github.com/${tap_repo}.git" refs/heads/main >/dev/null

base="https://github.com/${repo}/releases/download/${tag}"
while IFS= read -r -d '' local_asset; do
  name="$(basename "$local_asset")"
  download_public_asset "$base/$name" "$tmp/download/$name"
  cmp "$local_asset" "$tmp/download/$name"
done < <(find "$candidate" -maxdepth 1 -type f -print0)

bundle_name="$(basename "$provenance_bundle")"
download_public_asset "$base/$bundle_name" "$tmp/download/$bundle_name"
cmp "$provenance_bundle" "$tmp/download/$bundle_name"
(cd "$tmp/download" && sha256sum --check checksums.txt)

while IFS= read -r -d '' artifact; do
  gh attestation verify "$artifact" \
    --bundle "$tmp/download/$bundle_name" \
    --repo "$repo" \
    --signer-workflow "$repo/.github/workflows/release-publish.yml" \
    --source-ref "refs/tags/$tag" \
    --deny-self-hosted-runners >/dev/null
done < <(find "$tmp/download" -maxdepth 1 -type f ! -name "$bundle_name" -print0)

curl -fsSL --max-redirs 0 \
  "https://raw.githubusercontent.com/${repo}/${commit}/docs/release-policy.md" \
  -o "$tmp/release-policy.md"
cmp docs/release-policy.md "$tmp/release-policy.md"
curl -fsSL --max-redirs 0 \
  "https://raw.githubusercontent.com/${repo}/${commit}/governance/public-delivery-surfaces.json" \
  -o "$tmp/public-delivery-surfaces.json"
cmp "$governance_manifest" "$tmp/public-delivery-surfaces.json"

curl -fsSL --max-redirs 0 \
  "https://raw.githubusercontent.com/${tap_repo}/main/Casks/${cask}.rb" \
  -o "$tmp/published-cask.rb"
cmp "$tmp/expected-cask.rb" "$tmp/published-cask.rb"

echo "anonymous release probe passed for every required governance surface at $tag" >&2
