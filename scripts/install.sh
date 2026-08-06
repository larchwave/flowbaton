#!/bin/sh
# FlowBaton POSIX installer. Downloads a signed release archive, verifies its
# SHA-256 against the published checksums file, and installs the flowbaton
# binary. Fails closed on any mismatch.
#
#   curl -fsSL https://github.com/larchwave/flowbaton/releases/latest/download/install.sh | sh
#
# Environment overrides:
#   FLOWBATON_VERSION      release version without the leading "v" (default: latest)
#   FLOWBATON_INSTALL_DIR  install directory (default: /usr/local/bin)
#   FLOWBATON_BASE_URL     release download base (default: GitHub releases)
set -eu

REPO="larchwave/flowbaton"
INSTALL_DIR="${FLOWBATON_INSTALL_DIR:-/usr/local/bin}"

fail() {
	echo "install: $1" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || fail "required tool not found: $1"
}

need curl
need tar

# Resolve platform.
os="$(uname -s)"
arch="$(uname -m)"
case "$os" in
	Darwin) os="darwin" ;;
	Linux) os="linux" ;;
	*) fail "unsupported operating system: $os (use install.ps1 on Windows)" ;;
esac
case "$arch" in
	x86_64 | amd64) arch="amd64" ;;
	arm64 | aarch64) arch="arm64" ;;
	*) fail "unsupported architecture: $arch" ;;
esac
# The release build ships no linux/arm64 archive.
if [ "$os" = "linux" ] && [ "$arch" = "arm64" ]; then
	fail "no linux/arm64 release archive is published"
fi

# Resolve version.
version="${FLOWBATON_VERSION:-}"
if [ -z "$version" ]; then
	need sed
	tag="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
		sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
	[ -n "$tag" ] || fail "could not resolve the latest release tag"
	version="${tag#v}"
fi

base="${FLOWBATON_BASE_URL:-https://github.com/${REPO}/releases/download/v${version}}"
asset="flowbaton_${version}_${os}_${arch}.tar.gz"

# Verify SHA-256 with whichever tool is available.
if command -v sha256sum >/dev/null 2>&1; then
	sha_cmd="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
	sha_cmd="shasum -a 256"
else
	fail "required tool not found: sha256sum or shasum"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "install: downloading ${asset} (v${version})" >&2
curl -fsSL "${base}/${asset}" -o "${tmp}/${asset}" || fail "download failed: ${base}/${asset}"
curl -fsSL "${base}/checksums.txt" -o "${tmp}/checksums.txt" || fail "download failed: checksums.txt"

expected="$(grep " ${asset}\$" "${tmp}/checksums.txt" | awk '{print $1}' | head -n 1)"
[ -n "$expected" ] || fail "no checksum listed for ${asset}"
actual="$(cd "$tmp" && $sha_cmd "$asset" | awk '{print $1}')"
[ "$expected" = "$actual" ] || fail "checksum mismatch for ${asset}: expected ${expected}, got ${actual}"

tar -xzf "${tmp}/${asset}" -C "$tmp"
binary="${tmp}/flowbaton_${version}_${os}_${arch}/flowbaton"
[ -f "$binary" ] || fail "archive did not contain the flowbaton binary"

if [ -w "$INSTALL_DIR" ] || mkdir -p "$INSTALL_DIR" 2>/dev/null && [ -w "$INSTALL_DIR" ]; then
	install -m 0755 "$binary" "${INSTALL_DIR}/flowbaton"
elif command -v sudo >/dev/null 2>&1; then
	echo "install: elevating with sudo to write ${INSTALL_DIR}" >&2
	sudo install -m 0755 "$binary" "${INSTALL_DIR}/flowbaton"
else
	fail "cannot write ${INSTALL_DIR}; set FLOWBATON_INSTALL_DIR to a writable path"
fi

echo "install: flowbaton v${version} installed to ${INSTALL_DIR}/flowbaton" >&2
