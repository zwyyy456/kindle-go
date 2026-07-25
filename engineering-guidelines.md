# Kindle Toolbox Engineering Guidelines

- Type: Normative
- Status: Active
- Last reviewed: 2026-07-26
- Scope: Go code, Python workflow integration, persistence, local HTTP surfaces, tests, and tooling

These rules adapt stable Go community conventions to this repository. They intentionally avoid a
large third-party lint policy: rules should either affect implementation/review decisions or map to
an executable check.

## 1. Go Style And Naming

- `gofmt` is authoritative. Do not hand-align code or maintain a competing formatting style.
- Package names are short, specific, lower-case, and describe the capability. Avoid catch-all
  packages such as `util`, `common`, `misc`, `types`, or `interfaces`.
- Names express domain meaning. Preserve conventional initialisms such as `ID`, `URL`, `HTTP`,
  `EPUB`, and `SHA256`.
- Default to the smallest useful visibility. Export an identifier only when another package needs
  the contract.
- Comments on exported contracts explain purpose or constraints. Internal comments explain why a
  non-obvious rule exists, not what the next statement does.

## 2. Modules And Dependencies

- Prefer deep modules: callers learn a small interface while parsing, validation, persistence
  ordering, cleanup, and error detail stay inside the owning package.
- Use the standard library before adding a dependency. A new dependency requires a concrete current
  need, license review, maintained upstream, and a clear removal or replacement path.
- Interfaces normally belong near the consumer and remain as narrow as the consumed capability.
  Do not introduce an interface when there is one implementation and no real test or runtime seam.
- Avoid generic runtime, context, manager, helper, or coordinator objects that merely collect
  unrelated dependencies or transfer an existing dependency graph.
- Keep dependency direction toward capabilities and persistence:

  ```text
  HTTP/CLI adapters -> use-case services -> store/external adapters -> format cores
  ```

  Format cores must not depend on HTTP, SQLite, task state, or library paths.

## 3. APIs, Context, And Resource Ownership

- Functions accept only the dependencies and values needed for the operation. Do not pass a complete
  handler, store, runtime, or configuration object to obtain one capability.
- A blocking or cancelable operation accepts `context.Context` as its first parameter. Do not store
  request contexts in long-lived structs.
- The creator of a resource defines who closes it. Close files, rows, response bodies, workers, and
  subprocess pipes on every success and failure path.
- Use `io.Reader` / `io.Writer` and streaming boundaries when the complete value does not need to be
  resident in memory.
- Configuration is validated once at its owning boundary. Callers should not repeat or reinterpret
  the same precedence and validation rules.

## 4. Errors And Failure Semantics

- Return errors across package boundaries; reserve `panic` for broken internal invariants that
  cannot be handled locally.
- Wrap errors with operation context using `%w` when callers may need `errors.Is` or `errors.As`.
  Error text starts lower-case and does not add redundant punctuation.
- Distinguish stable machine-readable error codes from user-facing messages and diagnostic detail.
  Persisted task failures must not require parsing human prose.
- Do not swallow errors, report false success, or silently switch to an older path. Best-effort
  cleanup may ignore an error only when the primary result is already determined and the choice is
  clear in code or logs.
- User-facing errors say what failed and, when the user can act, what action is available.

## 5. Concurrency And Long-Running Work

- Every goroutine has an owner and a termination condition. Long-running work supports cancellation
  and bounded shutdown.
- Protect shared mutable state with one clear synchronization strategy; do not mix ownership by
  goroutine, mutexes, and ad-hoc atomics for the same state.
- The sender owns closing a channel. Receivers do not close channels they did not create.
- Do not start background work from an HTTP request when a persistent task is the product owner of
  that work.
- Prevent stale or canceled results from committing files or overwriting newer state.
- Changes to queues, workers, cancellation, locks, or shared state require race-detector coverage.

## 6. Persistence, Files, And Input Boundaries

- Schema and file-layout changes are contracts. Define forward migration and rollback/read-only
  behavior before changing them.
- Run multi-record state transitions in transactions. Commit files through a temporary path plus
  atomic rename, and clean incomplete output after failures.
- Validate stored relative paths remain below their declared root before opening, serving, moving,
  or deleting them.
- Enforce request, file, archive-entry, expanded-size, JSON, and subprocess-output limits at the
  boundary that consumes them.
- Parse external formats strictly enough to reject ambiguity, while keeping format-specific
  compatibility decisions in the format package rather than HTTP or UI code.
- Escape untrusted values for their destination context. Never log book content, full prompts,
  model responses, secrets, management tokens, or signed URLs.

## 7. Tests

- Test through the public behavior seam when practical. A test should fail when the user-visible
  result or stable contract is wrong, not merely when an implementation string moves.
- Use table-driven tests when cases share behavior, `t.Run` for meaningful case names, `t.Helper`
  for helpers, and `t.TempDir` for filesystem state.
- Prefer deterministic synchronization over sleeps. Time-based behavior uses an injected clock or a
  bounded eventual assertion.
- Parsers, archive/path handling, and other hostile-input boundaries are good fuzz-test candidates.
  Add fuzzing when it protects a real parser or security boundary, not as a coverage ritual.
- Integration tests use real SQLite transactions and filesystem semantics where those behaviors are
  part of the contract.
- When a confirmed product contract changes, update or delete stale tests and remove the obsolete
  production path in the same change.

## 8. Logs And Diagnostics

- Critical workflow logs use stable fields such as `event`, `task_id`, `book_id`, `file_id`,
  `stage`, `duration`, and `error_code`.
- Logs identify the operation and failure boundary without duplicating persisted content.
- Expected user errors and dependency diagnostics must not be logged as unexplained stack traces.
- Diagnostic endpoints and settings pages report capabilities truthfully; a configured or detected
  executable is not equivalent to a successful real invocation.

## 9. Compatibility And Cleanup

- Compatibility code needs a real protected contract: a released CLI/configuration, an existing
  library, a stored schema, or a verified target-device limitation.
- Record the compatibility path's scope and deletion condition near its owner or in the relevant
  product/format document.
- Do not add a fallback because a test expects obsolete behavior. Do not leave a migrated path,
  dormant setting, adapter, or duplicate task implementation reachable “for safety”.
- After a new path owns the complete workflow, delete the old write path and its implementation-only
  fixtures.

## 10. Minimum Checks

- Documentation-only change: `git diff --check`.
- Go change: `gofmt` plus targeted `go test`.
- Normal cross-package change: `tools/test.sh`.
- Concurrency or lifecycle change: relevant `go test -race`.
- Repository-wide or release-oriented change: `go vet ./...`.

## References

- [Effective Go](https://go.dev/doc/effective_go)
- [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)
- [Go security best practices](https://go.dev/doc/security/best-practices)
- [Go fuzzing](https://go.dev/doc/security/fuzz/)
