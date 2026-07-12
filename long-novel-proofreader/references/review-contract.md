# Review record contract

Use UTF-8 JSONL with one object per line. Do not wrap records in Markdown fences.

## First-review candidate input

```json
{"batch_id":"batch-00001","line":42,"original":"噗之以鼻","occurrence":1,"proposed":"嗤之以鼻","category":"wrong_character","confidence":"high","reason":"固定成语应为嗤之以鼻","context":"他对这个结论噗之以鼻。"}
```

Required fields are `batch_id`, `line`, `original`, `proposed`, `category`, `confidence`, `reason`, and `context`. `confidence` is `high` or `review`. Use an empty `proposed` string only for a confirmed deletion. Add `occurrence` when needed; it defaults to 1.

Allowed core categories are `wrong_character`, `homophone`, `missing_character`, `duplicate_character`, and `external_ad`. Use `grammar`, `punctuation`, `de_di_de`, `style`, or `ambiguous` only with `confidence: review`.

The importer adds `candidate_id`, `start_char`, `end_char`, and source hash after resolving the exact original substring.

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
