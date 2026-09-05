#!/bin/sh
# Install a published mysq release. Override INSTALL_DIR or VERSION as needed.
set -eu

fail() {
    printf 'mysq: %s\n' "$*" >&2
    exit 1
}

main() {
    for dependency in curl tar awk mktemp; do
        command -v "$dependency" >/dev/null 2>&1 || fail "required command not found: $dependency"
    done
    case "$(uname -s)" in
        Linux) platform=linux ;;
        Darwin) platform=darwin ;;
        *) fail 'installer supports macOS and Linux; download Windows ZIPs from https://github.com/maheshrijal/mysq/releases' ;;
    esac
    case "$(uname -m)" in
        x86_64|amd64) architecture=amd64 ;;
        arm64|aarch64) architecture=arm64 ;;
        *) fail 'supported architectures are amd64 and arm64' ;;
    esac
    if command -v sha256sum >/dev/null 2>&1; then
        checksum_command=sha256sum
    elif command -v shasum >/dev/null 2>&1; then
        checksum_command=shasum
    else
        fail 'SHA-256 verification requires sha256sum or shasum'
    fi

    version=${VERSION:-latest}
    release_url=https://github.com/maheshrijal/mysq/releases
    if [ "$version" = latest ]; then
        download_url=$release_url/latest/download
    else
        case "$version" in
            *[!a-zA-Z0-9._-]*|[!v]*|v) fail 'VERSION must be a release tag such as v0.1.0' ;;
        esac
        download_url=$release_url/download/$version
    fi
    archive=mysq_${platform}_${architecture}.tar.gz
    install_dir=${INSTALL_DIR:-"${HOME:?set HOME or INSTALL_DIR}/.local/bin"}
    temp_dir=$(mktemp -d)
    staged_binary=
    trap 'rm -rf "$temp_dir"; if [ -n "$staged_binary" ]; then rm -f "$staged_binary"; fi' 0
    trap 'exit 1' HUP INT TERM

    printf 'Downloading mysq %s for %s/%s…\n' "$version" "$platform" "$architecture"
    curl --proto '=https' --tlsv1.2 -fsSL "$download_url/checksums.txt" -o "$temp_dir/checksums.txt" ||
        fail "cannot download release checksums; check the release tag and network connection: $release_url (a published release is required)"
    curl --proto '=https' --tlsv1.2 -fsSL "$download_url/$archive" -o "$temp_dir/$archive" ||
        fail "cannot download $archive from $release_url"
    expected=$(awk -v name="$archive" '$2 == name {print $1}' "$temp_dir/checksums.txt")
    [ "${#expected}" -eq 64 ] || fail "missing or invalid checksum for $archive"
    case "$expected" in *[!0-9a-fA-F]*) fail "invalid checksum for $archive" ;; esac
    if [ "$checksum_command" = sha256sum ]; then
        actual=$(sha256sum "$temp_dir/$archive")
    else
        actual=$(shasum -a 256 "$temp_dir/$archive")
    fi
    actual=${actual%% *}
    [ "$actual" = "$expected" ] || fail 'checksum mismatch; existing installation was not changed'

    tar -xzf "$temp_dir/$archive" -C "$temp_dir" mysq
    [ -f "$temp_dir/mysq" ] && [ ! -L "$temp_dir/mysq" ] || fail 'release archive has no regular mysq binary'
    mkdir -p "$install_dir"
    staged_binary=$(mktemp "$install_dir/.mysq.XXXXXX")
    cp "$temp_dir/mysq" "$staged_binary"
    chmod 755 "$staged_binary"
    mv -f "$staged_binary" "$install_dir/mysq"
    staged_binary=
    printf 'Installed %s/mysq\n' "$install_dir"
    case ":${PATH:-}:" in
        *":$install_dir:"*) printf 'Run: mysq --help\n' ;;
        *) printf 'Add this directory to PATH in your shell configuration: %s\n' "$install_dir" ;;
    esac
}

main "$@"
