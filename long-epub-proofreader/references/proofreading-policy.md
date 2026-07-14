# Conservative EPUB proofreading policy

## Text candidates

Actively look for wrong characters, unambiguous homophone substitutions, explicit missing or duplicated characters, malformed fixed expressions with one ordinary reading, and standalone site/download banners outside the book's content.

Use `high` only when the source is invalid in context and one replacement is unambiguous. Use the smallest source span that expresses the correction. Inline markup does not break linguistic context.

Always use `review` for punctuation, grammar, 的/地/得, repetition, style, awkwardness, names, invented terms, dialect, slang, plot-dependent wording, and any advertisement that might occur inside the book's narrative.

## Image candidates

Review every local image referenced from a spine body only for site advertising.

- `external_ad_image`: the referenced image is purely an out-of-book site, download, social-account, or redistribution banner. Propose `remove_reference`.
- `mixed_ad_image`: legitimate book content and an added advertisement or watermark share one image. Propose `keep` and require a user decision.

Do not proofread ordinary image text. Do not infer a typo from OCR alone. Never automatically edit image pixels or remove a mixed-content image.

## Protected content

Preserve author afterwords, in-story advertisements, illustrations, charts, cover text, attributes, anchors, links, styles, metadata, and navigation. A glossary term becomes protected only after evidence from two distinct batches or explicit user approval. Never normalize repeated strings globally.

## Write-back

Patch decoded visible text through its recorded raw XHTML positions. Preserve all tags and allow emptied text-bearing elements. For an insertion exactly between two text nodes, insert at the end of the left node. Escape replacement text for XML and retain the document's original encoding.

An approved pure-ad image removes only the exact referencing element. Keep the image resource in the ZIP. A user-supplied replacement may replace the resource only when it has one spine usage and the media type is unchanged.

## Reporting

Retain source location, original text or image, proposal, context, first confidence, verification, user decision, final classification, and exact modified entry. Report every skipped spine document with its reason and unreviewed-character estimate. Any skip makes the completion scope `partial`.
