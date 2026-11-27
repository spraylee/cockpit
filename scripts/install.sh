#!/bin/sh
set -eu

repo=spraylee/svc
bindir=${SVC_BIN_DIR:-"$HOME/.local/bin"}
case "$(uname -s):$(uname -m)" in
  Darwin:arm64) triple=aarch64-apple-darwin ;;
  Darwin:x86_64) triple=x86_64-apple-darwin ;;
  Linux:aarch64|Linux:arm64) triple=aarch64-unknown-linux-gnu ;;
  Linux:x86_64) triple=x86_64-unknown-linux-gnu ;;
  *) echo 'svc: this platform has no prebuilt release' >&2; exit 1 ;;
esac
command -v gh >/dev/null || { echo 'svc: install GitHub CLI and run gh auth login first' >&2; exit 1; }
tag=${1:-$(gh release view --repo "$repo" --json tagName --jq .tagName)}
if ! printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo 'svc: expected a version like v0.1.0' >&2; exit 1
fi
asset="svc-$tag-$triple.tar.gz"
work=$(mktemp -d)
staged=''
cleanup() { rm -rf "$work"; if [ -n "$staged" ]; then rm -f "$staged"; fi; }
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

gh release download "$tag" --repo "$repo" --dir "$work" --pattern "$asset" --pattern SHA256SUMS
(
  cd "$work"
  awk -v asset="$asset" '$2 == asset { print; found++ } END { if (found != 1) exit 1 }' SHA256SUMS > asset.sha256
  if command -v shasum >/dev/null; then shasum -a 256 -c asset.sha256; else sha256sum -c asset.sha256; fi
)
mkdir -p "$bindir"
dest="$bindir/svc"
if { [ -e "$dest" ] || [ -L "$dest" ]; } && [ "${SVC_REPLACE:-0}" != 1 ]; then
  echo "svc: $dest already exists; after checking it, use SVC_REPLACE=1 to replace it" >&2
  exit 1
fi
staged=$(mktemp "$bindir/.svc.XXXXXX")
tar -xzOf "$work/$asset" svc > "$staged"
chmod 755 "$staged"
# Verify the extracted binary before replacing anything already installed.
[ "$("$staged" --version)" = "$tag" ]
if [ "${SVC_REPLACE:-0}" = 1 ] && [ ! -L "$dest" ] && [ ! -d "$dest" ]; then
  mv -f "$staged" "$dest"
else
  if [ -e "$dest" ] || [ -L "$dest" ]; then
    echo "svc: refusing to replace a symlink or directory: $dest" >&2; exit 1
  fi
  ln "$staged" "$dest"
fi
printf 'Installed %s at %s\n' "$tag" "$dest"
case ":$PATH:" in *":$bindir:"*) ;; *) printf 'Add %s to PATH to use svc by name.\n' "$bindir" ;; esac
if ! command -v process-compose >/dev/null; then
  echo 'The CLI is installed. Install process-compose 1.124+ before starting a service.'
fi
