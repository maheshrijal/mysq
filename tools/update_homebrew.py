"""Update the existing mysq formula from a stable release's checksums."""

import re
import sys
from pathlib import Path


def update_formula(tag, checksums, formula):
    if not re.fullmatch(r"v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)", tag):
        raise ValueError("Expected a stable release tag: vMAJOR.MINOR.PATCH")
    version = tag[1:]
    current = re.findall(r'^  version "(\d+\.\d+\.\d+)"$', formula, re.MULTILINE)
    if len(current) != 1:
        raise ValueError("Expected exactly one formula version")
    if tuple(map(int, version.split("."))) < tuple(map(int, current[0].split("."))):
        raise ValueError("Refusing to downgrade the formula")

    updated = formula.replace(f'  version "{current[0]}"', f'  version "{version}"', 1)
    for platform in ("darwin", "linux"):
        for arch in ("amd64", "arm64"):
            filename = f"mysq_{platform}_{arch}.tar.gz"
            hashes = re.findall(
                rf"^([0-9a-f]{{64}})  {re.escape(filename)}$", checksums, re.MULTILINE
            )
            if len(hashes) != 1:
                raise ValueError(f"Expected exactly one SHA-256 for {filename}")
            prefix = "https://github.com/maheshrijal/mysq/releases/download/"
            pattern = (
                rf'url "{re.escape(prefix)}v{re.escape(current[0])}/{re.escape(filename)}"'
                r'\n(\s+)sha256 "[0-9a-f]{64}"'
            )
            updated, count = re.subn(
                pattern,
                lambda match: f'url "{prefix}{tag}/{filename}"\n{match[1]}sha256 "{hashes[0]}"',
                updated,
            )
            if count != 1:
                raise ValueError(f"Expected exactly one formula URL and checksum for {filename}")
    if version == current[0] and updated != formula:
        raise ValueError("Refusing to change checksums for an already-published version")
    return updated


if __name__ == "__main__":
    tag, checksum_path, formula_path = sys.argv[1:]
    path = Path(formula_path)
    path.write_text(update_formula(tag, Path(checksum_path).read_text(), path.read_text()))
