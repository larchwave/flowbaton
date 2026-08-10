#!/usr/bin/env bash
set -euo pipefail

candidate="${1:?usage: sign-notarize-darwin.sh CANDIDATE_DIR VERSION HOST_ARCH RECEIPT_DIR}"
version="${2:?usage: sign-notarize-darwin.sh CANDIDATE_DIR VERSION HOST_ARCH RECEIPT_DIR}"
host_arch="${3:?usage: sign-notarize-darwin.sh CANDIDATE_DIR VERSION HOST_ARCH RECEIPT_DIR}"
receipt_dir="${4:?usage: sign-notarize-darwin.sh CANDIDATE_DIR VERSION HOST_ARCH RECEIPT_DIR}"

for name in \
  APPLE_DEVELOPER_ID_APPLICATION \
  APPLE_DEVELOPER_ID_CERTIFICATE_BASE64 \
  APPLE_DEVELOPER_ID_CERTIFICATE_PASSWORD \
  APPLE_NOTARY_KEY_ID \
  APPLE_NOTARY_ISSUER_ID \
  APPLE_NOTARY_PRIVATE_KEY_BASE64
do
  [[ -n "${!name:-}" ]] || { echo "required release-signing secret is absent: $name" >&2; exit 1; }
done

case "$host_arch" in
  amd64) macho_arch=x86_64 ;;
  arm64) macho_arch=arm64 ;;
  *) echo "unsupported Darwin architecture: $host_arch" >&2; exit 2 ;;
esac

archive="$candidate/flowbaton_${version}_darwin_${host_arch}.tar.gz"
test -f "$archive"
tmp="$(mktemp -d)"
keychain="$tmp/release-signing.keychain-db"
keychain_password="$(openssl rand -hex 24)"
cleanup() {
  security delete-keychain "$keychain" 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

printf '%s' "$APPLE_DEVELOPER_ID_CERTIFICATE_BASE64" | openssl base64 -d -A -out "$tmp/developer-id.p12"
printf '%s' "$APPLE_NOTARY_PRIVATE_KEY_BASE64" | openssl base64 -d -A -out "$tmp/AuthKey.p8"
chmod 600 "$tmp/developer-id.p12" "$tmp/AuthKey.p8"

security create-keychain -p "$keychain_password" "$keychain"
security set-keychain-settings -lut 900 "$keychain"
security unlock-keychain -p "$keychain_password" "$keychain"
security import "$tmp/developer-id.p12" -k "$keychain" \
  -P "$APPLE_DEVELOPER_ID_CERTIFICATE_PASSWORD" -T /usr/bin/codesign
security set-key-partition-list -S apple-tool:,apple: -s -k "$keychain_password" "$keychain"

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
  --key "$tmp/AuthKey.p8" \
  --key-id "$APPLE_NOTARY_KEY_ID" \
  --issuer "$APPLE_NOTARY_ISSUER_ID" \
  --wait --output-format json >"$tmp/notary-submit.json"
submission_id="$(python3 -c 'import json,sys; data=json.load(open(sys.argv[1])); assert data.get("status") == "Accepted", data; print(data["id"])' "$tmp/notary-submit.json")"
xcrun notarytool log "$submission_id" \
  --key "$tmp/AuthKey.p8" \
  --key-id "$APPLE_NOTARY_KEY_ID" \
  --issuer "$APPLE_NOTARY_ISSUER_ID" >"$tmp/notary-log.json"
spctl --assess --type execute --verbose=4 "$binary"

export SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:?SOURCE_DATE_EPOCH is required for deterministic release archives}"
python3 scripts/release/repack-release-archive.py --root "$root" --output "$archive"
verify="$tmp/verify"
mkdir "$verify"
tar -xzf "$archive" -C "$verify"
codesign --verify --strict --verbose=2 "$verify/$(basename "$root")/flowbaton"
spctl --assess --type execute --verbose=4 "$verify/$(basename "$root")/flowbaton"

mkdir -p "$receipt_dir"
cp "$tmp/codesign.txt" "$receipt_dir/codesign-${host_arch}.txt"
cp "$tmp/notary-submit.json" "$receipt_dir/notary-submit-${host_arch}.json"
cp "$tmp/notary-log.json" "$receipt_dir/notary-log-${host_arch}.json"
