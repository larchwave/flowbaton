#!/usr/bin/env python3
"""Repack one release directory into a deterministic tar.gz archive."""

import argparse
import gzip
import io
import os
from pathlib import Path
import stat
import tarfile


parser = argparse.ArgumentParser()
parser.add_argument("--root", type=Path, required=True)
parser.add_argument("--output", type=Path, required=True)
args = parser.parse_args()

root = args.root.resolve(strict=True)
if not root.is_dir():
    raise SystemExit(f"archive root is not a directory: {root}")
epoch = int(os.environ.get("SOURCE_DATE_EPOCH", "0"))

buffer = io.BytesIO()
with tarfile.open(fileobj=buffer, mode="w", format=tarfile.PAX_FORMAT) as archive:
    for item in [root, *sorted(root.rglob("*"), key=lambda path: path.as_posix())]:
        if item.is_symlink() or (not item.is_dir() and not item.is_file()):
            raise SystemExit(f"unsafe archive entry: {item}")
        relative = Path(root.name) if item == root else Path(root.name) / item.relative_to(root)
        info = tarfile.TarInfo(relative.as_posix() + ("/" if item.is_dir() else ""))
        info.uid = 0
        info.gid = 0
        info.uname = ""
        info.gname = ""
        info.mtime = epoch
        info.mode = stat.S_IMODE(item.stat().st_mode) or (0o755 if item.is_dir() else 0o644)
        if item.is_dir():
            info.type = tarfile.DIRTYPE
            archive.addfile(info)
        else:
            contents = item.read_bytes()
            info.size = len(contents)
            archive.addfile(info, io.BytesIO(contents))

compressed = io.BytesIO()
with gzip.GzipFile(fileobj=compressed, mode="wb", filename="", mtime=epoch) as output:
    output.write(buffer.getvalue())
args.output.write_bytes(compressed.getvalue())
os.chmod(args.output, 0o644)
