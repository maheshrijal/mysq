#!/bin/sh
# Install a published mysq release. Override INSTALL_DIR or VERSION as needed.
set -eu

fail() {
    printf 'mysq: %s\n' "$*" >&2
    exit 1
}

# Read Cobra's version output without letting a binary consume curl | sh input.
binary_version() {
    version_output=$("$1" --version </dev/null 2>/dev/null) || return 1
    reported_version=$(printf '%s\n' "$version_output" | awk '$1 == "mysq" && $2 == "version" && NF == 3 { print $3; exit }')
    case "$reported_version" in
        ''|*[!a-zA-Z0-9.+_-]*) return 1 ;;
        [0-9]*) printf 'v%s' "$reported_version" ;;
        *) printf '%s' "$reported_version" ;;
    esac
}

quote_sh() {
    printf "'"
    printf '%s' "$1" | sed "s/'/'\\\\''/g"
    printf "'"
}

quote_fish() {
    printf "'"
    printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e "s/'/\\\\'/g"
    printf "'"
}

append_path_config() {
    config_file=$1
    config_line=$2
    config_block=$3
    if [ -f "$config_file" ] && grep -Fqx -- "$config_line" "$config_file"; then
        return
    fi
    if mkdir -p "$(dirname "$config_file")" &&
        printf '\n# mysq PATH\n%s\n' "$config_block" >> "$config_file"; then
        printf 'Updated %s\n' "$config_file"
    else
        config_failed=1
        printf 'mysq: could not update %s; add the install directory to PATH manually\n' "$config_file" >&2
    fi
}

configure_path() {
    sh_dir=$(quote_sh "$install_dir")
    fish_dir=$(quote_fish "$install_dir")
    if [ "${MYSQ_NO_MODIFY_PATH:-0}" != 1 ] && [ -n "${HOME:-}" ]; then
        config_failed=0
        # Keep existing startup files intact. In particular, creating a
        # .bash_profile would prevent Bash from reading an existing .profile.
        sh_line="    *) export PATH=$sh_dir:\"\$PATH\" ;;"
        sh_block="case \":\$PATH:\" in
    *:$sh_dir:*) ;;
$sh_line
esac"
        append_path_config "$HOME/.profile" "$sh_line" "$sh_block"
        append_path_config "$HOME/.bashrc" "$sh_line" "$sh_block"
        for login_config in "$HOME/.bash_profile" "$HOME/.bash_login"; do
            if [ -f "$login_config" ]; then
                append_path_config "$login_config" "$sh_line" "$sh_block"
                break
            fi
        done
        append_path_config "${ZDOTDIR:-$HOME}/.zshrc" "$sh_line" "$sh_block"
        fish_line="fish_add_path --path $fish_dir"
        append_path_config "${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/mysq.fish" "$fish_line" "$fish_line"
        if [ "$config_failed" = 0 ]; then
            printf 'PATH configured for sh, Bash, Zsh, and Fish. Open a new terminal to use mysq.\n'
        else
            printf 'Some shell configurations could not be updated; use the command below in those shells.\n'
        fi
    else
        printf 'Shell configuration unchanged. Add the install directory to PATH if needed.\n'
    fi

    # curl | sh runs in a child process; only the user can update the current shell.
    shell_name=${SHELL:-}
    case "${shell_name##*/}" in
        fish) printf 'For this terminal, run: fish_add_path --path %s\n' "$fish_dir" ;;
        sh|dash|bash|zsh|ksh|'') printf 'For this terminal, run: export PATH=%s:"$PATH"\n' "$sh_dir" ;;
        *) printf 'Add %s to PATH using your shell configuration.\n' "$install_dir" ;;
    esac
}

main() {
    for dependency in curl tar awk mktemp sed grep; do
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
    # Persist an absolute path so future shells do not depend on this directory.
    case "$install_dir" in /*) ;; *) install_dir=$PWD/$install_dir ;; esac
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
    install_action=Installed
    if [ -e "$install_dir/mysq" ] || [ -L "$install_dir/mysq" ]; then
        install_action=Updated
        previous_version=$(binary_version "$install_dir/mysq") || previous_version=unknown
    fi
    staged_binary=$(mktemp "$install_dir/.mysq.XXXXXX")
    cp "$temp_dir/mysq" "$staged_binary"
    chmod 755 "$staged_binary"
    installed_version=$(binary_version "$staged_binary") ||
        fail 'cannot read version from downloaded mysq; existing installation was not changed'
    mv -f "$staged_binary" "$install_dir/mysq"
    staged_binary=
    if [ "$install_action" = Updated ]; then
        printf 'Updated mysq %s → %s (%s/mysq)\n' "$previous_version" "$installed_version" "$install_dir"
    else
        printf 'Installed mysq %s (%s/mysq)\n' "$installed_version" "$install_dir"
    fi
    configure_path
}

main "$@"
