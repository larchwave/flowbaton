#!/usr/bin/env python3
import argparse
import hashlib
from pathlib import Path


parser = argparse.ArgumentParser()
parser.add_argument("--version", required=True)
parser.add_argument("--candidate", type=Path, required=True)
parser.add_argument("--output", type=Path, required=True)
args = parser.parse_args()


def digest(name: str) -> str:
    path = args.candidate / name
    if not path.is_file():
        raise SystemExit(f"missing Homebrew archive: {path}")
    return hashlib.sha256(path.read_bytes()).hexdigest()


arm = f"flowbaton_{args.version}_darwin_arm64.tar.gz"
intel = f"flowbaton_{args.version}_darwin_amd64.tar.gz"
cask = f'''cask "flowbaton" do
  version "{args.version}"

  on_arm do
    sha256 "{digest(arm)}"
    url "https://github.com/larchwave/flowbaton/releases/download/v#{{version}}/{arm}"
    binary "flowbaton_#{{version}}_darwin_arm64/flowbaton"
  end

  on_intel do
    sha256 "{digest(intel)}"
    url "https://github.com/larchwave/flowbaton/releases/download/v#{{version}}/{intel}"
    binary "flowbaton_#{{version}}_darwin_amd64/flowbaton"
  end

  name "FlowBaton"
  desc "Pre-alpha mobile UI automation toolkit"
  homepage "https://github.com/larchwave/flowbaton"
end
'''
args.output.parent.mkdir(parents=True, exist_ok=True)
args.output.write_text(cask)
