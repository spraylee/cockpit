#!/bin/sh
# Install or upgrade cockpit (alias: ck) from GitHub Releases.
#   curl -fsSL https://github.com/spraylee/cockpit/releases/latest/download/install.sh | sh
#   sh install.sh v0.2.0          pin a version
#   COCKPIT_BIN_DIR=~/bin sh ...  install elsewhere
set -eu

repo=spraylee/cockpit
bindir=${COCKPIT_BIN_DIR:-"$HOME/.local/bin"}

case "$(uname -s):$(uname -m)" in
  Darwin:arm64) triple=aarch64-apple-darwin ;;
  Darwin:x86_64) triple=x86_64-apple-darwin ;;
  Linux:aarch64|Linux:arm64) triple=aarch64-unknown-linux-gnu ;;
  Linux:x86_64) triple=x86_64-unknown-linux-gnu ;;
  *) echo 'cockpit: no prebuilt release for this platform' >&2; exit 1 ;;
esac

tag=${1:-latest}
if [ "$tag" = latest ]; then
  url="https://github.com/$repo/releases/latest/download"
else
  printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || {
    echo 'cockpit: version looks like v0.2.0' >&2
    exit 1
  }
  url="https://github.com/$repo/releases/download/$tag"
fi

work=$(mktemp -d)
staged=''
cleanup() {
  rm -rf "$work"
  if [ -n "$staged" ]; then rm -f "$staged"; fi
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

curl -fsSL "$url/SHA256SUMS" -o "$work/SHA256SUMS"
# The tag is not known for "latest", so the asset name comes from the manifest.
asset=$(awk -v t="$triple" '$2 ~ ("^cockpit-v[0-9]+\\.[0-9]+\\.[0-9]+-" t "\\.tar\\.gz$") {print $2; exit}' "$work/SHA256SUMS")
[ -n "$asset" ] || { echo "cockpit: no release archive for $triple" >&2; exit 1; }
curl -fsSL "$url/$asset" -o "$work/$asset"

(
  cd "$work"
  awk -v a="$asset" '$2 == a {print; found++} END {if (found != 1) exit 1}' SHA256SUMS > asset.sha256
  if command -v shasum >/dev/null; then shasum -a 256 -c asset.sha256; else sha256sum -c asset.sha256; fi
)

mkdir -p "$bindir"
dest="$bindir/cockpit"
staged=$(mktemp "$bindir/.cockpit.XXXXXX")
tar -xzOf "$work/$asset" cockpit > "$staged"
chmod 755 "$staged"
# Prove the extracted binary runs before touching the installed one.
"$staged" --version >/dev/null
installed=$("$staged" --version)
mv -f "$staged" "$dest"
staged=''
# ck is a symlink, so `cockpit update` (which replaces the file in place)
# upgrades both names at once.
ln -sfn cockpit "$bindir/ck"

printf 'Installed cockpit %s -> %s\n' "$installed" "$dest"
case ":$PATH:" in *":$bindir:"*) ;; *) printf 'Add %s to PATH to use cockpit/ck by name.\n' "$bindir" ;; esac

# `ck` is also an alias of the concurrencykit formula; warn only if it would
# actually shadow ours.
other=$(command -v ck 2>/dev/null || true)
if [ -n "$other" ] && [ "$other" != "$bindir/ck" ]; then
  printf 'Warning: %s also provides ck and comes first in PATH.\n' "$other" >&2
fi
if ! command -v process-compose >/dev/null; then
  echo 'cockpit is installed. Install process-compose 1.124+ before starting a service.'
fi
exit 0
