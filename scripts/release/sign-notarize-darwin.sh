#!/usr/bin/env bash
set -euo pipefail

credentials_any_set() {
  local name
  for name in "$@"; do
    [[ -z "${!name:-}" ]] || return 0
  done
  return 1
}

credentials_all_set() {
  local name
  for name in "$@"; do
    [[ -n "${!name:-}" ]] || return 1
  done
  return 0
}

validate_release_credentials() {
  local name
  for name in \
    APPLE_DEVELOPER_ID_APPLICATION \
    APPLE_DEVELOPER_ID_CERTIFICATE_BASE64 \
    APPLE_DEVELOPER_ID_CERTIFICATE_PASSWORD
  do
    [[ -n "${!name:-}" ]] || { echo "required release-signing secret is absent: $name" >&2; return 1; }
  done

  local api_names=(
    APPLE_NOTARY_KEY_ID
    APPLE_NOTARY_ISSUER_ID
    APPLE_NOTARY_PRIVATE_KEY_BASE64
  )
  local password_names=(
    APPLE_NOTARY_APPLE_ID
    APPLE_NOTARY_TEAM_ID
    APPLE_NOTARY_PASSWORD
  )
  local api_any=false password_any=false
  credentials_any_set "${api_names[@]}" && api_any=true
  credentials_any_set "${password_names[@]}" && password_any=true

  if [[ "$api_any" == true && "$password_any" == true ]]; then
    echo "notarization credential modes must not be mixed" >&2
    return 1
  fi
  if [[ "$api_any" == true ]]; then
    credentials_all_set "${api_names[@]}" || {
      echo "incomplete App Store Connect API key notarization credentials" >&2
      return 1
    }
    notary_auth_mode=api-key
    return 0
  fi
  if [[ "$password_any" == true ]]; then
    credentials_all_set "${password_names[@]}" || {
      echo "incomplete app-specific password notarization credentials" >&2
      return 1
    }
    notary_auth_mode=app-specific-password
    return 0
  fi
  echo "notarization credentials are absent: configure one complete authentication mode" >&2
  return 1
}

check_credentials=false
if [[ "${1:-}" == "--check-credentials" ]]; then
  check_credentials=true
  shift
fi

if [[ "$check_credentials" == false ]]; then
  candidate="${1:?usage: sign-notarize-darwin.sh CANDIDATE_DIR VERSION HOST_ARCH RECEIPT_DIR}"
  version="${2:?usage: sign-notarize-darwin.sh CANDIDATE_DIR VERSION HOST_ARCH RECEIPT_DIR}"
  host_arch="${3:?usage: sign-notarize-darwin.sh CANDIDATE_DIR VERSION HOST_ARCH RECEIPT_DIR}"
  receipt_dir="${4:?usage: sign-notarize-darwin.sh CANDIDATE_DIR VERSION HOST_ARCH RECEIPT_DIR}"
fi

notary_auth_mode=
validate_release_credentials
[[ "$check_credentials" == false ]] || exit 0

case "$host_arch" in
  amd64) macho_arch=x86_64 ;;
  arm64) macho_arch=arm64 ;;
  *) echo "unsupported Darwin architecture: $host_arch" >&2; exit 2 ;;
esac

archive="$candidate/flowbaton_${version}_darwin_${host_arch}.tar.gz"
test -f "$archive"
developer_id_intermediate=scripts/release/certificates/DeveloperIDCA.cer
developer_id_intermediate_sha256=7afc9d01a62f03a2de9637936d4afe68090d2de18d03f29c88cfb0b1ba63587f
printf '%s  %s\n' "$developer_id_intermediate_sha256" "$developer_id_intermediate" | shasum -a 256 --check -
tmp="$(mktemp -d)"
keychain="$tmp/release-signing.keychain-db"
original_keychains=()
search_list_changed=false
cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [[ "$search_list_changed" == true ]] &&
    ! security list-keychains -d user -s "${original_keychains[@]}" >/dev/null; then
    echo 'failed to restore the user keychain search list' >&2
    status=1
  fi
  security delete-keychain "$keychain" 2>/dev/null || true
  rm -rf "$tmp"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

keychain_password="$(openssl rand -hex 24)"
original_keychain_list="$(security list-keychains -d user)"
while IFS= read -r listed_keychain; do
  listed_keychain="${listed_keychain#*\"}"
  listed_keychain="${listed_keychain%\"*}"
  [[ -z "$listed_keychain" ]] || original_keychains+=("$listed_keychain")
done <<<"$original_keychain_list"

printf '%s' "$APPLE_DEVELOPER_ID_CERTIFICATE_BASE64" | openssl base64 -d -A -out "$tmp/developer-id.p12"
chmod 600 "$tmp/developer-id.p12"

notary_auth_args=()
if [[ "$notary_auth_mode" == api-key ]]; then
  printf '%s' "$APPLE_NOTARY_PRIVATE_KEY_BASE64" | openssl base64 -d -A -out "$tmp/AuthKey.p8"
  chmod 600 "$tmp/AuthKey.p8"
  notary_auth_args=(
    --key "$tmp/AuthKey.p8"
    --key-id "$APPLE_NOTARY_KEY_ID"
    --issuer "$APPLE_NOTARY_ISSUER_ID"
  )
else
  notary_auth_args=(
    --apple-id "$APPLE_NOTARY_APPLE_ID"
    --password "$APPLE_NOTARY_PASSWORD"
    --team-id "$APPLE_NOTARY_TEAM_ID"
  )
fi

security create-keychain -p "$keychain_password" "$keychain"
security set-keychain-settings -lut 900 "$keychain"
security unlock-keychain -p "$keychain_password" "$keychain"
security list-keychains -d user -s "$keychain" "${original_keychains[@]}" >/dev/null
search_list_changed=true
security import "$developer_id_intermediate" -k "$keychain"
security import "$tmp/developer-id.p12" -k "$keychain" \
  -P "$APPLE_DEVELOPER_ID_CERTIFICATE_PASSWORD" -T /usr/bin/codesign
security set-key-partition-list -S apple-tool:,apple: -s -k "$keychain_password" "$keychain" >/dev/null

tar -xzf "$archive" -C "$tmp"
root="$tmp/flowbaton_${version}_darwin_${host_arch}"
binary="$root/flowbaton"
test -x "$binary"
actual_arches="$(xcrun lipo -archs "$binary")"
if [[ "$actual_arches" != "$macho_arch" ]]; then
  echo "Darwin CLI contains '$actual_arches'; expected exactly '$macho_arch'" >&2
  exit 1
fi

codesign --force --options runtime --timestamp \
  --identifier dev.larchwave.flowbaton.cli \
  --keychain "$keychain" \
  --sign "$APPLE_DEVELOPER_ID_APPLICATION" \
  "$binary"
codesign --verify --strict --verbose=2 "$binary"
codesign --display --verbose=4 "$binary" 2>"$tmp/codesign.txt"
grep -Fq 'Authority=Developer ID Application:' "$tmp/codesign.txt"

notary_payload="$tmp/flowbaton_${version}_darwin_${host_arch}-notary.zip"
ditto -c -k --keepParent "$binary" "$notary_payload"
xcrun notarytool submit "$notary_payload" \
  "${notary_auth_args[@]}" \
  --wait --output-format json >"$tmp/notary-submit.json"
submission_id="$(python3 -c 'import json,sys; data=json.load(open(sys.argv[1])); assert data.get("status") == "Accepted", data; print(data["id"])' "$tmp/notary-submit.json")"
xcrun notarytool log "$submission_id" \
  "${notary_auth_args[@]}" >"$tmp/notary-log.json"
codesign -vvvv -R='notarized' --check-notarization "$binary"

export SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:?SOURCE_DATE_EPOCH is required for deterministic release archives}"
python3 scripts/release/repack-release-archive.py --root "$root" --output "$archive"
verify="$tmp/verify"
mkdir "$verify"
tar -xzf "$archive" -C "$verify"
codesign --verify --strict --verbose=2 "$verify/$(basename "$root")/flowbaton"
codesign -vvvv -R='notarized' --check-notarization "$verify/$(basename "$root")/flowbaton"

mkdir -p "$receipt_dir"
cp "$tmp/codesign.txt" "$receipt_dir/codesign-${host_arch}.txt"
cp "$tmp/notary-submit.json" "$receipt_dir/notary-submit-${host_arch}.json"
cp "$tmp/notary-log.json" "$receipt_dir/notary-log-${host_arch}.json"
