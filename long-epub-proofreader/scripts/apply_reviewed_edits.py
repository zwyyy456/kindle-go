#!/usr/bin/env python3
"""Apply doubly reviewed EPUB text and image-reference edits without reserializing XHTML."""

from __future__ import annotations

import argparse
import json
import sys
import zipfile
from collections import defaultdict
from pathlib import Path
from typing import Any, Dict, Iterable, List, Sequence, Tuple

from epub_core import (
    Block,
    ProofreadError,
    clone_zipinfo,
    encode_xml_text,
    image_maps,
    latest_by,
    load_runtime,
    parse_epub_package,
    read_json,
    read_jsonl,
    scan_xhtml,
    sha256_bytes,
    source_manifest,
    xml_escape_text,
)
from proofread_epub import status_payload


Patch = Tuple[int, int, str]


def classify(candidate: Dict[str, Any], review: Dict[str, Any], decision: Dict[str, Any] | None) -> Tuple[str, str, str]:
    if decision:
        if decision["action"] == "reject":
            return "rejected_by_user", candidate["proposed"], decision["reason"]
        if decision["proposed"] == "keep":
            return "approved_no_change", "keep", decision["reason"]
        return "approved_by_user", decision["proposed"], decision["reason"]
    if review["verdict"] == "reject":
        return "rejected_by_verifier", candidate["proposed"], review["reason"]
    if (
        candidate["confidence"] == "high"
        and review["verdict"] == "high"
        and candidate["proposed"] == review["proposed"]
    ):
        return "approved_automatically", candidate["proposed"], review["reason"]
    return "unresolved", candidate["proposed"], review["reason"]


def common_edges(original: str, proposed: str) -> Tuple[int, int]:
    prefix = 0
    limit = min(len(original), len(proposed))
    while prefix < limit and original[prefix] == proposed[prefix]:
        prefix += 1
    suffix = 0
    while (
        suffix < len(original) - prefix
        and suffix < len(proposed) - prefix
        and original[len(original) - suffix - 1] == proposed[len(proposed) - suffix - 1]
    ):
        suffix += 1
    return prefix, suffix


def text_candidate_patches(block: Block, candidate: Dict[str, Any], proposed: str) -> List[Patch]:
    start = int(candidate["start_offset"])
    end = int(candidate["end_offset"])
    original = str(candidate["original"])
    if block.text[start:end] != original:
        raise ProofreadError(f"candidate source text no longer matches: {candidate['candidate_id']}")
    if "\n" in proposed or "\r" in proposed:
        raise ProofreadError(f"replacement may not add line breaks: {candidate['candidate_id']}")
    prefix, suffix = common_edges(original, proposed)
    old_start = start + prefix
    old_end = end - suffix
    new_end = len(proposed) - suffix if suffix else len(proposed)
    new_middle = proposed[prefix:new_end]
    if old_start == old_end and not new_middle:
        return []

    if old_start == old_end:
        left = block.atoms[old_start - 1] if old_start > 0 and not block.atoms[old_start - 1].virtual else None
        right = next((atom for atom in block.atoms[old_start:] if not atom.virtual), None)
        anchor = left.raw_end if left is not None else (right.raw_start if right is not None else None)
        if anchor is None:
            raise ProofreadError(f"cannot anchor insertion for {candidate['candidate_id']}")
        return [(anchor, anchor, xml_escape_text(new_middle))]

    selected = block.atoms[old_start:old_end]
    if not selected or any(atom.virtual for atom in selected):
        raise ProofreadError(f"candidate crosses an unpatchable line-break marker: {candidate['candidate_id']}")
    selected_spans = {(atom.raw_start, atom.raw_end) for atom in selected}
    outside = block.atoms[:old_start] + block.atoms[old_end:]
    if any((atom.raw_start, atom.raw_end) in selected_spans for atom in outside if not atom.virtual):
        raise ProofreadError(f"candidate selects only part of a decoded XML entity: {candidate['candidate_id']}")

    groups: List[List[Any]] = []
    for atom in selected:
        if not groups or groups[-1][-1].node_id != atom.node_id or atom.raw_start > groups[-1][-1].raw_end:
            groups.append([atom])
        else:
            groups[-1].append(atom)
    patches: List[Patch] = []
    for index, group in enumerate(groups):
        patches.append((group[0].raw_start, group[-1].raw_end, xml_escape_text(new_middle) if index == 0 else ""))
    return patches


def ranges_conflict(left: Patch, right: Patch) -> bool:
    ls, le, _ = left
    rs, re, _ = right
    if ls == le and rs == re:
        return ls == rs
    if ls == le:
        return rs <= ls <= re
    if rs == re:
        return ls <= rs <= le
    return max(ls, rs) < min(le, re)


def candidates_logically_conflict(left: Dict[str, Any], right: Dict[str, Any]) -> bool:
    if left["kind"] == right["kind"] == "text" and left["block_id"] == right["block_id"]:
        return max(int(left["start_offset"]), int(right["start_offset"])) < min(
            int(left["end_offset"]), int(right["end_offset"])
        )
    if left["kind"] == right["kind"] == "image":
        return left["image_usage_id"] == right["image_usage_id"]
    return False


def apply_patches(raw: str, patches: Sequence[Patch], document_href: str) -> str:
    output = raw
    for start, end, replacement in sorted(patches, key=lambda item: (item[0], item[1]), reverse=True):
        if start < 0 or end < start or end > len(output):
            raise ProofreadError(f"invalid raw patch range in {document_href}: {start}:{end}")
        output = output[:start] + replacement + output[end:]
    return output


def image_signature(data: bytes) -> str:
    if data.startswith(b"\xff\xd8\xff"):
        return "image/jpeg"
    if data.startswith(b"\x89PNG\r\n\x1a\n"):
        return "image/png"
    if data.startswith((b"GIF87a", b"GIF89a")):
        return "image/gif"
    if len(data) >= 12 and data[:4] == b"RIFF" and data[8:12] == b"WEBP":
        return "image/webp"
    if data.lstrip().startswith(b"<svg") or b"<svg" in data[:512]:
        return "image/svg+xml"
    return "application/octet-stream"


def render_report(
    manifest: Dict[str, Any],
    audit: Sequence[Dict[str, Any]],
    output: Path,
    output_sha256: str,
    modified_entries: Sequence[str],
    skipped_documents: Sequence[Dict[str, Any]],
    skipped_images: Sequence[Dict[str, Any]],
) -> str:
    applied = [item for item in audit if item["final_status"].startswith("approved_") and item["final_status"] != "approved_no_change"]
    unchanged = [item for item in audit if item["final_status"] == "approved_no_change"]
    unresolved = [item for item in audit if item["final_status"].startswith("unresolved")]
    rejected = [item for item in audit if item["final_status"].startswith("rejected_")]
    partial = bool(skipped_documents or skipped_images)
    lines = [
        "# EPUB 长篇校对报告\n",
        f"- 完成范围：{'部分完成' if partial else '全文完成'}\n",
        f"- 源 EPUB SHA-256：`{manifest['source_sha256']}`\n",
        f"- 修订 EPUB：`{output.name}`\n",
        f"- 修订 EPUB SHA-256：`{output_sha256}`\n",
        f"- 已应用修改：{len(applied)}；确认保持：{len(unchanged)}；待确认：{len(unresolved)}；已否决：{len(rejected)}\n",
        f"- 发生内容变化的 ZIP 条目：{len(modified_entries)}\n",
        "\n## 已应用修改\n",
    ]
    if not applied:
        lines.append("- 无\n")
    for item in applied:
        candidate = item["candidate"]
        location = candidate.get("block_id") or candidate.get("image_usage_id")
        lines.append(
            f"- `{candidate['document_href']}` / `{location}`：`{candidate['original']}` → "
            f"`{item['final_proposed']}`（{item['final_status']}；{item['final_reason']}）\n"
        )
    lines.append("\n## 待用户确认\n")
    if not unresolved:
        lines.append("- 无\n")
    for item in unresolved:
        candidate = item["candidate"]
        lines.append(
            f"- `{candidate['document_href']}`：`{candidate['original']}` → `{candidate['proposed']}`；{item['final_reason']}\n"
        )
    lines.append("\n## 跳过范围\n")
    if not skipped_documents and not skipped_images:
        lines.append("- 无\n")
    for document in skipped_documents:
        lines.append(
            f"- 文档 `{document.get('href', '')}`：{document.get('reason', 'unknown')}；"
            f"未审阅字符估计 {document.get('unreviewed_character_estimate', 0)}\n"
        )
    for image in skipped_images:
        lines.append(f"- 图片 `{image.get('resource_href', '')}`：{image.get('reason', 'unknown')}\n")
    lines.extend([
        "\n## 验证结果\n",
        "- 源 EPUB 哈希与逐条目内容哈希已校验。\n",
        "- 未修改条目的解压内容、名称、顺序及 ZIP 元数据保持不变。\n",
        "- XHTML 未重新序列化；只应用审计记录中的原始范围补丁。\n",
        "- OPF、spine、目录文件、未批准图片资源和其他资源未被改写。\n",
        "- 修订 EPUB 已重复生成并得到完全一致的 SHA-256。\n",
        "- 标点、的/地/得、一般语法、文风和歧义表达未自动修改。\n",
        "\n完整候选、复核、用户决定、跳过和最终分类见 JSONL 审计附录。\n",
    ])
    return "".join(lines)


def generate_epub(
    source: Path,
    output: Path,
    entry_replacements: Dict[str, bytes],
) -> None:
    output.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(source) as zin, zipfile.ZipFile(output, "w") as zout:
        zout.comment = zin.comment
        for info in zin.infolist():
            data = entry_replacements.get(info.filename, zin.read(info.filename))
            zout.writestr(clone_zipinfo(info), data, compress_type=info.compress_type)


def verify_output(
    source: Path,
    output: Path,
    entry_replacements: Dict[str, bytes],
    parsed_document_hrefs: Iterable[str],
) -> None:
    with zipfile.ZipFile(source) as zin, zipfile.ZipFile(output) as zout:
        source_infos = zin.infolist()
        output_infos = zout.infolist()
        if [item.filename for item in output_infos] != [item.filename for item in source_infos]:
            raise ProofreadError("output ZIP entry order changed")
        for source_info, output_info in zip(source_infos, output_infos):
            for field in ("filename", "date_time", "compress_type", "external_attr", "internal_attr", "create_system"):
                if getattr(source_info, field) != getattr(output_info, field):
                    raise ProofreadError(f"output ZIP metadata changed for {source_info.filename}: {field}")
            if source_info.extra != output_info.extra or source_info.comment != output_info.comment:
                raise ProofreadError(f"output ZIP extra metadata changed for {source_info.filename}")
            expected = entry_replacements.get(source_info.filename, zin.read(source_info.filename))
            if zout.read(output_info.filename) != expected:
                raise ProofreadError(f"output ZIP entry content mismatch: {source_info.filename}")
        if output_infos[0].filename != "mimetype" or output_infos[0].compress_type != zipfile.ZIP_STORED:
            raise ProofreadError("output EPUB mimetype invariant failed")
        parse_epub_package(zout)
        for href in parsed_document_hrefs:
            scan_xhtml(zout.read(href), href)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state-dir", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--report", required=True, type=Path)
    parser.add_argument("--audit", required=True, type=Path)
    args = parser.parse_args()

    try:
        state = status_payload(args.state_dir)
        if not state["ready_to_apply"]:
            raise ProofreadError(
                f"state is incomplete: next_batch={state['next_incomplete_batch']} "
                f"pending_images={len(state['pending_image_ids'])} unreviewed={len(state['unreviewed_candidate_ids'])}"
            )
        manifest, source, source_bytes = source_manifest(args.state_dir)
        if args.output.resolve() == source.resolve():
            raise ProofreadError("output path must differ from the immutable source")
        manifest2, documents, blocks = load_runtime(args.state_dir)
        if manifest2["source_sha256"] != manifest["source_sha256"]:
            raise ProofreadError("runtime manifest mismatch")
        block_lookup = {block.block_id: block for block in blocks}
        document_lookup = {document.href: document for document in documents}
        candidates = latest_by(read_jsonl(args.state_dir / "candidates.jsonl"), "candidate_id")
        reviews = latest_by(read_jsonl(args.state_dir / "reviews.jsonl"), "candidate_id")
        decisions = latest_by(read_jsonl(args.state_dir / "decisions.jsonl"), "candidate_id")
        images, usages = image_maps(args.state_dir)

        audit: Dict[str, Dict[str, Any]] = {}
        candidate_patches: Dict[str, List[Tuple[str, Patch]]] = {}
        resource_replacements: Dict[str, Tuple[str, bytes]] = {}
        for candidate_id, candidate in candidates.items():
            status, proposed, reason = classify(candidate, reviews[candidate_id], decisions.get(candidate_id))
            record = {
                "candidate_id": candidate_id,
                "source_sha256": manifest["source_sha256"],
                "candidate": candidate,
                "verification": reviews[candidate_id],
                "user_decision": decisions.get(candidate_id),
                "final_status": status,
                "final_proposed": proposed,
                "final_reason": reason,
            }
            audit[candidate_id] = record
            if not status.startswith("approved_") or status == "approved_no_change":
                continue
            try:
                if candidate["kind"] == "text":
                    block = block_lookup[candidate["block_id"]]
                    patches = text_candidate_patches(block, candidate, proposed)
                    candidate_patches[candidate_id] = [(candidate["document_href"], patch) for patch in patches]
                elif proposed == "remove_reference":
                    image, usage = usages[candidate["image_usage_id"]]
                    document = document_lookup[usage["document_href"]]
                    matched = any(
                        item.resource_href == image["resource_href"]
                        and item.raw_start == usage["raw_start"]
                        and item.raw_end == usage["raw_end"]
                        for item in document.image_usages
                    )
                    if not matched:
                        raise ProofreadError("image usage no longer maps to the original XHTML")
                    candidate_patches[candidate_id] = [(
                        usage["document_href"],
                        (int(usage["raw_start"]), int(usage["raw_end"]), ""),
                    )]
                elif proposed == "replace_resource":
                    decision = decisions[candidate_id]
                    replacement_path = args.state_dir / decision["replacement_path"]
                    data = replacement_path.read_bytes()
                    if sha256_bytes(data) != decision["replacement_sha256"]:
                        raise ProofreadError("stored replacement image hash changed")
                    image = images[candidate["image_id"]]
                    expected_type = image.get("media_type", "")
                    actual_type = image_signature(data)
                    if expected_type and actual_type != expected_type:
                        raise ProofreadError(f"replacement image type {actual_type} does not match {expected_type}")
                    existing = resource_replacements.get(image["resource_href"])
                    if existing and existing[1] != data:
                        raise ProofreadError("conflicting replacements for one image resource")
                    resource_replacements[image["resource_href"]] = (candidate_id, data)
                else:
                    raise ProofreadError(f"unsupported approved proposal: {proposed}")
            except (KeyError, OSError, ProofreadError) as exc:
                record["final_status"] = "unresolved_unmappable"
                record["final_reason"] = str(exc)

        conflicts = set()
        approved_candidates = [
            candidate_id for candidate_id in candidates
            if audit[candidate_id]["final_status"].startswith("approved_")
            and audit[candidate_id]["final_status"] != "approved_no_change"
        ]
        for index, candidate_id in enumerate(approved_candidates):
            for other_id in approved_candidates[index + 1:]:
                if candidates_logically_conflict(candidates[candidate_id], candidates[other_id]):
                    conflicts.update({candidate_id, other_id})
        patch_items = [(candidate_id, href, patch) for candidate_id, values in candidate_patches.items() for href, patch in values]
        for index, (candidate_id, href, patch) in enumerate(patch_items):
            if audit[candidate_id]["final_status"].startswith("unresolved"):
                continue
            for other_id, other_href, other_patch in patch_items[index + 1:]:
                if candidate_id != other_id and href == other_href and ranges_conflict(patch, other_patch):
                    conflicts.update({candidate_id, other_id})
        for candidate_id in conflicts:
            audit[candidate_id]["final_status"] = "unresolved_overlap_conflict"
            audit[candidate_id]["final_reason"] = "approved edit overlaps another approved candidate"

        document_patches: Dict[str, List[Patch]] = defaultdict(list)
        for candidate_id, values in candidate_patches.items():
            if audit[candidate_id]["final_status"].startswith("approved_"):
                for href, patch in values:
                    document_patches[href].append(patch)

        entry_replacements: Dict[str, bytes] = {}
        for href, patches in document_patches.items():
            document = document_lookup[href]
            revised_raw = apply_patches(document.raw_text, patches, href)
            entry_replacements[href] = encode_xml_text(revised_raw, document.codec, document.bom.hex())
        for href, (candidate_id, data) in resource_replacements.items():
            if audit[candidate_id]["final_status"].startswith("approved_"):
                entry_replacements[href] = data

        generate_epub(source, args.output, entry_replacements)
        verify_output(source, args.output, entry_replacements, document_lookup)
        source_visible_text = "\n".join(block.text for block in blocks)
        with zipfile.ZipFile(args.output) as revised_zip:
            revised_visible_text = "\n".join(
                block.text
                for document in documents
                for block in scan_xhtml(revised_zip.read(document.href), document.href).blocks
            )
        for item in read_json(args.state_dir / "glossary.json").get("terms", []):
            term = str(item.get("term", ""))
            if item.get("protected") and term and revised_visible_text.count(term) < source_visible_text.count(term):
                raise ProofreadError(f"protected glossary term was removed or changed: {term}")
        first_hash = sha256_bytes(args.output.read_bytes())
        temporary = args.output.with_suffix(args.output.suffix + ".reproducible.tmp")
        generate_epub(source, temporary, entry_replacements)
        verify_output(source, temporary, entry_replacements, document_lookup)
        second_hash = sha256_bytes(temporary.read_bytes())
        temporary.unlink()
        if first_hash != second_hash:
            raise ProofreadError("regenerating from state produced a different EPUB SHA-256")
        if source.read_bytes() != source_bytes:
            raise ProofreadError("source EPUB changed while generating output")

        entry_hashes = {item["name"]: item["sha256"] for item in read_jsonl(args.state_dir / "entries.jsonl")}
        with zipfile.ZipFile(args.output) as zout:
            modified_entries = [name for name in zout.namelist() if sha256_bytes(zout.read(name)) != entry_hashes[name]]
        expected_modified = sorted(entry_replacements)
        if sorted(modified_entries) != expected_modified:
            raise ProofreadError("modified ZIP entry whitelist does not match actual output")

        audit_records = sorted(
            audit.values(),
            key=lambda item: (
                item["candidate"].get("document_href", ""),
                item["candidate"].get("start_offset", item["candidate"].get("raw_start", 0)),
                item["candidate_id"],
            ),
        )
        args.audit.parent.mkdir(parents=True, exist_ok=True)
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.audit.write_text(
            "".join(json.dumps(item, ensure_ascii=False, sort_keys=True) + "\n" for item in audit_records),
            encoding="utf-8",
        )
        document_records = read_jsonl(args.state_dir / "documents.jsonl")
        image_records = read_jsonl(args.state_dir / "images.jsonl")
        args.report.write_text(
            render_report(
                manifest,
                audit_records,
                args.output,
                first_hash,
                modified_entries,
                [item for item in document_records if item.get("status") == "skipped"],
                [item for item in image_records if item.get("status") == "skipped"],
            ),
            encoding="utf-8",
        )
        print(
            f"applied={sum(1 for item in audit_records if item['final_status'].startswith('approved_') and item['final_status'] != 'approved_no_change')} "
            f"unresolved={sum(1 for item in audit_records if item['final_status'].startswith('unresolved'))} "
            f"scope={state['completion_scope']} sha256={first_hash}"
        )
    except (ProofreadError, OSError, zipfile.BadZipFile) as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(2)


if __name__ == "__main__":
    main()
