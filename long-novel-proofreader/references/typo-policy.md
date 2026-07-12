# Conservative proofreading policy

## Core candidates

The first-review model must read all prose and actively look for:

- wrong characters and unambiguous homophone substitutions;
- explicit missing or duplicated characters;
- malformed fixed expressions when context permits only one ordinary reading;
- standalone website, URL, download, or publishing-site banners outside the story.

A script match is only a hint. Confirm every candidate from surrounding prose.

## High confidence

Use `high` only when the original is invalid in context and the proposed replacement has one clear reading. Preserve the smallest possible source span. Both review passes must independently choose the exact same replacement before automatic application.

## Review only

Always use `review`, never automatic application, for:

- 的/地/得, punctuation, sentence smoothness, grammar, repetition, or copyediting;
- names, places, organizations, invented terms, dialect, slang, and unusual narration;
- `渡过/度过` or similar usage choices unless the sentence is indisputable;
- possible plot inconsistencies or wording dependent on author intent;
- advertisements, posters, messages, URLs, or downloads that may occur inside a scene;
- conflicting first-review proposals for the same source span.

## Protected content

Preserve author afterwords unless the user explicitly asks for a pure-body edition. Maintain a dynamic glossary for names and invented terms. A proposed term becomes automatically protected only after evidence from at least two distinct batches, or after explicit user approval. Never globally normalize a suspected variant without user confirmation.

## Reporting

For every candidate retain its original line, exact original text, proposed replacement, category, context, confidence, and reason. Keep rejected candidates in the audit appendix. Put unresolved candidates in the concise report.
