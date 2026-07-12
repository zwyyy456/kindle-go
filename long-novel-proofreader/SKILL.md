---
name: long-novel-proofreader
description: Model-led, conservative proofreading for long Chinese TXT novels. Use when Codex must read an entire multi-chapter manuscript in resumable batches, discover contextual typos and missing characters beyond fixed patterns, independently verify proposed edits, remove only confirmed out-of-story site ads, preserve the source and authorial voice, and produce a revised copy plus auditable reports.
---

# Long Novel Proofreader

Proofread the entire manuscript with model reasoning. Use scripts only for deterministic preparation, hints, state, validation, and application. Never let a string match directly modify prose.

Read [references/typo-policy.md](references/typo-policy.md) before reviewing text. Read [references/review-contract.md](references/review-contract.md) before recording candidates or reviews.

## Non-negotiable rules

- Keep the source immutable and identify it by SHA-256.
- Send every non-overlapping source span through model first review, including spans with no script hints.
- Treat known typo and ad patterns as hints, not decisions.
- Preserve chapter order, encoding, line endings, names, invented terms, author afterwords, and in-story advertisements.
- Never automatically change style, punctuation, general grammar, 的/地/得, repetition, or ambiguous wording.
- Apply an edit without user input only when first review and an isolated second review both return `high` with exactly the same replacement.
- Record all candidates and rejected or unresolved findings. Do not silently drop them.

## Roles

### Orchestrator

Own the immutable source, state directory, glossary versions, batch scheduling, deduplication, conflicts, final application, and reports. Do not perform a second review in the same context immediately after first review when a fresh subagent is available.

### First-review subagent

Read the complete assigned batch and actively discover errors. Do not limit review to supplied hints. Return structured candidates only for the batch's core range; use overlap text for context. Propose glossary additions separately. Do not edit files.

### Verification subagent

Use a fresh context. Receive the candidate, source context, and current protected glossary, but not the first review's confidence or rationale. Independently return `high`, `review`, or `reject`, a replacement, and a reason. Do not edit files.

If subagents are unavailable, perform verification with an isolated prompt and record `review_mode: same_model_isolated`; never describe that as independent-model verification.

## Workflow

1. Initialize resumable state:

   ```bash
   python3 long-novel-proofreader/scripts/proofread_txt.py init \
     --input /path/to/source.txt \
     --state-dir /path/to/.proofread
   ```

2. Inspect `manifest.json`, status, chapter markers, encoding, size, and batch boundaries. Use `show-batch` to retrieve line-numbered model input. The default core size is about 12,000 characters with 600 characters of context overlap.
3. Process first-review batches in waves of 3–5 subagents. Give every agent the same glossary snapshot within a wave. Import returned JSONL candidates, mark even zero-candidate batches complete, then merge glossary proposals before starting the next wave.
4. Deduplicate candidates by immutable source span and proposed replacement. Resolve conflicting proposals as `review`; do not choose one automatically.
5. Use `show-review` to build verification input, then send it to fresh verification subagents. This command omits first-pass confidence and rationale. Import every verification result.
6. Record explicit user accept/reject decisions for unresolved candidates when provided.
7. Run `status`. Do not call the final applier until every core batch is complete and every candidate has a verification result.
8. Generate the revised TXT, concise report, and audit appendix:

   ```bash
   python3 long-novel-proofreader/scripts/apply_reviewed_edits.py \
     --state-dir /path/to/.proofread \
     --output /path/to/revised.txt \
     --report /path/to/proofreading-report.md \
     --audit /path/to/proofreading-audit.jsonl
   ```

9. Verify that the source hash is unchanged, output generation is reproducible, chapter order and protected terms remain, confirmed site banners are gone, and in-story advertisement references remain.

## Batch review

Review the prose itself before consulting hints. Check core categories: wrong characters, homophone misuse, explicit missing characters, and out-of-story site banners. Report grammar, punctuation, 的/地/得, awkwardness, or possible style issues only as `review`.

Use original-source line numbers and exact substrings. When the same substring occurs more than once on a line, provide a one-based `occurrence`. Never invent character offsets; the importer resolves and validates them against the immutable source.

## State and recovery

Keep state in `.proofread/`: `manifest.json`, `batches.jsonl`, `candidates.jsonl`, `reviews.jsonl`, `decisions.jsonl`, and `glossary.json`. Validate the source hash before every operation. Resume from the first incomplete batch. Keep state until the user accepts the final result.

## Completion gate

Declare completion only when:

- every core source span has completed first review;
- every candidate has an isolated verification result;
- every agreed `high` edit is applied or explicitly rejected by the user;
- unresolved items appear in the report;
- source hash, encoding, line endings, chapter order, and protected terms pass verification;
- regenerating from state produces identical output.

The deliverables are a distinct revised TXT, a concise Markdown report, a complete JSONL audit appendix, and a short handoff naming intentionally unchanged categories.
