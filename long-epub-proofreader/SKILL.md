---
name: long-epub-proofreader
description: Conservatively proofread an entire long Chinese EPUB in resumable model-reviewed batches, including contextual typos, missing or duplicated characters, confirmed site advertisements in text, and advertisements embedded as referenced images. Use when Codex must preserve the immutable source, original ZIP entries and XHTML bytes outside approved patches, independently verify every candidate, and produce a deterministic revised EPUB with reports and complete audit records.
---

# Long EPUB Proofreader

Proofread the reading-order XHTML as continuous visible prose while patching only exact original byte ranges. Never convert the EPUB to TXT and rebuild it from a generic book model.

Read [references/proofreading-policy.md](references/proofreading-policy.md) before reviewing content. Read [references/review-contract.md](references/review-contract.md) before recording candidates, reviews, or decisions.

## Non-negotiable rules

- Keep the source EPUB immutable and validate its SHA-256 before every command.
- Review every non-overlapping text batch, including batches with no hints.
- Concatenate visible text across inline elements such as `span`, `a`, and `em`; XHTML nodes are not review boundaries.
- Review every local image actually referenced from a spine `<body>`, but inspect images only for out-of-story site advertising.
- Treat string matches, OCR, and known banners only as hints.
- Apply automatically only when first review and an isolated verification both return `high` with exactly the same proposal.
- Never globally replace repeated text or propagate one edit to another source location.
- Never reserialize XHTML. Patch exact text ranges; an approved pure-ad image may delete only its exact `img` or SVG `image` element.
- Preserve attributes and all unapproved markup. Empty `p`, `span`, or other parent elements are allowed.
- Record every candidate, rejection, unresolved item, skipped document, and skipped image.

## Roles

### Orchestrator

Own the immutable source, state, batch scheduling, glossary versions, image coverage, conflicts, final application, deterministic regeneration, and reports. Do not perform verification in the first-review context when a fresh subagent is available.

### Text first reviewer

Read every core block as continuous visible prose. Use overlap only for context. Return structured candidates only for the core range and propose glossary additions separately. Do not edit files.

### Image first reviewer

Inspect the extracted image and every usage context. Report only `external_ad_image` or `mixed_ad_image`; do not proofread ordinary text in covers, charts, diagrams, or illustrations. Mark the image complete even when it has no candidate.

### Verification reviewer

Use a fresh context. Receive the normalized candidate, source context, protected glossary, and image when applicable, but not the first review's confidence or rationale. Do not edit files.

If subagents are unavailable, use an isolated prompt and record `review_mode: same_model_isolated`; never describe it as independent-subagent verification.

## Workflow

1. Initialize resumable state:

   ```bash
   python3 long-epub-proofreader/scripts/proofread_epub.py init \
     --input /path/to/source.epub \
     --state-dir /path/to/.proofread-epub
   ```

2. Inspect `manifest.json`, `documents.jsonl`, status, skipped ranges, batch boundaries, and image inventory.
3. Process `show-batch` results in waves. Import JSONL candidates and explicitly complete every batch, including zero-candidate batches.
4. Process every pending image with `show-image`. Use the returned absolute image path for visual inspection. Import per-usage candidates, then call `complete-image`. Use `skip-image` only when the asset cannot be inspected and record the reason.
5. Merge glossary proposals only after each text-review wave.
6. Use `show-review` for leak-resistant verification inputs and import every verification result.
7. Import explicit user decisions for unresolved candidates. A mixed image remains unchanged unless the user accepts `remove_reference` or supplies `replacement_file`; replacement is allowed only for a resource with one spine usage.
8. Run `status` and `verify`. Do not apply until every text batch and inspectable image is complete and every candidate has a verification record.
9. Generate deliverables:

   ```bash
   python3 long-epub-proofreader/scripts/apply_reviewed_edits.py \
     --state-dir /path/to/.proofread-epub \
     --output /path/to/revised.epub \
     --report /path/to/proofreading-report.md \
     --audit /path/to/proofreading-audit.jsonl
   ```

10. Keep the state directory until the user accepts the revised EPUB.
11. If `epubcheck` is already available, run it as an additional non-mandatory validation and record its result. Do not install it or treat source-existing findings as hidden success criteria.

## Scope

- Review text nodes under spine XHTML `<body>` elements. Skip `script`, `style`, and all attribute values.
- Treat a spine directory page as ordinary readable content. Validate but do not proofread OPF metadata, NCX, or navigation files outside the spine.
- Reject DRM/encrypted EPUBs. Skip unsupported or unmappable spine documents and undecodable images, then label the final result `partial` with all skipped ranges.
- Scan only local images reached through `img` or inline SVG `image`. Do not scan orphan resources, CSS backgrounds, or remote images.
- Keep mixed content-plus-ad images unchanged and unresolved by default. Never automatically edit image pixels.

## Completion gate

Declare `full` completion only when every spine text block and referenced inspectable image completed first review, every candidate has isolated verification, no document or image was skipped, and every approved edit was applied or explicitly rejected.

Require all of the following for both `full` and `partial` output:

- source and per-entry hashes remain valid;
- only approved XHTML ranges or explicitly supplied single-use replacement images change;
- untouched entry content, order, compression method, timestamps, permissions, OPF, spine, and navigation remain unchanged;
- the output opens as an EPUB and every parsed spine XHTML remains parseable;
- regenerating from the same source and state produces a byte-identical EPUB SHA-256;
- unresolved and intentionally unchanged categories appear in the report and audit.

The deliverables are a distinct revised EPUB, concise Markdown report, complete JSONL audit appendix, resumable state, and a short handoff naming skipped or intentionally unchanged content.
