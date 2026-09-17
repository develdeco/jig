#!/bin/sh
# Installs the latest (or a pinned) jig release for Linux or macOS.
# Windows: use install.ps1 instead.
#
# Environment variables:
#   JIG_VERSION      A release tag (e.g. v0.1.1) to install instead of the
#                     latest release.
#   JIG_RELEASE_URL   Overrides the base URL archives and checksums.txt are
#                     fetched from (mirrors, or a local snapshot such as
#                     file:///path/to/dist for testing).
#   JIG_INSTALL_DIR   Overrides the install directory (default: $HOME/.local/bin).
set -eu

repo_owner="develdeco"
repo_name="jig"

die() {
    echo "install.sh: error: $*" >&2
    exit 1
}

info() {
    echo "$*"
}

os_name=$(uname -s)
case "$os_name" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "unsupported OS: $os_name - use install.ps1 on Windows" ;;
esac

arch_name=$(uname -m)
case "$arch_name" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) die "unsupported architecture: $arch_name" ;;
esac

if [ -n "${JIG_VERSION:-}" ]; then
    default_base_url="https://github.com/$repo_owner/$repo_name/releases/download/$JIG_VERSION"
else
    default_base_url="https://github.com/$repo_owner/$repo_name/releases/latest/download"
fi
base_url="${JIG_RELEASE_URL:-$default_base_url}"

archive="jig_${os}_${arch}.tar.gz"

fetch() {
    url=$1
    out=$2
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL -o "$out" "$url"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O "$out" "$url"
    else
        die "curl or wget is required to download jig"
    fi
}

tmp_dir=$(mktemp -d)
cleanup() {
    rm -rf "$tmp_dir"
}
trap cleanup EXIT INT TERM

info "downloading $archive from $base_url..."
fetch "$base_url/$archive" "$tmp_dir/$archive" || die "failed to download $archive"
fetch "$base_url/checksums.txt" "$tmp_dir/checksums.txt" || die "failed to download checksums.txt"

info "verifying checksum..."
checksum_line=$(grep -F "  $archive" "$tmp_dir/checksums.txt" || true)
[ -n "$checksum_line" ] || die "no checksum entry for $archive in checksums.txt"

(
    cd "$tmp_dir"
    if command -v sha256sum >/dev/null 2>&1; then
        printf '%s\n' "$checksum_line" | sha256sum -c - >/dev/null
    elif command -v shasum >/dev/null 2>&1; then
        printf '%s\n' "$checksum_line" | shasum -a 256 -c - >/dev/null
    else
        echo "sha256sum or shasum is required to verify the download" >&2
        exit 1
    fi
) || die "checksum verification failed for $archive"

info "extracting..."
tar -xzf "$tmp_dir/$archive" -C "$tmp_dir" jig || die "failed to extract $archive"

install_dir="${JIG_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$install_dir"
install_path="$install_dir/jig"
mv "$tmp_dir/jig" "$install_path"
chmod +x "$install_path"

info "installed jig to $install_path"

case ":$PATH:" in
    *":$install_dir:"*) ;;
    *)
        info ""
        info "$install_dir is not on your PATH. Add it, e.g. by appending this to"
        info "your shell profile:"
        info "  export PATH=\"$install_dir:\$PATH\""
        ;;
esac

info ""
"$install_path" version

info ""
info 'next: run "jig skills install" to install the session skills'
