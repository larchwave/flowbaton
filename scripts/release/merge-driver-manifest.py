#!/usr/bin/env python3
import argparse
import json
from pathlib import Path


parser = argparse.ArgumentParser()
parser.add_argument("--version", required=True)
parser.add_argument("--output", type=Path, required=True)
parser.add_argument("fragments", nargs="+", type=Path)
args = parser.parse_args()

assets = [json.loads(path.read_text()) for path in args.fragments]
assets.sort(key=lambda asset: (asset["id"], asset["host_os"], asset["host_arch"]))
coordinates = {
    (asset["id"], asset["asset_version"], asset["platform"], asset["host_version"], asset["host_os"], asset["host_arch"])
    for asset in assets
}
if len(coordinates) != len(assets):
    raise SystemExit("duplicate driver asset coordinate")
required = {
    ("android-agent", "darwin", "amd64"),
    ("android-agent", "darwin", "arm64"),
    ("android-agent", "linux", "amd64"),
    ("android-agent", "windows", "amd64"),
    ("ios-simulator-runner", "darwin", "amd64"),
    ("ios-simulator-runner", "darwin", "arm64"),
}
present = {(asset["id"], asset["host_os"], asset["host_arch"]) for asset in assets}
if present != required:
    raise SystemExit(f"driver manifest coordinates differ: missing={sorted(required-present)} extra={sorted(present-required)}")

document = {
    "schema_version": "flowbaton.assets.v0",
    "manifest_version": args.version,
    "assets": assets,
}
args.output.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n")
