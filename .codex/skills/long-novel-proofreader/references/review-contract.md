# Review record contract

Use UTF-8 JSONL with one object per line. Do not wrap records in Markdown fences.

## First-review candidate input

```json
{"batch_id":"batch-00001","line":42,"original":"噗之以鼻","occurrence":1,"proposed":"嗤之以鼻","category":"wrong_character","confidence":"high","reason":"固定成语应为嗤之以鼻","context":"他对这个结论噗之以鼻。"}
```

Required fields are `batch_id`, `line`, `original`, `proposed`, `category`, `confidence`, `reason`, and `context`. `confidence` is `high` or `review`. Use an empty `proposed` string only for a confirmed deletion. Add `occurrence` when needed; it defaults to 1.

Allowed core categories are:

- `wrong_character`, `homophone`, `missing_character`, and `duplicate_character` for character-level defects;
- `wrong_word`, `missing_word`, and `extra_text` for uniquely recoverable contextual word or short-phrase defects;
- `transposition` for a uniquely recoverable accidental character or word-order swap;
- `word_boundary` for punctuation or whitespace that corrupts an established lexical unit;
- `malformed_expression` for a fixed expression with one ordinary correction;
- `external_ad` for confirmed out-of-story banners.

Use `grammar`, `punctuation`, `structural_punctuation`, `de_di_de`, `style`, `semantic_mismatch`, `paired_structure`, `entity_consistency`, `reference`, `timeline`, `location`, `duplicate_text`, `text_noise`, `continuity`, or `ambiguous` only with `confidence: review`. Classify a definite missing object as `missing_word`, not `grammar`; classify punctuation inside `微，信` as `word_boundary`, not `punctuation`, only when context uniquely requires `微信`.

Candidates in `entity_consistency`, `reference`, `timeline`, `location`, or `continuity` must add non-empty `scope_keys`, a JSON string array of the involved names, aliases, places, dates, or other lookup keys. The importer rejects these categories without scope keys. This lets `show-review` include relevant factual context notes without exposing first-pass reasoning or anomaly notes.

The importer adds `candidate_id`, `start_char`, `end_char`, and source hash after resolving the exact original substring.

## Context note input

Store compact factual leads for the later global consistency pass:

```json
{"batch_id":"batch-00003","line":188,"kind":"entity","subject":"赵宁","value":"同事称其为赵队；男性代词证据","context":"赵队把外套递给他。"}
```

Required fields are `batch_id`, `line`, `kind`, `subject`, `value`, and `context`. `line` anchors the primary evidence; `context` may be a concise excerpt or a compact multi-line summary and must never substitute for reopening the source. Allowed kinds are `entity`, `alias`, `reference`, `relationship`, `time`, `timeline`, `location`, `state`, `continuity`, `chapter_summary`, and `anomaly`. Keep ordinary notes factual. Use `anomaly` only as a global-pass lead; `show-review` excludes anomaly notes so they cannot leak first-pass suspicion to the verifier. Notes are evidence pointers, not candidates and not permission to edit.

## Verification input and output

Give the verifier only the normalized candidate identity, original/proposed text, category, source context, and protected glossary. Hide first-review confidence and reason.

Return:

```json
{"candidate_id":"cand-...","verdict":"high","proposed":"嗤之以鼻","reason":"上下文明确使用该固定成语","review_mode":"independent_subagent","reviewer":"agent-name"}
```

`verdict` is `high`, `review`, or `reject`. `review_mode` is `independent_subagent` or `same_model_isolated`.

## User decision

```json
{"candidate_id":"cand-...","action":"accept","proposed":"嗤之以鼻","reason":"用户确认"}
```

`action` is `accept` or `reject`. Decisions are append-only; the last valid decision for a candidate wins.

## Automatic application

Apply automatically only when the first record has `confidence: high`, the latest verification has `verdict: high`, both proposed strings are identical, and there is no user rejection. A user acceptance may resolve a `review` item. Reject overlapping edits and conflicting replacements instead of guessing.

## State commands

Import a first-review JSONL file, then explicitly complete its batch even when it contains no candidates:

```bash
python3 scripts/proofread_txt.py import-candidates --state-dir .proofread --file first-review.jsonl
python3 scripts/proofread_txt.py complete-batch --state-dir .proofread --batch-id batch-00001 --reviewer agent-name --glossary-version 1
```

Generate leak-resistant verification input and import the verifier's JSONL response:

```bash
python3 scripts/proofread_txt.py show-review --state-dir .proofread --candidate-id cand-...
python3 scripts/proofread_txt.py import-reviews --state-dir .proofread --file verification.jsonl
```

Import user decisions with `import-decisions`. Merge glossary proposals from a UTF-8 JSON array with `merge-glossary`; each proposal requires `term`, `type`, and `batch_id`. Run `status` after each wave and `verify` before final application.

Import context notes during each wave, then inspect them after all batches complete:

```bash
python3 scripts/proofread_txt.py import-context --state-dir .proofread --file context-notes.jsonl
python3 scripts/proofread_txt.py show-context --state-dir .proofread --kind entity
python3 scripts/proofread_txt.py show-context --state-dir .proofread --subject 赵宁
```

Reopen implicated batches, import any global candidates through `import-candidates`, then close the mandatory pass even when it finds no candidates:

```bash
python3 scripts/proofread_txt.py complete-consistency --state-dir .proofread --reviewer agent-name
```
