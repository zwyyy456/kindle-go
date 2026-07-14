#!/usr/bin/env python3
"""Prepare and maintain immutable, resumable EPUB proofreading state."""

from __future__ import annotations

import argparse
import hashlib
import json
import shutil
import sys
import zipfile
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Dict, Iterable, List

from epub_core import (
    IMAGE_CATEGORIES,
    KNOWN_HINTS,
    REVIEW_ONLY_CATEGORIES,
    STATE_VERSION,
    TEXT_CORE_CATEGORIES,
    ProofreadError,
    append_jsonl,
    assign_block_ids,
    block_map,
    build_batches,
    estimate_unreviewed_characters,
    image_maps,
    latest_by,
    load_runtime,
    media_type_for,
    occurrence_positions,
    parse_epub_package,
    read_json,
    read_jsonl,
    scan_xhtml,
    sha256_bytes,
    source_manifest,
    write_json,
    write_jsonl,
)


def now_iso() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat()


def command_init(args: argparse.Namespace) -> None:
    source = args.input.resolve()
    if not source.is_file():
        raise ProofreadError(f"input is not a file: {source}")
    state_dir = args.state_dir.resolve()
    if state_dir.exists() and any(state_dir.iterdir()):
        raise ProofreadError(f"state directory is not empty: {state_dir}")

    source_data = source.read_bytes()
    source_digest = sha256_bytes(source_data)
    documents: List[Dict[str, Any]] = []
    parsed_documents = []
    entry_records: List[Dict[str, Any]] = []
    raw_image_usages = []

    try:
        with zipfile.ZipFile(source) as zf:
            infos = zf.infolist()
            if not infos or infos[0].filename != "mimetype":
                raise ProofreadError("EPUB mimetype must be the first ZIP entry")
            if infos[0].compress_type != zipfile.ZIP_STORED:
                raise ProofreadError("EPUB mimetype entry must be uncompressed")
            if zf.read("mimetype") != b"application/epub+zip":
                raise ProofreadError("invalid EPUB mimetype content")
            opf_path, spine, media_by_path, fixed_layout = parse_epub_package(zf)
            names = set(zf.namelist())
            for info in infos:
                data = zf.read(info.filename)
                entry_records.append({
                    "name": info.filename,
                    "sha256": sha256_bytes(data),
                    "size": len(data),
                    "compress_type": info.compress_type,
                    "date_time": list(info.date_time),
                    "external_attr": info.external_attr,
                    "internal_attr": info.internal_attr,
                    "create_system": info.create_system,
                })

            for spine_index, item in enumerate(spine):
                href = item.get("href", "")
                media_type = item.get("media_type", "")
                record: Dict[str, Any] = {
                    "spine_index": spine_index,
                    "href": href,
                    "media_type": media_type,
                }
                reason = ""
                data = b""
                if fixed_layout:
                    reason = "fixed-layout EPUB is outside the first-version scope"
                elif not href:
                    reason = "spine item has no resolvable local href"
                elif href not in names:
                    reason = "spine entry is missing from ZIP"
                elif media_type not in {"application/xhtml+xml", "text/html"}:
                    reason = f"unsupported spine media type: {media_type or 'unknown'}"
                else:
                    data = zf.read(href)
                    try:
                        parsed = scan_xhtml(data, href)
                    except ProofreadError as exc:
                        reason = str(exc)
                    else:
                        record.update({
                            "status": "parsed",
                            "sha256": sha256_bytes(data),
                            "codec": parsed.codec,
                            "bom_hex": parsed.bom.hex(),
                            "block_count": len(parsed.blocks),
                            "character_count": sum(len(block.text) for block in parsed.blocks),
                        })
                        documents.append(record)
                        parsed_documents.append(parsed)
                        raw_image_usages.extend(parsed.image_usages)
                        continue
                if not data and href in names:
                    data = zf.read(href)
                record.update({
                    "status": "skipped",
                    "reason": reason,
                    "sha256": sha256_bytes(data) if data else "",
                    "unreviewed_character_estimate": estimate_unreviewed_characters(data) if data else 0,
                })
                documents.append(record)

            blocks = assign_block_ids(parsed_documents)
            batches = build_batches(blocks, args.batch_chars, args.overlap_chars)
            grouped: Dict[str, Dict[str, Any]] = {}
            for usage in raw_image_usages:
                identity = f"{source_digest}:{usage.document_href}:{usage.raw_start}:{usage.raw_end}".encode("utf-8")
                usage.usage_id = "imguse-" + hashlib.sha256(identity).hexdigest()[:20]
                image_identity = f"{source_digest}:{usage.resource_href}".encode("utf-8")
                image_id = "image-" + hashlib.sha256(image_identity).hexdigest()[:20]
                image = grouped.setdefault(usage.resource_href, {
                    "image_id": image_id,
                    "resource_href": usage.resource_href,
                    "media_type": media_type_for(usage.resource_href, media_by_path.get(usage.resource_href, "")),
                    "usages": [],
                })
                image["usages"].append({
                    "image_usage_id": usage.usage_id,
                    "document_href": usage.document_href,
                    "tag": usage.tag,
                    "raw_start": usage.raw_start,
                    "raw_end": usage.raw_end,
                    "context": usage.context,
                })

            images: List[Dict[str, Any]] = []
            for resource_href, image in sorted(grouped.items(), key=lambda item: item[0]):
                if resource_href not in names:
                    image.update({"status": "skipped", "reason": "referenced image resource is missing", "sha256": ""})
                else:
                    data = zf.read(resource_href)
                    suffix = Path(resource_href).suffix.lower()
                    if not suffix or len(suffix) > 10:
                        suffix = ".bin"
                    extracted = Path("images") / f"{image['image_id']}{suffix}"
                    image.update({
                        "status": "pending",
                        "sha256": sha256_bytes(data),
                        "extracted_path": str(extracted),
                        "_extracted_data": data,
                    })
                images.append(image)
    except zipfile.BadZipFile as exc:
        raise ProofreadError(f"invalid EPUB ZIP: {exc}") from exc

    skipped_documents = [item for item in documents if item["status"] == "skipped"]
    manifest = {
        "version": STATE_VERSION,
        "created_at": now_iso(),
        "source_path": str(source),
        "source_sha256": source_digest,
        "size_bytes": len(source_data),
        "opf_path": opf_path,
        "spine_count": len(documents),
        "parsed_document_count": len(documents) - len(skipped_documents),
        "skipped_document_count": len(skipped_documents),
        "unreviewed_character_estimate": sum(item.get("unreviewed_character_estimate", 0) for item in skipped_documents),
        "block_count": len(blocks),
        "character_count": sum(len(block.text) for block in blocks),
        "batch_chars": args.batch_chars,
        "overlap_chars": args.overlap_chars,
        "batch_count": len(batches),
        "image_count": len(images),
    }
    state_dir.mkdir(parents=True, exist_ok=True)
    (state_dir / "images").mkdir()
    (state_dir / "replacements").mkdir()
    for image in images:
        extracted_data = image.pop("_extracted_data", None)
        if extracted_data is not None:
            (state_dir / image["extracted_path"]).write_bytes(extracted_data)
    write_json(state_dir / "manifest.json", manifest)
    write_jsonl(state_dir / "entries.jsonl", entry_records)
    write_jsonl(state_dir / "documents.jsonl", documents)
    write_jsonl(state_dir / "batches.jsonl", batches)
    write_jsonl(state_dir / "images.jsonl", images)
    for filename in ("candidates.jsonl", "reviews.jsonl", "decisions.jsonl"):
        (state_dir / filename).write_text("", encoding="utf-8")
    write_json(state_dir / "glossary.json", {"version": 1, "terms": []})
    print(json.dumps(manifest, ensure_ascii=False, indent=2))


def find_batch(state_dir: Path, batch_id: str) -> Dict[str, Any]:
    for batch in read_jsonl(state_dir / "batches.jsonl"):
        if batch["batch_id"] == batch_id:
            return batch
    raise ProofreadError(f"unknown batch: {batch_id}")


def command_show_batch(args: argparse.Namespace) -> None:
    manifest, _, blocks = load_runtime(args.state_dir)
    batch = find_batch(args.state_dir, args.batch_id)
    rendered = []
    for index in range(batch["context_start_index"], batch["context_end_index"]):
        block = blocks[index]
        scope = "core" if batch["core_start_index"] <= index < batch["core_end_index"] else "overlap"
        rendered.append(f"{block.block_id}\t[{scope}]\t{block.document_href}\t{block.text}")
    excerpt = "\n".join(blocks[index].text for index in range(batch["context_start_index"], batch["context_end_index"]))
    hints = [{"pattern": pattern, "count": excerpt.count(pattern), "decision": "model_required"} for pattern in KNOWN_HINTS if pattern in excerpt]
    payload = {
        "source_sha256": manifest["source_sha256"],
        "batch": batch,
        "glossary": read_json(args.state_dir / "glossary.json"),
        "hints": hints,
        "instruction": "Review every core block as continuous visible text. Inline XHTML nodes are not review boundaries; overlap is context only.",
        "text": "\n".join(rendered),
    }
    print(json.dumps(payload, ensure_ascii=False, indent=2))


def normalize_text_candidate(record: Dict[str, Any], manifest: Dict[str, Any], blocks: Dict[str, Any], batches: Dict[str, Any]) -> Dict[str, Any]:
    required = ("batch_id", "block_id", "original", "proposed", "category", "confidence", "reason", "context")
    missing = [key for key in required if key not in record]
    if missing:
        raise ProofreadError(f"text candidate missing fields: {', '.join(missing)}")
    batch = batches.get(str(record["batch_id"]))
    block = blocks.get(str(record["block_id"]))
    if not batch or not block:
        raise ProofreadError("text candidate has an unknown batch or block")
    if not batch["core_start_index"] <= block.global_index < batch["core_end_index"]:
        raise ProofreadError(f"candidate block {block.block_id} is outside {batch['batch_id']} core range")
    category = str(record["category"])
    confidence = str(record["confidence"])
    if category not in TEXT_CORE_CATEGORIES | REVIEW_ONLY_CATEGORIES:
        raise ProofreadError(f"unsupported text candidate category: {category}")
    if confidence not in {"high", "review"}:
        raise ProofreadError(f"unsupported candidate confidence: {confidence}")
    if category in REVIEW_ONLY_CATEGORIES and confidence != "review":
        raise ProofreadError(f"{category} candidates must use confidence=review")
    original = str(record["original"])
    proposed = str(record["proposed"])
    if not original or "\n" in original or "\r" in original or "\n" in proposed or "\r" in proposed:
        raise ProofreadError("text candidates must be non-empty and may not contain line breaks")
    if original == proposed:
        raise ProofreadError("text candidate proposal must differ from the original")
    positions = occurrence_positions(block.text, original)
    occurrence = int(record.get("occurrence", 1))
    if occurrence < 1 or occurrence > len(positions):
        raise ProofreadError(f"cannot resolve occurrence {occurrence} of {original!r} in {block.block_id}")
    start = positions[occurrence - 1]
    end = start + len(original)
    identity = f"{manifest['source_sha256']}:{block.document_href}:{block.block_id}:{start}:{end}:{proposed}".encode("utf-8")
    return {
        "candidate_id": "cand-" + hashlib.sha256(identity).hexdigest()[:20],
        "kind": "text",
        "source_sha256": manifest["source_sha256"],
        "batch_id": batch["batch_id"],
        "block_id": block.block_id,
        "document_href": block.document_href,
        "start_offset": start,
        "end_offset": end,
        "original": original,
        "occurrence": occurrence,
        "proposed": proposed,
        "category": category,
        "confidence": confidence,
        "reason": str(record["reason"]),
        "context": str(record["context"]),
        "recorded_at": now_iso(),
    }


def normalize_image_candidate(record: Dict[str, Any], manifest: Dict[str, Any], usages: Dict[str, Any]) -> Dict[str, Any]:
    required = ("image_usage_id", "category", "confidence", "reason")
    missing = [key for key in required if key not in record]
    if missing:
        raise ProofreadError(f"image candidate missing fields: {', '.join(missing)}")
    usage_id = str(record["image_usage_id"])
    pair = usages.get(usage_id)
    if not pair:
        raise ProofreadError(f"unknown image usage: {usage_id}")
    image, usage = pair
    category = str(record["category"])
    confidence = str(record["confidence"])
    if category not in IMAGE_CATEGORIES or confidence not in {"high", "review"}:
        raise ProofreadError("unsupported image category or confidence")
    if category == "mixed_ad_image" and confidence != "review":
        raise ProofreadError("mixed_ad_image candidates must use confidence=review")
    proposed = str(record.get("proposed", "remove_reference" if category == "external_ad_image" else "keep"))
    if proposed not in {"remove_reference", "keep"}:
        raise ProofreadError(f"unsupported image proposal: {proposed}")
    identity = f"{manifest['source_sha256']}:{usage_id}:{proposed}".encode("utf-8")
    return {
        "candidate_id": "cand-" + hashlib.sha256(identity).hexdigest()[:20],
        "kind": "image",
        "source_sha256": manifest["source_sha256"],
        "image_id": image["image_id"],
        "image_usage_id": usage_id,
        "document_href": usage["document_href"],
        "resource_href": image["resource_href"],
        "raw_start": usage["raw_start"],
        "raw_end": usage["raw_end"],
        "original": image["resource_href"],
        "proposed": proposed,
        "category": category,
        "confidence": confidence,
        "reason": str(record["reason"]),
        "context": usage.get("context", ""),
        "recorded_at": now_iso(),
    }


def command_import_candidates(args: argparse.Namespace) -> None:
    manifest, _, blocks = load_runtime(args.state_dir)
    block_lookup = block_map(blocks)
    batches = {item["batch_id"]: item for item in read_jsonl(args.state_dir / "batches.jsonl")}
    _, usages = image_maps(args.state_dir)
    existing = read_jsonl(args.state_dir / "candidates.jsonl")
    known = {record["candidate_id"] for record in existing}
    normalized = []
    for record in read_jsonl(args.file):
        candidate = (
            normalize_image_candidate(record, manifest, usages)
            if "image_usage_id" in record
            else normalize_text_candidate(record, manifest, block_lookup, batches)
        )
        if candidate["candidate_id"] not in known:
            known.add(candidate["candidate_id"])
            normalized.append(candidate)
    append_jsonl(args.state_dir / "candidates.jsonl", normalized)
    print(f"imported_candidates={len(normalized)}")


def command_complete_batch(args: argparse.Namespace) -> None:
    source_manifest(args.state_dir)
    batches = read_jsonl(args.state_dir / "batches.jsonl")
    matched = False
    for batch in batches:
        if batch["batch_id"] == args.batch_id:
            batch.update({
                "status": "complete",
                "completed_at": now_iso(),
                "reviewer": args.reviewer,
                "glossary_version": args.glossary_version,
            })
            matched = True
    if not matched:
        raise ProofreadError(f"unknown batch: {args.batch_id}")
    write_jsonl(args.state_dir / "batches.jsonl", batches)
    print(f"completed_batch={args.batch_id}")


def command_show_image(args: argparse.Namespace) -> None:
    manifest, _, _ = source_manifest(args.state_dir)
    images, _ = image_maps(args.state_dir)
    image = images.get(args.image_id)
    if not image:
        raise ProofreadError(f"unknown image: {args.image_id}")
    payload = dict(image)
    if image.get("extracted_path"):
        payload["extracted_path"] = str((args.state_dir / image["extracted_path"]).resolve())
    payload.update({
        "source_sha256": manifest["source_sha256"],
        "instruction": "Inspect the image only for out-of-story site advertising. Ordinary cover, chart, diagram, and illustration text is not typo-proofread.",
    })
    print(json.dumps(payload, ensure_ascii=False, indent=2))


def update_image_status(args: argparse.Namespace, status: str) -> None:
    source_manifest(args.state_dir)
    images = read_jsonl(args.state_dir / "images.jsonl")
    matched = False
    for image in images:
        if image["image_id"] == args.image_id:
            image.update({"status": status, "reviewer": args.reviewer, "completed_at": now_iso()})
            if status == "skipped":
                image["reason"] = args.reason
            matched = True
    if not matched:
        raise ProofreadError(f"unknown image: {args.image_id}")
    write_jsonl(args.state_dir / "images.jsonl", images)
    print(f"{status}_image={args.image_id}")


def command_import_reviews(args: argparse.Namespace) -> None:
    source_manifest(args.state_dir)
    candidates = latest_by(read_jsonl(args.state_dir / "candidates.jsonl"), "candidate_id")
    normalized = []
    for record in read_jsonl(args.file):
        required = ("candidate_id", "verdict", "proposed", "reason", "review_mode", "reviewer")
        missing = [key for key in required if key not in record]
        if missing:
            raise ProofreadError(f"review missing fields: {', '.join(missing)}")
        candidate_id = str(record["candidate_id"])
        if candidate_id not in candidates:
            raise ProofreadError(f"review has unknown candidate: {candidate_id}")
        verdict = str(record["verdict"])
        review_mode = str(record["review_mode"])
        if verdict not in {"high", "review", "reject"}:
            raise ProofreadError(f"unsupported review verdict: {verdict}")
        if review_mode not in {"independent_subagent", "same_model_isolated"}:
            raise ProofreadError(f"unsupported review mode: {review_mode}")
        normalized.append({
            "candidate_id": candidate_id,
            "verdict": verdict,
            "proposed": str(record["proposed"]),
            "reason": str(record["reason"]),
            "review_mode": review_mode,
            "reviewer": str(record["reviewer"]),
            "recorded_at": now_iso(),
        })
    append_jsonl(args.state_dir / "reviews.jsonl", normalized)
    print(f"imported_reviews={len(normalized)}")


def command_show_review(args: argparse.Namespace) -> None:
    manifest, _, blocks = load_runtime(args.state_dir)
    candidates = latest_by(read_jsonl(args.state_dir / "candidates.jsonl"), "candidate_id")
    candidate = candidates.get(args.candidate_id)
    if not candidate:
        raise ProofreadError(f"unknown candidate: {args.candidate_id}")
    payload: Dict[str, Any] = {
        "source_sha256": manifest["source_sha256"],
        "candidate_id": candidate["candidate_id"],
        "kind": candidate["kind"],
        "original": candidate["original"],
        "proposed": candidate["proposed"],
        "category": candidate["category"],
        "protected_glossary": [
            item for item in read_json(args.state_dir / "glossary.json").get("terms", []) if item.get("protected")
        ],
        "instruction": "Judge independently. First-review confidence and rationale are intentionally hidden.",
    }
    if candidate["kind"] == "text":
        index = next((item.global_index for item in blocks if item.block_id == candidate["block_id"]), -1)
        if index < 0:
            raise ProofreadError("candidate block no longer exists")
        start = max(0, index - args.context_blocks)
        end = min(len(blocks), index + args.context_blocks + 1)
        payload["context"] = "\n".join(f"{block.block_id}\t{block.text}" for block in blocks[start:end])
    else:
        images, usages = image_maps(args.state_dir)
        image, usage = usages[candidate["image_usage_id"]]
        payload.update({
            "image_path": str((args.state_dir / image["extracted_path"]).resolve()) if image.get("extracted_path") else "",
            "usage_context": usage.get("context", ""),
            "document_href": usage["document_href"],
        })
    print(json.dumps(payload, ensure_ascii=False, indent=2))


def command_import_decisions(args: argparse.Namespace) -> None:
    source_manifest(args.state_dir)
    candidates = latest_by(read_jsonl(args.state_dir / "candidates.jsonl"), "candidate_id")
    images, _ = image_maps(args.state_dir)
    normalized = []
    for record in read_jsonl(args.file):
        candidate_id = str(record.get("candidate_id", ""))
        action = str(record.get("action", ""))
        candidate = candidates.get(candidate_id)
        if not candidate:
            raise ProofreadError(f"decision has unknown candidate: {candidate_id}")
        if action not in {"accept", "reject"}:
            raise ProofreadError(f"unsupported decision action: {action}")
        decision: Dict[str, Any] = {
            "candidate_id": candidate_id,
            "action": action,
            "proposed": str(record.get("proposed", candidate["proposed"])),
            "reason": str(record.get("reason", "user decision")),
            "recorded_at": now_iso(),
        }
        replacement_file = record.get("replacement_file")
        if replacement_file:
            if candidate["kind"] != "image" or action != "accept":
                raise ProofreadError("replacement_file is only valid for an accepted image candidate")
            image = images[candidate["image_id"]]
            if len(image.get("usages", [])) != 1:
                raise ProofreadError("a replacement image is allowed only when the resource has one spine usage")
            replacement = Path(str(replacement_file)).resolve()
            if not replacement.is_file():
                raise ProofreadError(f"replacement image is missing: {replacement}")
            target = args.state_dir / "replacements" / f"{candidate_id}{replacement.suffix.lower()}"
            shutil.copyfile(replacement, target)
            data = target.read_bytes()
            decision.update({
                "proposed": "replace_resource",
                "replacement_path": str(target.relative_to(args.state_dir)),
                "replacement_sha256": sha256_bytes(data),
            })
        normalized.append(decision)
    append_jsonl(args.state_dir / "decisions.jsonl", normalized)
    print(f"imported_decisions={len(normalized)}")


def command_merge_glossary(args: argparse.Namespace) -> None:
    source_manifest(args.state_dir)
    glossary_path = args.state_dir / "glossary.json"
    glossary = read_json(glossary_path)
    terms = {item["term"]: item for item in glossary.get("terms", [])}
    values = json.loads(args.file.read_text(encoding="utf-8"))
    if not isinstance(values, list):
        raise ProofreadError("glossary input must be a JSON array")
    for record in values:
        term = str(record.get("term", "")).strip()
        batch_id = str(record.get("batch_id", "")).strip()
        if not term or not batch_id:
            raise ProofreadError("glossary proposal requires term and batch_id")
        item = terms.setdefault(term, {
            "term": term,
            "type": str(record.get("type", "invented_term")),
            "evidence_batches": [],
            "user_protected": False,
        })
        if batch_id not in item["evidence_batches"]:
            item["evidence_batches"].append(batch_id)
        if record.get("user_protected") is True:
            item["user_protected"] = True
        item["protected"] = item["user_protected"] or len(item["evidence_batches"]) >= 2
    glossary["version"] = int(glossary.get("version", 0)) + 1
    glossary["terms"] = sorted(terms.values(), key=lambda item: item["term"])
    write_json(glossary_path, glossary)
    print(f"glossary_version={glossary['version']} terms={len(terms)}")


def status_payload(state_dir: Path) -> Dict[str, Any]:
    manifest, _, _ = source_manifest(state_dir)
    batches = read_jsonl(state_dir / "batches.jsonl")
    images = read_jsonl(state_dir / "images.jsonl")
    candidates = latest_by(read_jsonl(state_dir / "candidates.jsonl"), "candidate_id")
    reviews = latest_by(read_jsonl(state_dir / "reviews.jsonl"), "candidate_id")
    decisions = latest_by(read_jsonl(state_dir / "decisions.jsonl"), "candidate_id")
    incomplete_batches = [item["batch_id"] for item in batches if item.get("status") != "complete"]
    pending_images = [item["image_id"] for item in images if item.get("status") == "pending"]
    skipped_images = [item for item in images if item.get("status") == "skipped"]
    unreviewed = [candidate_id for candidate_id in candidates if candidate_id not in reviews]
    partial = manifest["skipped_document_count"] > 0 or bool(skipped_images)
    ready = not incomplete_batches and not pending_images and not unreviewed
    return {
        "source_sha256": manifest["source_sha256"],
        "batches_total": len(batches),
        "batches_complete": len(batches) - len(incomplete_batches),
        "next_incomplete_batch": incomplete_batches[0] if incomplete_batches else None,
        "images_total": len(images),
        "images_complete": sum(1 for item in images if item.get("status") == "complete"),
        "pending_image_ids": pending_images,
        "skipped_image_ids": [item["image_id"] for item in skipped_images],
        "candidates": len(candidates),
        "reviews": len(reviews),
        "user_decisions": len(decisions),
        "unreviewed_candidate_ids": unreviewed,
        "ready_to_apply": ready,
        "completion_scope": ("partial" if partial else "full") if ready else "in_progress",
    }


def command_status(args: argparse.Namespace) -> None:
    print(json.dumps(status_payload(args.state_dir), ensure_ascii=False, indent=2))


def command_verify(args: argparse.Namespace) -> None:
    manifest, source, data = source_manifest(args.state_dir)
    if len(data) != manifest["size_bytes"]:
        raise ProofreadError("source EPUB size changed")
    entries = {item["name"]: item for item in read_jsonl(args.state_dir / "entries.jsonl")}
    with zipfile.ZipFile(source) as zf:
        if zf.namelist() != list(entries):
            raise ProofreadError("source ZIP entry order changed")
        for name, record in entries.items():
            if sha256_bytes(zf.read(name)) != record["sha256"]:
                raise ProofreadError(f"source ZIP entry changed: {name}")
    for image in read_jsonl(args.state_dir / "images.jsonl"):
        if image.get("extracted_path"):
            extracted = args.state_dir / image["extracted_path"]
            if not extracted.is_file() or sha256_bytes(extracted.read_bytes()) != image["sha256"]:
                raise ProofreadError(f"extracted review image changed: {image['image_id']}")
    load_runtime(args.state_dir)
    print("source_verified=true")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    init = subparsers.add_parser("init")
    init.add_argument("--input", required=True, type=Path)
    init.add_argument("--state-dir", required=True, type=Path)
    init.add_argument("--batch-chars", type=int, default=12000)
    init.add_argument("--overlap-chars", type=int, default=600)
    init.set_defaults(func=command_init)

    for name, function in (("status", command_status), ("verify", command_verify)):
        command = subparsers.add_parser(name)
        command.add_argument("--state-dir", required=True, type=Path)
        command.set_defaults(func=function)

    show_batch = subparsers.add_parser("show-batch")
    show_batch.add_argument("--state-dir", required=True, type=Path)
    show_batch.add_argument("--batch-id", required=True)
    show_batch.set_defaults(func=command_show_batch)

    candidates = subparsers.add_parser("import-candidates")
    candidates.add_argument("--state-dir", required=True, type=Path)
    candidates.add_argument("--file", required=True, type=Path)
    candidates.set_defaults(func=command_import_candidates)

    complete_batch = subparsers.add_parser("complete-batch")
    complete_batch.add_argument("--state-dir", required=True, type=Path)
    complete_batch.add_argument("--batch-id", required=True)
    complete_batch.add_argument("--reviewer", required=True)
    complete_batch.add_argument("--glossary-version", required=True, type=int)
    complete_batch.set_defaults(func=command_complete_batch)

    show_image = subparsers.add_parser("show-image")
    show_image.add_argument("--state-dir", required=True, type=Path)
    show_image.add_argument("--image-id", required=True)
    show_image.set_defaults(func=command_show_image)

    complete_image = subparsers.add_parser("complete-image")
    complete_image.add_argument("--state-dir", required=True, type=Path)
    complete_image.add_argument("--image-id", required=True)
    complete_image.add_argument("--reviewer", required=True)
    complete_image.set_defaults(func=lambda args: update_image_status(args, "complete"))

    skip_image = subparsers.add_parser("skip-image")
    skip_image.add_argument("--state-dir", required=True, type=Path)
    skip_image.add_argument("--image-id", required=True)
    skip_image.add_argument("--reviewer", required=True)
    skip_image.add_argument("--reason", required=True)
    skip_image.set_defaults(func=lambda args: update_image_status(args, "skipped"))

    reviews = subparsers.add_parser("import-reviews")
    reviews.add_argument("--state-dir", required=True, type=Path)
    reviews.add_argument("--file", required=True, type=Path)
    reviews.set_defaults(func=command_import_reviews)

    show_review = subparsers.add_parser("show-review")
    show_review.add_argument("--state-dir", required=True, type=Path)
    show_review.add_argument("--candidate-id", required=True)
    show_review.add_argument("--context-blocks", type=int, default=2)
    show_review.set_defaults(func=command_show_review)

    decisions = subparsers.add_parser("import-decisions")
    decisions.add_argument("--state-dir", required=True, type=Path)
    decisions.add_argument("--file", required=True, type=Path)
    decisions.set_defaults(func=command_import_decisions)

    glossary = subparsers.add_parser("merge-glossary")
    glossary.add_argument("--state-dir", required=True, type=Path)
    glossary.add_argument("--file", required=True, type=Path)
    glossary.set_defaults(func=command_merge_glossary)
    return parser


def main() -> None:
    parser = build_parser()
    args = parser.parse_args()
    if hasattr(args, "batch_chars") and args.batch_chars <= 0:
        parser.error("--batch-chars must be positive")
    if hasattr(args, "overlap_chars") and args.overlap_chars < 0:
        parser.error("--overlap-chars must be non-negative")
    try:
        args.func(args)
    except (ProofreadError, OSError, zipfile.BadZipFile) as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(2)


if __name__ == "__main__":
    main()
