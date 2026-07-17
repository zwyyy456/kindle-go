#!/bin/sh
set -eu

if [ "${KINDLE_GO_CONFIRM_REAL_CODEX:-}" != "1" ]; then
  echo "Refusing to invoke the real model without KINDLE_GO_CONFIRM_REAL_CODEX=1." >&2
  echo "This opt-in test sends only bundled synthetic TXT/EPUB fixtures to the logged-in Codex CLI." >&2
  exit 2
fi

go test -tags=realcodex ./internal/proofread -run '^TestRealCodexTXTAndEPUBAcceptance$' -count=1 -timeout=70m -v
