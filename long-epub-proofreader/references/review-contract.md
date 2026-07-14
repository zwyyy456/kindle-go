# EPUB review record contract

Use UTF-8 JSONL with one object per line. Do not wrap records in Markdown fences.

## Text first-review candidate

```json
{"batch_id":"batch-00001","block_id":"block-0000042","original":"噗之以鼻","occurrence":1,"proposed":"嗤之以鼻","category":"wrong_character","confidence":"high","reason":"固定成语应为嗤之以鼻","context":"他对这个结论噗之以鼻。"}
```

Required fields are `batch_id`, `block_id`, `original`, `proposed`, `category`, `confidence`, `reason`, and `context`. `occurrence` defaults to 1.

Core categories are `wrong_character`, `homophone`, `missing_character`, `duplicate_character`, and `external_ad`. Use `grammar`, `punctuation`, `de_di_de`, `style`, or `ambiguous` only with `confidence: review`.

## Image first-review candidate

Pure advertisement:

```json
{"image_usage_id":"imguse-...","category":"external_ad_image","confidence":"high","proposed":"remove_reference","reason":"整张图片是站外微信推广"}
```

Mixed book content and advertisement:

```json
{"image_usage_id":"imguse-...","category":"mixed_ad_image","confidence":"review","proposed":"keep","reason":"原书日志页叠加了推广水印，不能自动删除整图"}
```

Record a candidate for each usage location. Complete the unique image after all usage contexts have been reviewed, including when no candidate exists.

## Verification

`show-review` hides first-review confidence and rationale. Return:

```json
{"candidate_id":"cand-...","verdict":"high","proposed":"嗤之以鼻","reason":"上下文明确使用固定成语","review_mode":"independent_subagent","reviewer":"agent-name"}
```

`verdict` is `high`, `review`, or `reject`. `review_mode` is `independent_subagent` or `same_model_isolated`.

## User decisions

Text or image-reference decision:

```json
{"candidate_id":"cand-...","action":"accept","proposed":"remove_reference","reason":"用户确认删除纯广告引用"}
```

Keep a mixed image unchanged with `action: accept, proposed: keep`, or reject the candidate. To replace a mixed image resource that has exactly one spine usage:

```json
{"candidate_id":"cand-...","action":"accept","replacement_file":"/path/to/cleaned.jpg","reason":"用户提供清理后的同格式图片"}
```

Decisions are append-only; the latest valid decision wins.

## Automatic application

Apply automatically only when first review and latest verification are both `high` with identical proposals and no user rejection exists. Never automatically apply `mixed_ad_image`. Reject overlapping raw ranges rather than choosing one. Do not propagate an accepted edit to repeated text or another image usage.
