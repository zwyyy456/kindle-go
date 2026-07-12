#!/usr/bin/env python3
"""Safely apply doubly reviewed proofreading edits to an immutable source."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any, Dict, List, Tuple

from proofread_txt import (
    ProofreadError,
    latest_by,
    read_json,
    read_jsonl,
    source_state,
    status_payload,
)


def classify(
    candidate: Dict[str, Any],
    review: Dict[str, Any],
    decision: Dict[str, Any] | None,
) -> Tuple[str, str, str]:
    if decision:
        if decision["action"] == "reject":
            return "rejected_by_user", candidate["proposed"], decision["reason"]
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


def chapter_headings(text: str) -> List[str]:
    from proofread_txt import CHAPTER_RE

    return [
        match.group(1).strip()
        for line in text.splitlines()
        for match in [CHAPTER_RE.match(line)]
        if match
    ]


def apply_edits(text: str, edits: List[Dict[str, Any]]) -> str:
    output = text
    for edit in sorted(edits, key=lambda item: item["start_char"], reverse=True):
        start = edit["start_char"]
        end = edit["end_char"]
        if output[start:end] != edit["original"]:
            raise ProofreadError(f"source span no longer matches {edit['candidate_id']}")
        proposed = edit["final_proposed"]
        if "\n" in proposed or "\r" in proposed:
            raise ProofreadError(f"replacement may not add line breaks: {edit['candidate_id']}")
        output = output[:start] + proposed + output[end:]
    return output


def reject_overlaps(approved: List[Dict[str, Any]], audit: Dict[str, Dict[str, Any]]) -> List[Dict[str, Any]]:
    safe: List[Dict[str, Any]] = []
    ordered = sorted(approved, key=lambda item: (item["start_char"], item["end_char"]))
    conflicting = set()
    for index, current in enumerate(ordered):
        for other in ordered[index + 1 :]:
            if other["start_char"] >= current["end_char"]:
                break
            conflicting.add(current["candidate_id"])
            conflicting.add(other["candidate_id"])
    for candidate in ordered:
        candidate_id = candidate["candidate_id"]
        if candidate_id in conflicting:
            audit[candidate_id]["final_status"] = "unresolved_overlap_conflict"
            audit[candidate_id]["final_reason"] = "approved edit overlaps another approved candidate"
        else:
            safe.append(candidate)
    return safe


def render_report(
    manifest: Dict[str, Any],
    audit_records: List[Dict[str, Any]],
    output: Path,
) -> str:
    applied = [item for item in audit_records if item["final_status"].startswith("approved_")]
    unresolved = [item for item in audit_records if item["final_status"].startswith("unresolved")]
    rejected = [item for item in audit_records if item["final_status"].startswith("rejected_")]
    ads = [item for item in applied if item["candidate"]["category"] == "external_ad"]
    lines = [
        "# 长篇小说模型校对报告\n",
        f"- 源文件 SHA-256：`{manifest['source_sha256']}`\n",
        f"- 修订文件：`{output.name}`\n",
        f"- 全文批次：{manifest['batch_count']}（全部完成）\n",
        f"- 已应用修改：{len(applied)}；待确认：{len(unresolved)}；已否决：{len(rejected)}\n",
        f"- 已确认删除的站外广告候选：{len(ads)}\n",
        "\n## 已应用修改\n",
    ]
    if not applied:
        lines.append("- 无\n")
    for item in applied:
        candidate = item["candidate"]
        lines.append(
            f"- 原文件第 {candidate['line']} 行：`{candidate['original']}` → "
            f"`{item['final_proposed']}`（{item['final_status']}；{item['final_reason']}）\n"
        )
    lines.append("\n## 待用户确认\n")
    if not unresolved:
        lines.append("- 无\n")
    for item in unresolved:
        candidate = item["candidate"]
        lines.append(
            f"- 原文件第 {candidate['line']} 行：`{candidate['original']}` → "
            f"`{candidate['proposed']}`；{item['final_reason']}\n"
        )
    lines.extend(
        [
            "\n## 验证结果\n",
            "- 源文件哈希、编码和换行风格已校验。\n",
            "- 所有核心批次均完成首审，所有候选均有隔离复核结果。\n",
            "- 章节标题顺序和受保护词出现次数未发生未授权变化。\n",
            "- 标点、的/地/得、一般语法、文风和歧义表达未自动修改。\n",
            "\n完整候选、复核和否决记录见 JSONL 审计附录。\n",
        ]
    )
    return "".join(lines)


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
                f"unreviewed={len(state['unreviewed_candidate_ids'])}"
            )
        manifest, source_bytes, source_text = source_state(args.state_dir)
        source_path = Path(manifest["source_path"]).resolve()
        if args.output.resolve() == source_path:
            raise ProofreadError("output path must differ from the immutable source")

        candidates = latest_by(read_jsonl(args.state_dir / "candidates.jsonl"), "candidate_id")
        reviews = latest_by(read_jsonl(args.state_dir / "reviews.jsonl"), "candidate_id")
        decisions = latest_by(read_jsonl(args.state_dir / "decisions.jsonl"), "candidate_id")
        audit: Dict[str, Dict[str, Any]] = {}
        approved = []
        for candidate_id, candidate in candidates.items():
            review = reviews[candidate_id]
            status, proposed, reason = classify(candidate, review, decisions.get(candidate_id))
            record = {
                "candidate_id": candidate_id,
                "source_sha256": manifest["source_sha256"],
                "candidate": candidate,
                "verification": review,
                "user_decision": decisions.get(candidate_id),
                "final_status": status,
                "final_proposed": proposed,
                "final_reason": reason,
            }
            audit[candidate_id] = record
            if status.startswith("approved_"):
                edit = dict(candidate)
                edit["final_proposed"] = proposed
                approved.append(edit)

        safe_edits = reject_overlaps(approved, audit)
        safe_ids = {item["candidate_id"] for item in safe_edits}
        for candidate_id, record in audit.items():
            if record["final_status"].startswith("approved_") and candidate_id not in safe_ids:
                record["final_status"] = "unresolved_overlap_conflict"

        revised = apply_edits(source_text, safe_edits)
        if chapter_headings(revised) != chapter_headings(source_text):
            raise ProofreadError("chapter heading sequence changed")
        glossary = read_json(args.state_dir / "glossary.json")
        for term in glossary.get("terms", []):
            if term.get("protected") and revised.count(term["term"]) < source_text.count(term["term"]):
                raise ProofreadError(f"protected glossary term was removed or changed: {term['term']}")

        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.audit.parent.mkdir(parents=True, exist_ok=True)
        encoded = revised.encode(manifest["encoding"])
        args.output.write_bytes(encoded)
        audit_records = sorted(
            audit.values(),
            key=lambda item: (item["candidate"]["start_char"], item["candidate_id"]),
        )
        args.audit.write_text(
            "".join(json.dumps(item, ensure_ascii=False, sort_keys=True) + "\n" for item in audit_records),
            encoding="utf-8",
        )
        args.report.write_text(render_report(manifest, audit_records, args.output), encoding="utf-8")

        if Path(manifest["source_path"]).read_bytes() != source_bytes:
            raise ProofreadError("source changed while generating output")
        if args.output.read_bytes() != revised.encode(manifest["encoding"]):
            raise ProofreadError("output verification failed")
        print(
            f"applied={len(safe_edits)} unresolved="
            f"{sum(1 for item in audit_records if item['final_status'].startswith('unresolved'))}"
        )
    except ProofreadError as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(2)


if __name__ == "__main__":
    main()
