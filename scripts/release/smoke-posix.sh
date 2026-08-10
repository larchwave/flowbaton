#!/usr/bin/env bash
set -euo pipefail

candidate="${1:?usage: smoke-posix.sh CANDIDATE_DIR VERSION}"
version="${2:?usage: smoke-posix.sh CANDIDATE_DIR VERSION}"
install_script="${GITHUB_WORKSPACE:-$(pwd)}/scripts/install.sh"
tmp="$(mktemp -d)"
server_pid=""
cleanup() {
  [[ -z "$server_pid" ]] || kill "$server_pid" 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

mkdir -p "$tmp/http/v${version}" "$tmp/home" "$tmp/bin"
cp "$candidate"/* "$tmp/http/v${version}/"
port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
python3 -m http.server "$port" --bind 127.0.0.1 --directory "$tmp/http" >"$tmp/http.log" 2>&1 &
server_pid=$!
for _ in $(seq 1 50); do
  curl -fsS "http://127.0.0.1:${port}/v${version}/checksums.txt" >/dev/null && break
  sleep 0.1
done

HOME="$tmp/home" \
FLOWBATON_VERSION="$version" \
FLOWBATON_BASE_URL="http://127.0.0.1:${port}/v${version}" \
FLOWBATON_INSTALL_DIR="$tmp/bin" \
sh "$install_script"

actual="$("$tmp/bin/flowbaton" --version)"
[[ "$actual" == "flowbaton ${version}" ]] || {
  echo "installed binary reported '$actual', expected 'flowbaton ${version}'" >&2
  exit 1
}

# Exercise the production driver-download path from an empty HOME. The local
# server deliberately has the same /vVERSION release shape as GitHub.
if [[ "$(uname -s)" == "Darwin" ]]; then
  HOME="$tmp/home" FLOWBATON_DRIVER_ASSET_BASE_URL="http://127.0.0.1:${port}" \
    "$tmp/bin/flowbaton" driver-setup -p ios
else
  HOME="$tmp/home" FLOWBATON_DRIVER_ASSET_BASE_URL="http://127.0.0.1:${port}" \
    "$tmp/bin/flowbaton" driver-setup -p android
fi
