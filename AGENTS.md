# Repository Guidelines

This file is the execution entry for agents and contributors working in this repository. Keep it
small and decision-oriented; detailed rules belong in the linked normative documents.

## 1. Project Scope

- The project is a Go 1.22 CLI and local Web application for Kindle content workflows.
- The main product lines are `vocab`, `txt2epub`, `serve`, and `transfer`.
- The Web UI and Kindle listener target a trusted local network. They are not public hosting
  surfaces and must not silently acquire public-service assumptions.
- Transfer control and storage roles are separate HTTPS deployment surfaces. Preserve their small
  control-body limit, signed data-plane authorization, exact-origin CORS, expiry, and revocation
  boundaries; do not apply the trusted-LAN assumptions of `serve` to them.
- `transfer` is an independent six-digit transfer product line. Its control plane never relays file
  bodies; WebRTC is the online path, while R2 and Server A are offline data-plane adapters required
  for Kindle browsers. Its acceptance status does not define Kindle Toolbox Web UI v1 completion.

## 2. Rule Precedence

Implementation decisions use this precedence:

1. `AGENTS.md`
2. `docs/product-design.md` for user-visible Web UI v1 behavior and product boundaries
3. `engineering-guidelines.md` for repository-wide engineering rules
4. `docs/development-plan.md` as an implementation reference, not a current-status source
5. `README.md` and tool-specific READMEs for current usage and operational instructions

When documents conflict, update or remove the stale statement. Do not preserve contradictions by
explaining them through precedence.

## 3. Context Loading

Read only the smallest set that can change the task:

| Change scope | Required context |
| --- | --- |
| Project orientation or commands | `README.md` |
| Web UI behavior, scope, or product copy | `docs/product-design.md` |
| Architecture, persistence, tasks, proofreading, or a cross-package change | `engineering-guidelines.md`; use `docs/development-plan.md` for the original design rationale |
| AZW3 compatibility | `docs/azw3-compatibility.md` and the affected `internal/azw3` / `internal/epub` code |
| TXT or EPUB proofreading protocol | The affected proofreader `SKILL.md` and its referenced contracts |
| Six-digit transfer | `docs/transfer.md`, `docs/transfer-real-device-testing.md`, and affected `internal/transfer` code |

## 4. Implementation Rules

- Fix confirmed root causes and close the affected user path. Keep the change within that boundary.
- Prefer the simplest design that expresses the current product model and failure semantics.
- Do not add speculative interfaces, adapters, fallback paths, compatibility branches, settings, or
  runtime switches.
- Preserve compatibility only for a real released CLI, configuration, database, file layout, or
  target-device behavior. State the protected contract and its removal condition.
- Keep business ownership in `library`, `generation`, `task`, `proofread`, `settings`, and
  `transfer`. HTTP and CLI packages adapt requests and render results; stores persist state but do
  not own workflows.
- Put interfaces at boundaries that truly vary, especially external processes, time, and task
  executors. Do not create pass-through interfaces for every store or service.
- Split large files by ownership, user flow, or platform boundary—not by line count alone.
- A stateful change must identify the state owner, readers, writers, persistence location, recovery
  behavior, and reset condition. One business state has one write authority.
- Treat source files as immutable. Derived files and task results are committed atomically and must
  not be exposed before they are ready.

## 5. Data And Compatibility

- SQLite migrations move forward by version and run transactionally. Never edit an already released
  migration in place.
- Changes to persisted schema, task/file states, configuration, report formats, or public CLI
  behavior must define migration, compatibility, explicit rejection, or reset behavior.
- Reject unsupported newer versions explicitly. Do not guess at a compatible interpretation.
- External input remains untrusted even on a trusted LAN: enforce size limits, strict parsing,
  archive expansion limits, safe paths, and output escaping at the owning boundary.

## 6. Verification

- Format changed Go files with `gofmt`.
- Run the narrowest relevant test while iterating.
- Before completing a normal code change, run `tools/test.sh`.
- Run `go test -race` for packages whose concurrency, cancellation, shared state, or worker lifecycle
  changed.
- Run `go vet ./...` for cross-package or release-oriented changes.
- Run `git diff --check` for every change.
- Tests should assert product behavior or stable contracts. Do not retain obsolete production paths
  only to satisfy stale tests.
- Real Codex, Calibre, browser, Kindle, R2, and network checks are explicit environment acceptance;
  never report them as completed from unit tests alone.

## 7. Documentation

- One topic has one normative source. Other documents link to it instead of restating it.
- `docs/product-design.md` defines desired behavior, not implementation progress.
- `docs/development-plan.md` preserves implementation rationale and historical sequencing; it must
  not be used as evidence that the current code still has its initial limitations.
- Update normative documentation in the same change when a product or engineering contract changes.
- Comments explain constraints and non-obvious decisions, not the literal code.
