# Conservative contextual proofreading policy

## Local batch pass

The first-review model must read all prose at character, sentence, and paragraph level and actively look for:

- wrong characters and unambiguous homophone substitutions;
- explicit missing or duplicated characters, words, or short phrases;
- accidental extra or transposed characters, words, or short spans;
- structurally incomplete sentences, such as a missing subject, object, complement, conjunction half, or required function word;
- contextually impossible words when one minimal replacement is uniquely supported;
- punctuation or whitespace accidentally inserted inside an established word, name, app, organization, or protected term;
- accidental extra text, transposition, or punctuation corruption when the intended local reading is unique;
- malformed fixed expressions when context permits only one ordinary reading;
- impossible local collocations or predicate-argument relations, even when every individual word is valid;
- unmatched paired punctuation, broken dialogue boundaries, paragraph or chapter-edge duplication/gaps, and damaged paired structures;
- page headers, mojibake, OCR fragments, or publishing residue that clearly does not belong to the story;
- standalone website, URL, download, or publishing-site banners outside the story.

A script match is only a hint. Confirm every candidate from surrounding prose. Re-read every sentence for predicate-argument completeness and every suspicious punctuation mark for whether it separates clauses or corrupts a lexical unit. A sentence with no typo hint still requires full review.

For each batch, also emit only high-signal context notes needed for the global pass: canonical names and aliases, explicit pronoun or identity evidence, relationships, time, location, material state changes, compact chapter summaries, and suspected anomalies. Do not turn ordinary plot detail into a huge summary.

## Global consistency pass

After all local batches are complete, compare context notes and the protected glossary across the manuscript. Check:

- entity spelling, aliases, titles, pronouns, and relationships;
- time, location, possession, injuries, knowledge, and other material state transitions;
- chapter joins, repeated or apparently missing passages, and unresolved paired structures;
- contamination that spans batch boundaries.

Context notes are leads, not evidence. Reopen the original source around every implicated occurrence before recording a candidate. Cross-batch inconsistencies remain `review` unless the source itself contains one uniquely recoverable local textual defect.

## High confidence

Use `high` only when the original is invalid in context and the proposed replacement has one clear reading. Preserve the smallest source span that makes the repair auditable. Both review passes must independently choose the exact same replacement before automatic application.

`Punctuation` is not automatically review-only. Distinguish:

- lexical corruption: `他打开微，信查看消息` → `他打开微信查看消息` may be `high` as `word_boundary`;
- authorial punctuation: clause rhythm, optional commas, dialogue pauses, emphasis, ellipses, and sentence smoothing remain `review`.

Likewise, `grammar` is not a reason to ignore a definite omission. `是不是一些人在对一个嘲笑` → `是不是一些人在对一个人嘲笑` may be `high` as `missing_word`; if several different insertions or rewrites would work, keep it as `review`.

## Review only

Always use `review`, never automatic application, for:

- 的/地/得, optional punctuation, sentence smoothness, broad grammar rewrites, repetition, or copyediting;
- syntax or semantics with more than one plausible minimal repair;
- entity/name, pronoun, relationship, timeline, location, possession, or plot-state inconsistencies;
- repeated or apparently missing sentences/paragraphs and broken chapter transitions;
- unmatched structural punctuation or paired structures when authorial intent is not certain;
- suspected OCR or text contamination whose intended replacement is unclear;
- punctuation inside dialogue when a pause, interruption, stammer, or emphasis is plausible;
- names, places, organizations, invented terms, dialect, slang, and unusual narration;
- `渡过/度过` or similar usage choices unless the sentence is indisputable;
- possible plot inconsistencies or wording dependent on author intent;
- advertisements, posters, messages, URLs, or downloads that may occur inside a scene;
- conflicting first-review proposals for the same source span.

## Protected content

Preserve author afterwords unless the user explicitly asks for a pure-body edition. Maintain a dynamic glossary for names and invented terms. A proposed term becomes automatically protected only after evidence from at least two distinct batches, or after explicit user approval. Never globally normalize a suspected variant without user confirmation.

## Reporting

For every candidate retain its original line, exact original text, proposed replacement, category, context, confidence, and reason. Keep rejected candidates in the audit appendix. Put unresolved candidates in the concise report.
