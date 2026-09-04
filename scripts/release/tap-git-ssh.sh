#!/usr/bin/env bash
set -euo pipefail

key_file="${FLOWBATON_TAP_SSH_KEY_FILE:?tap deploy key file is required}"
known_hosts_file="${FLOWBATON_TAP_KNOWN_HOSTS_FILE:?tap known_hosts file is required}"

[[ -f "$key_file" ]] || { echo 'tap deploy key file does not exist' >&2; exit 1; }
[[ -f "$known_hosts_file" ]] || { echo 'tap known_hosts file does not exist' >&2; exit 1; }
if key_mode="$(stat -c '%a' "$key_file" 2>/dev/null)"; then
  :
else
  key_mode="$(stat -f '%Lp' "$key_file")"
fi
[[ "$key_mode" == 600 ]] || { echo 'tap deploy key file must have mode 600' >&2; exit 1; }

printf -v quoted_key_file '%q' "$key_file"
printf -v quoted_known_hosts_file '%q' "$known_hosts_file"
export GIT_SSH_COMMAND="ssh -i $quoted_key_file -o IdentitiesOnly=yes -o UserKnownHostsFile=$quoted_known_hosts_file -o GlobalKnownHostsFile=/dev/null -o StrictHostKeyChecking=yes"
unset FLOWBATON_TAP_SSH_KEY_FILE FLOWBATON_TAP_KNOWN_HOSTS_FILE HOMEBREW_TAP_SSH_KEY

exec git "$@"
