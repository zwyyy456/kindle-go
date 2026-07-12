#!/usr/bin/env python3
"""Portable compatibility entry point for the long-novel proofreader state tool."""

from pathlib import Path
import subprocess
import sys


SCRIPT = Path(__file__).resolve().parents[1] / "long-novel-proofreader" / "scripts" / "proofread_txt.py"


if __name__ == "__main__":
    raise SystemExit(subprocess.call([sys.executable, str(SCRIPT), *sys.argv[1:]]))
