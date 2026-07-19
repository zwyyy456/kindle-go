---
name: long-novel-proofreader
description: Model-led, conservative proofreading for long Chinese TXT novels. Use when Codex must read an entire multi-chapter manuscript in resumable batches, discover character and word defects, omissions, sentence and local-semantic corruption, abnormal boundaries or punctuation, text contamination, and cross-batch entity or continuity inconsistencies, independently verify proposed edits, preserve the source and authorial voice, and produce a revised copy plus auditable reports.
---

# Long Novel Proofreader

Proofread the entire manuscript with model reasoning. Use scripts only for deterministic preparation, hints, state, validation, and application. Never let a string match directly modify prose.

Read [references/typo-policy.md](references/typo-policy.md) before reviewing text. Read [references/review-contract.md](references/review-contract.md) before recording candidates or reviews.

## Non-negotiable rules

- Keep the source immutable and identify it by SHA-256.
- Send every non-overlapping source span through model first review, including spans with no script hints.
- Treat known typo and ad patterns as hints, not decisions.
- Preserve chapter order, encoding, line endings, names, invented terms, author afterwords, and in-story advertisements.
- Never automatically copyedit style, optional punctuation, general grammar, 的/地/得, repetition, or ambiguous wording. This does not exempt definite textual corruption such as a uniquely recoverable missing word or punctuation inserted inside an established word.
- Apply an edit without user input only when first review and an isolated second review both return `high` with exactly the same replacement.
- Record all candidates and rejected or unresolved findings. Do not silently drop them.
- Complete both the exhaustive local batch pass and the global consistency pass. Neither substitutes for the other.

## Roles

### Orchestrator

Own the immutable source, state directory, glossary versions, batch scheduling, deduplication, conflicts, final application, and reports. Do not perform a second review in the same context immediately after first review when a fresh subagent is available.

### First-review subagent

Read the complete assigned batch and actively discover errors. Do not limit review to supplied hints. Return structured candidates only for the batch's core range; use overlap text for context. Propose glossary additions separately. Do not edit files.

### Verification subagent

Use a fresh context. Receive the candidate, source context, and current protected glossary, but not the first review's confidence or rationale. Independently return `high`, `review`, or `reject`, a replacement, and a reason. Do not edit files.

If subagents are unavailable, perform verification with an isolated prompt and record `review_mode: same_model_isolated`; never describe that as independent-model verification.

### Global-consistency subagent

Use merged context notes and the protected glossary only to locate possible conflicts. Reopen the relevant source batches before recording a candidate. Treat entity, reference, timeline, location, and plot-state conflicts as `review` unless one local source span has a unique minimal textual repair.

## Workflow

1. Initialize resumable state:

   ```bash
   python3 long-novel-proofreader/scripts/proofread_txt.py init \
     --input /path/to/source.txt \
     --state-dir /path/to/.proofread
   ```

2. Inspect `manifest.json`, status, chapter markers, encoding, size, and batch boundaries. Use `show-batch` to retrieve line-numbered model input. The default core size is about 12,000 characters with 600 characters of context overlap.
3. Process first-review batches in waves of 3–5 subagents. Give every agent the same glossary snapshot within a wave. Import candidates and concise context notes, mark even zero-candidate batches complete, then merge glossary proposals before the next wave. Context notes record only facts useful for later consistency checks; they are not edit candidates.
4. After all batches complete, run `show-context` by subject or kind. Check entity/name/reference consistency, relationships, time, location, character state, chapter transitions, repeated or missing passages, paired structures, and residual contamination. Reopen every implicated source batch before importing a candidate. Add `scope_keys` to every entity/reference/timeline/location/continuity candidate so independent verification receives the relevant factual notes, then run `complete-consistency` even if the pass finds none.
5. Deduplicate candidates by immutable source span and proposed replacement. Resolve conflicting proposals as `review`; do not choose one automatically.
6. Use `show-review` to build verification input, then send it to fresh verification subagents. This command omits first-pass confidence and rationale and includes scoped context notes for global candidates. Import every verification result.
7. Record explicit user accept/reject decisions for unresolved candidates when provided.
8. Run `status`. Do not call the final applier until every core batch and the global consistency pass are complete and every candidate has a verification result.
9. Generate the revised TXT, concise report, and audit appendix:

   ```bash
   python3 long-novel-proofreader/scripts/apply_reviewed_edits.py \
     --state-dir /path/to/.proofread \
     --output /path/to/revised.txt \
     --report /path/to/proofreading-report.md \
     --audit /path/to/proofreading-audit.jsonl
   ```

10. Verify that the source hash is unchanged, output generation is reproducible, chapter order and protected terms remain, confirmed site banners are gone, and in-story advertisement references remain.

## Batch review

Review the prose itself before consulting hints. Do not treat proofreading as a pattern-matching task. Read each core sentence and paragraph through seven lenses:

1. Character and word integrity: wrong, missing, extra, duplicated, or transposed characters/words; malformed fixed expressions.
2. Sentence structure: missing subjects, objects, complements, conjunction halves, function words, or short phrases; corrupted word order.
3. Boundary integrity: punctuation, whitespace, or line breaks inserted inside established words, names, apps, organizations, or protected terms.
4. Collocation and local semantics: individually valid words that form an impossible predicate-object, modifier-head, quantity, or action relationship in context.
5. Punctuation and text structure: unmatched quotation marks/brackets, broken dialogue boundaries, accidental paragraph or chapter-edge repetition/gaps, and damaged paired structures.
6. Entity and discourse consistency: names, aliases, pronouns, relationships, time, location, character state, and chapter-to-chapter continuity. Record concise context notes for the later global pass.
7. Text contamination: out-of-story ads, page headers, URLs, mojibake, OCR fragments, and publishing-site residue; preserve any of these when they belong to the story.

Record a candidate when the defect is definite even if no hint matched. A uniquely recoverable local textual defect may be `high`. Entity or continuity conflicts, broad grammar cleanup, optional punctuation, sentence smoothing, 的/地/得, literary ellipsis, dialogue hesitation, and every repair with multiple plausible readings must be `review`.

Examples:

- `是不是一些人在对一个嘲笑` → `是不是一些人在对一个人嘲笑`: `missing_word`, potentially `high` because the object of `对` is structurally absent and the local meaning supplies one unique character.
- `他打开微，信查看消息` → `他打开微信查看消息`: `word_boundary`, potentially `high` because the comma breaks the established app name and no pause reading fits the sentence.
- `他对一个……嘲笑起来` is not automatically repaired: literary ellipsis or omitted wording may depend on author intent.
- Dialogue such as `“微，信我一次。”` is not automatically changed to `微信`: a spoken pause can be intentional.

Use original-source line numbers and exact substrings. When the same substring occurs more than once on a line, provide a one-based `occurrence`. Never invent character offsets; the importer resolves and validates them against the immutable source.

## State and recovery

Keep state in `.proofread/`: `manifest.json`, `batches.jsonl`, `candidates.jsonl`, `reviews.jsonl`, `decisions.jsonl`, `glossary.json`, `context.jsonl`, and `consistency.json`. Validate the source hash before every operation. Resume from the first incomplete batch or pending global pass. Keep state until the user accepts the final result.

## Completion gate

Declare completion only when:

- every core source span has completed first review;
- the global entity and continuity consistency pass is complete;
- every candidate has an isolated verification result;
- every agreed `high` edit is applied or explicitly rejected by the user;
- unresolved items appear in the report;
- source hash, encoding, line endings, chapter order, and protected terms pass verification;
- regenerating from state produces identical output.

The deliverables are a distinct revised TXT, a concise Markdown report, a complete JSONL audit appendix, and a short handoff naming intentionally unchanged categories.
