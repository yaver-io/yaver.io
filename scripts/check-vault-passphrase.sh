#!/usr/bin/env bash
set -euo pipefail

project="${1:-*}"

if [[ ! -f "$HOME/.yaver/vault.enc" ]]; then
  echo "No vault file found at $HOME/.yaver/vault.enc" >&2
  exit 1
fi

printf "Vault passphrase: " >&2
stty_orig="$(stty -g)"
trap 'stty "$stty_orig" 2>/dev/null || true' EXIT
stty -echo
IFS= read -r passphrase
stty "$stty_orig"
printf "\n" >&2

if [[ -z "$passphrase" ]]; then
  echo "Passphrase cannot be empty." >&2
  exit 1
fi

tmp_out="$(mktemp)"
tmp_err="$(mktemp)"
trap 'rm -f "$tmp_out" "$tmp_err"; stty "$stty_orig" 2>/dev/null || true' EXIT

if YAVER_VAULT_PASSPHRASE="$passphrase" yaver vault list --project "$project" >"$tmp_out" 2>"$tmp_err"; then
  echo "Valid vault passphrase."
  sed -n '1,40p' "$tmp_out"
  exit 0
fi

echo "Invalid vault passphrase, or the vault file is corrupted." >&2
sed -n '1,4p' "$tmp_err" >&2
exit 1
