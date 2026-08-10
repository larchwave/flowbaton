#!/usr/bin/env python3
"""Create one deterministic driver archive and its assets.v0 descriptor."""

import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import stat
import tarfile


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def file_mode(path: Path) -> int:
    mode = stat.S_IMODE(path.stat().st_mode)
    return mode if mode else 0o644


def archive_tree(root: Path, epoch: int) -> tuple[bytes, list[dict]]:
    regular_files: list[Path] = []
    directories: set[Path] = set()
    for item in root.rglob("*"):
        relative = item.relative_to(root)
        if item.is_symlink():
            raise SystemExit(f"driver asset contains a symlink: {relative.as_posix()}")
        if item.is_dir():
            continue
        if not item.is_file():
            raise SystemExit(f"driver asset contains a non-regular entry: {relative.as_posix()}")
        regular_files.append(item)
        parent = relative.parent
        while parent != Path("."):
            directories.add(parent)
            parent = parent.parent
    entries = sorted(
        [root / directory for directory in directories] + regular_files,
        key=lambda item: item.relative_to(root).as_posix(),
    )
    files: list[dict] = []
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w", format=tarfile.PAX_FORMAT) as archive:
        for item in entries:
            relative = item.relative_to(root).as_posix()
            info = tarfile.TarInfo(relative + ("/" if item.is_dir() else ""))
            info.uid = 0
            info.gid = 0
            info.uname = ""
            info.gname = ""
            info.mtime = epoch
            info.mode = file_mode(item)
            if item.is_dir():
                info.type = tarfile.DIRTYPE
                archive.addfile(info)
                continue
            contents = item.read_bytes()
            info.size = len(contents)
            archive.addfile(info, io.BytesIO(contents))
            files.append(
                {
                    "path": relative,
                    "sha256": sha256(contents),
                    "size": len(contents),
                    "mode": f"0{file_mode(item):03o}",
                }
            )
    if not files:
        raise SystemExit("driver asset is empty")
    return buffer.getvalue(), files


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--id", required=True)
    parser.add_argument("--platform", choices=("android", "ios-simulator"), required=True)
    parser.add_argument("--host-version", required=True)
    parser.add_argument("--asset-version", required=True)
    parser.add_argument("--host-os", choices=("darwin", "linux", "windows"), required=True)
    parser.add_argument("--host-arch", choices=("amd64", "arm64"), required=True)
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--identity-kind", required=True)
    parser.add_argument("--identity-value", required=True)
    parser.add_argument("--identity-path", required=True)
    parser.add_argument("--android-api-min", type=int, default=0)
    parser.add_argument("--android-api-max", type=int, default=0)
    parser.add_argument("--xcode-min", default="")
    parser.add_argument("--xcode-max", default="")
    parser.add_argument("--ios-runtime-min", default="")
    parser.add_argument("--ios-runtime-max", default="")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()

    root = args.root.resolve(strict=True)
    if not root.is_dir():
        raise SystemExit(f"asset root is not a directory: {root}")
    identity = root / args.identity_path
    if not identity.exists() or identity.is_symlink():
        raise SystemExit(f"identity path is missing or unsafe: {args.identity_path}")

    epoch = int(os.environ.get("SOURCE_DATE_EPOCH", "0"))
    tar_bytes, files = archive_tree(root, epoch)
    compressed = io.BytesIO()
    with gzip.GzipFile(fileobj=compressed, mode="wb", filename="", mtime=epoch) as output:
        output.write(tar_bytes)
    archive_bytes = compressed.getvalue()

    budget = 20 * 1024 * 1024 if args.platform == "android" else 25 * 1024 * 1024
    if len(archive_bytes) > budget:
        raise SystemExit(f"compressed driver archive is {len(archive_bytes)} bytes; budget is {budget}")

    args.output.mkdir(parents=True, exist_ok=True)
    archive_name = (
        f"flowbaton_{args.host_version}_{args.id}_{args.asset_version}_"
        f"{args.host_os}_{args.host_arch}.tar.gz"
    )
    archive_path = args.output / archive_name
    archive_path.write_bytes(archive_bytes)
    os.chmod(archive_path, 0o644)

    descriptor = {
        "id": args.id,
        "status": "release",
        "host_version": args.host_version,
        "asset_version": args.asset_version,
        "host_os": args.host_os,
        "host_arch": args.host_arch,
        "platform": args.platform,
        "asset_hash": sha256(tar_bytes),
        "archive": {
            "format": "tar+gzip",
            "sha256": sha256(archive_bytes),
            "size": len(archive_bytes),
            "uncompressed_sha256": sha256(tar_bytes),
            "uncompressed_size": len(tar_bytes),
        },
        "files": files,
        "identity": {
            "kind": args.identity_kind,
            "value": args.identity_value,
            "path": args.identity_path,
        },
        "compatibility": {
            "android_api": {"min": args.android_api_min, "max": args.android_api_max},
            "xcode": {"min": args.xcode_min, "max": args.xcode_max},
            "ios_runtime": {"min": args.ios_runtime_min, "max": args.ios_runtime_max},
        },
    }
    fragment = args.output / f"{args.id}-{args.host_os}-{args.host_arch}.asset.json"
    fragment.write_text(json.dumps(descriptor, indent=2, sort_keys=True) + "\n")
    print(archive_path)


if __name__ == "__main__":
    main()
