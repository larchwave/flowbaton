#!/usr/bin/env python3
import argparse
import hashlib
from pathlib import Path
import re


parser = argparse.ArgumentParser()
parser.add_argument("--version", required=True)
parser.add_argument("--candidate", type=Path)
parser.add_argument(
    "--base-url",
    default="https://github.com/larchwave/flowbaton/releases/download/v{version}",
)
parser.add_argument("--output", type=Path)
parser.add_argument("--validate-only", action="store_true")
args = parser.parse_args()


identifier = r"(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)"
semver = re.compile(
    rf"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-({identifier}(?:\.{identifier})*))?$"
)
if not semver.fullmatch(args.version):
    parser.error(f"version must be strict SemVer without build metadata: {args.version}")
if args.validate_only:
    raise SystemExit(0)
if args.candidate is None or args.output is None:
    parser.error("--candidate and --output are required unless --validate-only is used")


def digest(name: str) -> str:
    path = args.candidate / name
    if not path.is_file():
        raise SystemExit(f"missing Homebrew archive: {path}")
    return hashlib.sha256(path.read_bytes()).hexdigest()


arm = f"flowbaton_{args.version}_darwin_arm64.tar.gz"
intel = f"flowbaton_{args.version}_darwin_amd64.tar.gz"
base_url = args.base_url.format(version="#{version}").rstrip("/")
cask_name = "flowbaton-beta" if "-" in args.version else "flowbaton"
cask = f'''cask "{cask_name}" do
  version "{args.version}"

  on_arm do
    sha256 "{digest(arm)}"
    url "{base_url}/{arm}"
    binary "flowbaton_#{{version}}_darwin_arm64/flowbaton"
  end

  on_intel do
    sha256 "{digest(intel)}"
    url "{base_url}/{intel}"
    binary "flowbaton_#{{version}}_darwin_amd64/flowbaton"
  end

  name "FlowBaton"
  desc "Pre-alpha mobile UI automation toolkit"
  homepage "https://github.com/larchwave/flowbaton"
end
'''
args.output.parent.mkdir(parents=True, exist_ok=True)
args.output.write_text(cask)
