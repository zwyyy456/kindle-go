#!/bin/sh
set -eu

go test ./...
python3 long-novel-proofreader/scripts/test_proofread_workflow.py
python3 long-epub-proofreader/scripts/test_epub_proofread_workflow.py
