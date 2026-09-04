#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp="$(mktemp -d)"
cleanup() {
  rm -rf -- "$tmp"
}
trap cleanup EXIT INT TERM

mkdir -p "$tmp/mock-bin" "$tmp/ssh material"
key_file="$tmp/ssh material/deploy-key"
known_hosts_file="$tmp/ssh material/known_hosts"
log="$tmp/git.log"
ssh_log="$tmp/ssh.log"
secret='deploy-key-private-material-must-not-escape'
printf '%s\n' "$secret" >"$key_file"
chmod 600 "$key_file"
printf '%s\n' 'github.com ssh-ed25519 pinned-test-key' >"$known_hosts_file"

# These single-quoted lines are the literal source of the mock executable.
# shellcheck disable=SC2016
printf '%s\n' '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  ': "${FLOWBATON_TAP_TEST_LOG:?}"' \
  'printf "%s\\n" "$GIT_SSH_COMMAND" >"$FLOWBATON_TAP_TEST_LOG"' \
  'printf "arg=%s\\n" "$@" >>"$FLOWBATON_TAP_TEST_LOG"' \
  'for name in HOMEBREW_TAP_SSH_KEY FLOWBATON_TAP_SSH_KEY_FILE FLOWBATON_TAP_KNOWN_HOSTS_FILE; do' \
  '  [[ -z "${!name:-}" ]] || printf "inherited=%s\\n" "$name" >>"$FLOWBATON_TAP_TEST_LOG"' \
  'done' \
  'sh -c "$GIT_SSH_COMMAND git@github.com"' \
  >"$tmp/mock-bin/git"
chmod +x "$tmp/mock-bin/git"
# shellcheck disable=SC2016
printf '%s\n' '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  ': "${FLOWBATON_TAP_TEST_SSH_LOG:?}"' \
  'printf "ssh-arg=%s\\n" "$@" >"$FLOWBATON_TAP_TEST_SSH_LOG"' \
  >"$tmp/mock-bin/ssh"
chmod +x "$tmp/mock-bin/ssh"

PATH="$tmp/mock-bin:$PATH" \
FLOWBATON_TAP_TEST_LOG="$log" \
FLOWBATON_TAP_TEST_SSH_LOG="$ssh_log" \
HOMEBREW_TAP_SSH_KEY="$secret" \
FLOWBATON_TAP_SSH_KEY_FILE="$key_file" \
FLOWBATON_TAP_KNOWN_HOSTS_FILE="$known_hosts_file" \
  "$root/scripts/release/tap-git-ssh.sh" clone git@github.com:larchwave/homebrew-flowbaton.git "$tmp/tap"

for required in \
  'ssh -i ' \
  'IdentitiesOnly=yes' \
  'UserKnownHostsFile=' \
  'GlobalKnownHostsFile=/dev/null' \
  'StrictHostKeyChecking=yes' \
  'arg=clone' \
  'arg=git@github.com:larchwave/homebrew-flowbaton.git'
do
  grep -Fq "$required" "$log" || { echo "mock tap Git invocation is missing: $required" >&2; exit 1; }
done

for required in \
  "ssh-arg=$key_file" \
  'ssh-arg=IdentitiesOnly=yes' \
  "ssh-arg=UserKnownHostsFile=$known_hosts_file" \
  'ssh-arg=GlobalKnownHostsFile=/dev/null' \
  'ssh-arg=StrictHostKeyChecking=yes' \
  'ssh-arg=git@github.com'
do
  grep -Fq "$required" "$ssh_log" || { echo "mock SSH invocation is missing: $required" >&2; exit 1; }
done

for forbidden in "$secret" 'HOMEBREW_TAP_SSH_KEY=' 'FLOWBATON_TAP_SSH_KEY_FILE=' 'FLOWBATON_TAP_KNOWN_HOSTS_FILE='; do
  if grep -Fq "$forbidden" "$log" "$ssh_log"; then
    echo "mock tap Git invocation inherited secret material: $forbidden" >&2
    exit 1
  fi
done

chmod 644 "$key_file"
if PATH="$tmp/mock-bin:$PATH" \
  FLOWBATON_TAP_TEST_LOG="$log" \
  FLOWBATON_TAP_TEST_SSH_LOG="$ssh_log" \
  FLOWBATON_TAP_SSH_KEY_FILE="$key_file" \
  FLOWBATON_TAP_KNOWN_HOSTS_FILE="$known_hosts_file" \
    "$root/scripts/release/tap-git-ssh.sh" status >"$tmp/insecure.out" 2>&1; then
  echo 'tap Git transport accepted an insecure deploy-key mode' >&2
  exit 1
fi
grep -Fq 'tap deploy key file must have mode 600' "$tmp/insecure.out"
