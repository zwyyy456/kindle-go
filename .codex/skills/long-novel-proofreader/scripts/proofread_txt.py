#!/usr/bin/env python3
"""Prepare and maintain immutable, model-led long-novel proofreading state."""

from __future__ import annotations

import argparse
import bisect
import hashlib
import json
import re
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Dict, Iterable, List, Tuple


STATE_VERSION = 1
CORE_CATEGORIES = {
    "wrong_character",
    "wrong_word",
    "homophone",
    "missing_character",
    "missing_word",
    "duplicate_character",
    "extra_text",
    "transposition",
    "word_boundary",
    "malformed_expression",
    "external_ad",
}
REVIEW_ONLY_CATEGORIES = {
    "grammar",
    "punctuation",
    "structural_punctuation",
    "de_di_de",
    "style",
    "semantic_mismatch",
    "paired_structure",
    "entity_consistency",
    "reference",
    "timeline",
    "location",
    "duplicate_text",
    "text_noise",
    "continuity",
    "ambiguous",
}
SCOPE_REQUIRED_CATEGORIES = {
    "entity_consistency",
    "reference",
    "timeline",
    "location",
    "continuity",
}
CONTEXT_NOTE_KINDS = {
    "entity",
    "alias",
    "reference",
    "relationship",
    "time",
    "timeline",
    "location",
    "state",
    "continuity",
    "chapter_summary",
    "anomaly",
}
KNOWN_HINTS = (
    "噗之以鼻",
    "目地",
    "遂即",
    "更多精校小说尽在知轩藏书下载：",
)
CHAPTER_RE = re.compile(r"^\s*(第.{1,24}[章节卷部回].*|序章.*|楔子.*|尾声.*|完本感言.*)\s*$")


class ProofreadError(RuntimeError):
    pass


def now_iso() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat()


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def detect_encoding(data: bytes) -> str:
    if data.startswith(b"\xef\xbb\xbf"):
        data.decode("utf-8-sig")
        return "utf-8-sig"
    try:
        data.decode("utf-8")
        return "utf-8"
    except UnicodeDecodeError:
        try:
            data.decode("gb18030")
            return "gb18030"
        except UnicodeDecodeError as exc:
            raise ProofreadError("input is neither UTF-8 nor GB18030") from exc


def newline_style(text: str) -> str:
    crlf = text.count("\r\n")
    lf = text.count("\n") - crlf
    cr = text.count("\r") - crlf
    styles = [(crlf, "crlf"), (lf, "lf"), (cr, "cr")]
    count, style = max(styles)
    return style if count else "none"


def read_json(path: Path) -> Dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def read_jsonl(path: Path) -> List[Dict[str, Any]]:
    if not path.exists():
        return []
    records: List[Dict[str, Any]] = []
    for line_no, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if not raw.strip():
            continue
        try:
            value = json.loads(raw)
        except json.JSONDecodeError as exc:
            raise ProofreadError(f"invalid JSONL at {path}:{line_no}: {exc}") from exc
        if not isinstance(value, dict):
            raise ProofreadError(f"expected object at {path}:{line_no}")
        records.append(value)
    return records


def write_json(path: Path, value: Any) -> None:
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    temporary.replace(path)


def write_jsonl(path: Path, records: Iterable[Dict[str, Any]]) -> None:
    temporary = path.with_suffix(path.suffix + ".tmp")
    body = "".join(json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n" for record in records)
    temporary.write_text(body, encoding="utf-8")
    temporary.replace(path)


def append_jsonl(path: Path, records: Iterable[Dict[str, Any]]) -> int:
    incoming = list(records)
    if not incoming:
        return 0
    with path.open("a", encoding="utf-8", newline="\n") as handle:
        for record in incoming:
            handle.write(json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n")
    return len(incoming)


def source_state(state_dir: Path) -> Tuple[Dict[str, Any], bytes, str]:
    manifest_path = state_dir / "manifest.json"
    if not manifest_path.exists():
        raise ProofreadError(f"missing state manifest: {manifest_path}")
    manifest = read_json(manifest_path)
    if manifest.get("version") != STATE_VERSION:
        raise ProofreadError(f"unsupported state version: {manifest.get('version')}")
    source_path = Path(manifest["source_path"])
    if not source_path.exists():
        raise ProofreadError(f"source file is missing: {source_path}")
    data = source_path.read_bytes()
    digest = sha256_bytes(data)
    if digest != manifest["source_sha256"]:
        raise ProofreadError("source hash changed; refuse to continue")
    text = data.decode(manifest["encoding"])
    return manifest, data, text


def line_layout(text: str) -> Tuple[List[str], List[int]]:
    lines = text.splitlines(keepends=True)
    if not lines and text:
        lines = [text]
    starts: List[int] = []
    position = 0
    for line in lines:
        starts.append(position)
        position += len(line)
    return lines, starts


def line_for_offset(starts: List[int], offset: int) -> int:
    if not starts:
        return 1
    return max(1, bisect.bisect_right(starts, offset))


def build_batches(text: str, target_chars: int, overlap_chars: int) -> List[Dict[str, Any]]:
    lines, starts = line_layout(text)
    if not lines:
        return []
    boundaries = starts + [len(text)]
    chapters: List[Tuple[int, str]] = []
    for index, line in enumerate(lines):
        stripped = line.rstrip("\r\n")
        match = CHAPTER_RE.match(stripped)
        if match:
            chapters.append((index + 1, match.group(1).strip()))

    batches: List[Dict[str, Any]] = []
    first_line = 0
    while first_line < len(lines):
        core_start = boundaries[first_line]
        last_line = first_line
        while last_line + 1 < len(lines) and boundaries[last_line + 1] - core_start < target_chars:
            last_line += 1
        core_end = boundaries[last_line + 1]
        context_start_target = max(0, core_start - overlap_chars)
        context_start_line = max(0, bisect.bisect_right(boundaries, context_start_target) - 1)
        context_end_target = min(len(text), core_end + overlap_chars)
        context_end_line = min(len(lines), bisect.bisect_left(boundaries, context_end_target))
        context_start = boundaries[context_start_line]
        context_end = boundaries[context_end_line]
        if context_end < core_end:
            context_end = core_end

        chapter = ""
        for chapter_line, heading in chapters:
            if chapter_line <= first_line + 1:
                chapter = heading
            else:
                break
        batch_number = len(batches) + 1
        batches.append(
            {
                "batch_id": f"batch-{batch_number:05d}",
                "chapter": chapter,
                "core_start_char": core_start,
                "core_end_char": core_end,
                "core_start_line": first_line + 1,
                "core_end_line": last_line + 1,
                "context_start_char": context_start,
                "context_end_char": context_end,
                "context_start_line": context_start_line + 1,
                "context_end_line": line_for_offset(starts, max(context_start, context_end - 1)),
                "status": "pending",
            }
        )
        first_line = last_line + 1
    return batches


def command_init(args: argparse.Namespace) -> None:
    source = args.input.resolve()
    if not source.is_file():
        raise ProofreadError(f"input is not a file: {source}")
    state_dir = args.state_dir.resolve()
    if state_dir.exists() and any(state_dir.iterdir()):
        raise ProofreadError(f"state directory is not empty: {state_dir}")
    state_dir.mkdir(parents=True, exist_ok=True)

    data = source.read_bytes()
    encoding = detect_encoding(data)
    text = data.decode(encoding)
    batches = build_batches(text, args.batch_chars, args.overlap_chars)
    lines, _ = line_layout(text)
    chapter_count = sum(1 for line in lines if CHAPTER_RE.match(line.rstrip("\r\n")))
    manifest = {
        "version": STATE_VERSION,
        "created_at": now_iso(),
        "source_path": str(source),
        "source_sha256": sha256_bytes(data),
        "encoding": encoding,
        "newline": newline_style(text),
        "size_bytes": len(data),
        "character_count": len(text),
        "line_count": len(lines),
        "chapter_marker_count": chapter_count,
        "batch_chars": args.batch_chars,
        "overlap_chars": args.overlap_chars,
        "batch_count": len(batches),
        "required_passes": ["global_consistency_review"],
    }
    write_json(state_dir / "manifest.json", manifest)
    write_jsonl(state_dir / "batches.jsonl", batches)
    for filename in ("candidates.jsonl", "reviews.jsonl", "decisions.jsonl", "context.jsonl"):
        (state_dir / filename).write_text("", encoding="utf-8")
    write_json(state_dir / "glossary.json", {"version": 1, "terms": []})
    write_json(
        state_dir / "consistency.json",
        {"status": "pending", "completed_at": None, "reviewer": None},
    )
    print(json.dumps(manifest, ensure_ascii=False, indent=2))


def find_batch(state_dir: Path, batch_id: str) -> Dict[str, Any]:
    for batch in read_jsonl(state_dir / "batches.jsonl"):
        if batch["batch_id"] == batch_id:
            return batch
    raise ProofreadError(f"unknown batch: {batch_id}")


def command_show_batch(args: argparse.Namespace) -> None:
    manifest, _, text = source_state(args.state_dir)
    batch = find_batch(args.state_dir, args.batch_id)
    lines, starts = line_layout(text)
    rendered = []
    for line_no in range(batch["context_start_line"], batch["context_end_line"] + 1):
        if 1 <= line_no <= len(lines):
            scope = "core" if batch["core_start_line"] <= line_no <= batch["core_end_line"] else "overlap"
            rendered.append(f"{line_no}\t[{scope}]\t{lines[line_no - 1].rstrip(chr(10) + chr(13))}")
    excerpt = text[batch["context_start_char"] : batch["context_end_char"]]
    hints = []
    for pattern in KNOWN_HINTS:
        if pattern in excerpt:
            hints.append({"pattern": pattern, "count": excerpt.count(pattern), "decision": "model_required"})
    payload = {
        "source_sha256": manifest["source_sha256"],
        "batch": batch,
        "glossary": read_json(args.state_dir / "glossary.json"),
        "hints": hints,
        "instruction": (
            "Review every core line; overlap is context only. Hints are not decisions. "
            "Check character/word integrity, sentence structure, lexical boundaries, local semantics, "
            "paired punctuation/structures, paragraph or chapter-edge duplication/gaps, and text contamination. "
            "Return concise context notes for later global entity and continuity review. "
            "Candidates in entity/reference/timeline/location/continuity categories must include scope_keys. "
            "Do not skip sentences without hints."
        ),
        "text": "\n".join(rendered),
    }
    print(json.dumps(payload, ensure_ascii=False, indent=2))


def load_input_records(path: Path) -> List[Dict[str, Any]]:
    return read_jsonl(path)


def occurrences(haystack: str, needle: str) -> List[int]:
    if not needle:
        return []
    found = []
    start = 0
    while True:
        index = haystack.find(needle, start)
        if index < 0:
            return found
        found.append(index)
        start = index + len(needle)


def normalize_candidate(
    record: Dict[str, Any], manifest: Dict[str, Any], text: str, batches: Dict[str, Dict[str, Any]]
) -> Dict[str, Any]:
    required = ("batch_id", "line", "original", "proposed", "category", "confidence", "reason", "context")
    missing = [key for key in required if key not in record]
    if missing:
        raise ProofreadError(f"candidate missing fields: {', '.join(missing)}")
    batch = batches.get(str(record["batch_id"]))
    if not batch:
        raise ProofreadError(f"candidate has unknown batch: {record['batch_id']}")
    line_no = int(record["line"])
    if not batch["core_start_line"] <= line_no <= batch["core_end_line"]:
        raise ProofreadError(f"candidate line {line_no} is outside {batch['batch_id']} core range")
    category = str(record["category"])
    confidence = str(record["confidence"])
    if category not in CORE_CATEGORIES | REVIEW_ONLY_CATEGORIES:
        raise ProofreadError(f"unsupported candidate category: {category}")
    if confidence not in {"high", "review"}:
        raise ProofreadError(f"unsupported candidate confidence: {confidence}")
    if category in REVIEW_ONLY_CATEGORIES and confidence != "review":
        raise ProofreadError(f"{category} candidates must use confidence=review")

    lines, starts = line_layout(text)
    if line_no < 1 or line_no > len(lines):
        raise ProofreadError(f"candidate line does not exist: {line_no}")
    original = str(record["original"])
    proposed = str(record["proposed"])
    if not original:
        raise ProofreadError("candidate original must not be empty")
    line_text = lines[line_no - 1].rstrip("\r\n")
    positions = occurrences(line_text, original)
    occurrence = int(record.get("occurrence", 1))
    if occurrence < 1 or occurrence > len(positions):
        raise ProofreadError(
            f"cannot resolve occurrence {occurrence} of {original!r} on source line {line_no}"
        )
    start_char = starts[line_no - 1] + positions[occurrence - 1]
    end_char = start_char + len(original)
    identity = f"{manifest['source_sha256']}:{start_char}:{end_char}:{proposed}".encode("utf-8")
    candidate_id = "cand-" + hashlib.sha256(identity).hexdigest()[:20]
    scope_keys = record.get("scope_keys", [])
    if not isinstance(scope_keys, list):
        raise ProofreadError("candidate scope_keys must be a JSON array")
    normalized_scope_keys = sorted(
        {str(item).strip() for item in scope_keys if str(item).strip()}
    )
    if category in SCOPE_REQUIRED_CATEGORIES and not normalized_scope_keys:
        raise ProofreadError(f"{category} candidates require non-empty scope_keys")
    return {
        "candidate_id": candidate_id,
        "source_sha256": manifest["source_sha256"],
        "batch_id": batch["batch_id"],
        "line": line_no,
        "start_char": start_char,
        "end_char": end_char,
        "original": original,
        "occurrence": occurrence,
        "proposed": proposed,
        "category": category,
        "confidence": confidence,
        "reason": str(record["reason"]),
        "context": str(record["context"]),
        "scope_keys": normalized_scope_keys,
        "recorded_at": now_iso(),
    }


def command_import_candidates(args: argparse.Namespace) -> None:
    manifest, _, text = source_state(args.state_dir)
    batches = {item["batch_id"]: item for item in read_jsonl(args.state_dir / "batches.jsonl")}
    existing = read_jsonl(args.state_dir / "candidates.jsonl")
    known = {record["candidate_id"] for record in existing}
    normalized = []
    for record in load_input_records(args.file):
        candidate = normalize_candidate(record, manifest, text, batches)
        if candidate["candidate_id"] not in known:
            normalized.append(candidate)
            known.add(candidate["candidate_id"])
    append_jsonl(args.state_dir / "candidates.jsonl", normalized)
    print(f"imported_candidates={len(normalized)}")


def command_complete_batch(args: argparse.Namespace) -> None:
    source_state(args.state_dir)
    batches = read_jsonl(args.state_dir / "batches.jsonl")
    matched = False
    for batch in batches:
        if batch["batch_id"] == args.batch_id:
            batch["status"] = "complete"
            batch["completed_at"] = now_iso()
            batch["reviewer"] = args.reviewer
            batch["glossary_version"] = args.glossary_version
            matched = True
    if not matched:
        raise ProofreadError(f"unknown batch: {args.batch_id}")
    write_jsonl(args.state_dir / "batches.jsonl", batches)
    print(f"completed_batch={args.batch_id}")


def latest_by(records: Iterable[Dict[str, Any]], key: str) -> Dict[str, Dict[str, Any]]:
    result: Dict[str, Dict[str, Any]] = {}
    for record in records:
        result[str(record[key])] = record
    return result


def command_import_reviews(args: argparse.Namespace) -> None:
    source_state(args.state_dir)
    candidates = latest_by(read_jsonl(args.state_dir / "candidates.jsonl"), "candidate_id")
    normalized = []
    for record in load_input_records(args.file):
        required = ("candidate_id", "verdict", "proposed", "reason", "review_mode", "reviewer")
        missing = [key for key in required if key not in record]
        if missing:
            raise ProofreadError(f"review missing fields: {', '.join(missing)}")
        candidate_id = str(record["candidate_id"])
        if candidate_id not in candidates:
            raise ProofreadError(f"review has unknown candidate: {candidate_id}")
        verdict = str(record["verdict"])
        if verdict not in {"high", "review", "reject"}:
            raise ProofreadError(f"unsupported review verdict: {verdict}")
        review_mode = str(record["review_mode"])
        if review_mode not in {"independent_subagent", "same_model_isolated"}:
            raise ProofreadError(f"unsupported review mode: {review_mode}")
        normalized.append(
            {
                "candidate_id": candidate_id,
                "verdict": verdict,
                "proposed": str(record["proposed"]),
                "reason": str(record["reason"]),
                "review_mode": review_mode,
                "reviewer": str(record["reviewer"]),
                "recorded_at": now_iso(),
            }
        )
    append_jsonl(args.state_dir / "reviews.jsonl", normalized)
    print(f"imported_reviews={len(normalized)}")


def command_show_review(args: argparse.Namespace) -> None:
    manifest, _, text = source_state(args.state_dir)
    candidates = latest_by(read_jsonl(args.state_dir / "candidates.jsonl"), "candidate_id")
    candidate = candidates.get(args.candidate_id)
    if not candidate:
        raise ProofreadError(f"unknown candidate: {args.candidate_id}")
    lines, _ = line_layout(text)
    line_no = int(candidate["line"])
    context_start = max(1, line_no - args.context_lines)
    context_end = min(len(lines), line_no + args.context_lines)
    rendered = "\n".join(
        f"{number}\t{lines[number - 1].rstrip(chr(10) + chr(13))}"
        for number in range(context_start, context_end + 1)
    )
    scope_keys = set(candidate.get("scope_keys", []))
    related_context = []
    if scope_keys:
        for note in read_jsonl(args.state_dir / "context.jsonl"):
            if note.get("kind") == "anomaly":
                continue
            searchable = " ".join(
                str(note.get(field, "")) for field in ("subject", "value", "context")
            )
            if any(key in searchable for key in scope_keys):
                related_context.append(note)
    payload = {
        "source_sha256": manifest["source_sha256"],
        "candidate_id": candidate["candidate_id"],
        "line": candidate["line"],
        "original": candidate["original"],
        "proposed": candidate["proposed"],
        "category": candidate["category"],
        "context": rendered,
        "scope_keys": candidate.get("scope_keys", []),
        "related_context_notes": related_context,
        "protected_glossary": [
            item
            for item in read_json(args.state_dir / "glossary.json").get("terms", [])
            if item.get("protected")
        ],
        "instruction": "Judge independently. First-review confidence and rationale are intentionally hidden.",
    }
    print(json.dumps(payload, ensure_ascii=False, indent=2))


def command_import_decisions(args: argparse.Namespace) -> None:
    source_state(args.state_dir)
    candidates = latest_by(read_jsonl(args.state_dir / "candidates.jsonl"), "candidate_id")
    normalized = []
    for record in load_input_records(args.file):
        candidate_id = str(record.get("candidate_id", ""))
        action = str(record.get("action", ""))
        if candidate_id not in candidates:
            raise ProofreadError(f"decision has unknown candidate: {candidate_id}")
        if action not in {"accept", "reject"}:
            raise ProofreadError(f"unsupported decision action: {action}")
        normalized.append(
            {
                "candidate_id": candidate_id,
                "action": action,
                "proposed": str(record.get("proposed", candidates[candidate_id]["proposed"])),
                "reason": str(record.get("reason", "user decision")),
                "recorded_at": now_iso(),
            }
        )
    append_jsonl(args.state_dir / "decisions.jsonl", normalized)
    print(f"imported_decisions={len(normalized)}")


def command_merge_glossary(args: argparse.Namespace) -> None:
    source_state(args.state_dir)
    glossary_path = args.state_dir / "glossary.json"
    glossary = read_json(glossary_path)
    terms = {item["term"]: item for item in glossary.get("terms", [])}
    for record in json.loads(args.file.read_text(encoding="utf-8")):
        term = str(record.get("term", "")).strip()
        batch_id = str(record.get("batch_id", "")).strip()
        if not term or not batch_id:
            raise ProofreadError("glossary proposal requires term and batch_id")
        item = terms.setdefault(
            term,
            {"term": term, "type": str(record.get("type", "invented_term")), "evidence_batches": [], "user_protected": False},
        )
        if batch_id not in item["evidence_batches"]:
            item["evidence_batches"].append(batch_id)
        if record.get("user_protected") is True:
            item["user_protected"] = True
        item["protected"] = item["user_protected"] or len(item["evidence_batches"]) >= 2
    glossary["version"] = int(glossary.get("version", 0)) + 1
    glossary["terms"] = sorted(terms.values(), key=lambda item: item["term"])
    write_json(glossary_path, glossary)
    print(f"glossary_version={glossary['version']} terms={len(terms)}")


def command_import_context(args: argparse.Namespace) -> None:
    manifest, _, text = source_state(args.state_dir)
    batches = {item["batch_id"]: item for item in read_jsonl(args.state_dir / "batches.jsonl")}
    lines, _ = line_layout(text)
    existing = read_jsonl(args.state_dir / "context.jsonl")
    known = {record["context_id"] for record in existing}
    normalized = []
    required = ("batch_id", "line", "kind", "subject", "value", "context")
    for record in load_input_records(args.file):
        missing = [key for key in required if key not in record]
        if missing:
            raise ProofreadError(f"context note missing fields: {', '.join(missing)}")
        batch_id = str(record["batch_id"])
        batch = batches.get(batch_id)
        if not batch:
            raise ProofreadError(f"context note has unknown batch: {batch_id}")
        line_no = int(record["line"])
        if not batch["core_start_line"] <= line_no <= batch["core_end_line"]:
            raise ProofreadError(f"context note line {line_no} is outside {batch_id} core range")
        if line_no < 1 or line_no > len(lines):
            raise ProofreadError(f"context note line does not exist: {line_no}")
        kind = str(record["kind"])
        if kind not in CONTEXT_NOTE_KINDS:
            raise ProofreadError(f"unsupported context note kind: {kind}")
        subject = str(record["subject"]).strip()
        value = str(record["value"]).strip()
        context = str(record["context"]).strip()
        if not subject or not value or not context:
            raise ProofreadError("context note subject, value, and context must not be empty")
        identity = f"{manifest['source_sha256']}:{batch_id}:{line_no}:{kind}:{subject}:{value}".encode("utf-8")
        context_id = "ctx-" + hashlib.sha256(identity).hexdigest()[:20]
        if context_id in known:
            continue
        known.add(context_id)
        normalized.append(
            {
                "context_id": context_id,
                "source_sha256": manifest["source_sha256"],
                "batch_id": batch_id,
                "line": line_no,
                "kind": kind,
                "subject": subject,
                "value": value,
                "context": context,
                "recorded_at": now_iso(),
            }
        )
    append_jsonl(args.state_dir / "context.jsonl", normalized)
    print(f"imported_context_notes={len(normalized)}")


def command_show_context(args: argparse.Namespace) -> None:
    manifest, _, _ = source_state(args.state_dir)
    notes = read_jsonl(args.state_dir / "context.jsonl")
    if args.kind:
        notes = [item for item in notes if item.get("kind") == args.kind]
    if args.subject:
        notes = [
            item
            for item in notes
            if args.subject in str(item.get("subject", ""))
            or args.subject in str(item.get("value", ""))
        ]
    payload = {
        "source_sha256": manifest["source_sha256"],
        "glossary": read_json(args.state_dir / "glossary.json"),
        "notes": notes,
        "instruction": (
            "Review these notes for entity/name/reference, relationship, time, location, state, "
            "chapter-transition, duplication, or contamination conflicts. Reopen the relevant source "
            "batches before recording any candidate. Global inconsistencies are review-only unless "
            "the source contains one uniquely recoverable local textual defect."
        ),
    }
    print(json.dumps(payload, ensure_ascii=False, indent=2))


def command_complete_consistency(args: argparse.Namespace) -> None:
    manifest, _, _ = source_state(args.state_dir)
    batches = read_jsonl(args.state_dir / "batches.jsonl")
    incomplete = [item["batch_id"] for item in batches if item.get("status") != "complete"]
    if incomplete:
        raise ProofreadError("cannot complete consistency review before all batches are complete")
    required_passes = manifest.get("required_passes")
    consistency_required = required_passes is None or "global_consistency_review" in required_passes
    if not consistency_required:
        raise ProofreadError("this state does not require a global consistency review")
    write_json(
        args.state_dir / "consistency.json",
        {"status": "complete", "completed_at": now_iso(), "reviewer": args.reviewer},
    )
    print("global_consistency_review=complete")


def status_payload(state_dir: Path) -> Dict[str, Any]:
    manifest, _, _ = source_state(state_dir)
    batches = read_jsonl(state_dir / "batches.jsonl")
    candidates = latest_by(read_jsonl(state_dir / "candidates.jsonl"), "candidate_id")
    reviews = latest_by(read_jsonl(state_dir / "reviews.jsonl"), "candidate_id")
    decisions = latest_by(read_jsonl(state_dir / "decisions.jsonl"), "candidate_id")
    incomplete = [item["batch_id"] for item in batches if item.get("status") != "complete"]
    unreviewed = [candidate_id for candidate_id in candidates if candidate_id not in reviews]
    required_passes = manifest.get("required_passes")
    consistency_required = required_passes is None or "global_consistency_review" in required_passes
    consistency_path = state_dir / "consistency.json"
    consistency = (
        read_json(consistency_path)
        if consistency_path.exists()
        else {"status": "pending" if consistency_required else "not_required"}
    )
    consistency_complete = not consistency_required or consistency.get("status") == "complete"
    return {
        "source_sha256": manifest["source_sha256"],
        "batches_total": len(batches),
        "batches_complete": len(batches) - len(incomplete),
        "next_incomplete_batch": incomplete[0] if incomplete else None,
        "candidates": len(candidates),
        "reviews": len(reviews),
        "user_decisions": len(decisions),
        "context_notes": len(read_jsonl(state_dir / "context.jsonl")),
        "global_consistency_review": consistency.get("status"),
        "unreviewed_candidate_ids": unreviewed,
        "ready_to_apply": not incomplete and not unreviewed and consistency_complete,
    }


def command_status(args: argparse.Namespace) -> None:
    print(json.dumps(status_payload(args.state_dir), ensure_ascii=False, indent=2))


def command_verify(args: argparse.Namespace) -> None:
    manifest, data, text = source_state(args.state_dir)
    if len(data) != manifest["size_bytes"] or len(text) != manifest["character_count"]:
        raise ProofreadError("source size or character count changed")
    if newline_style(text) != manifest["newline"]:
        raise ProofreadError("source newline style changed")
    print("source_verified=true")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)

    init = subparsers.add_parser("init", help="initialize immutable resumable state")
    init.add_argument("--input", required=True, type=Path)
    init.add_argument("--state-dir", required=True, type=Path)
    init.add_argument("--batch-chars", type=int, default=12000)
    init.add_argument("--overlap-chars", type=int, default=600)
    init.set_defaults(func=command_init)

    for name, function in (("status", command_status), ("verify", command_verify)):
        subparser = subparsers.add_parser(name)
        subparser.add_argument("--state-dir", required=True, type=Path)
        subparser.set_defaults(func=function)

    show = subparsers.add_parser("show-batch")
    show.add_argument("--state-dir", required=True, type=Path)
    show.add_argument("--batch-id", required=True)
    show.set_defaults(func=command_show_batch)

    candidates = subparsers.add_parser("import-candidates")
    candidates.add_argument("--state-dir", required=True, type=Path)
    candidates.add_argument("--file", required=True, type=Path)
    candidates.set_defaults(func=command_import_candidates)

    complete = subparsers.add_parser("complete-batch")
    complete.add_argument("--state-dir", required=True, type=Path)
    complete.add_argument("--batch-id", required=True)
    complete.add_argument("--reviewer", required=True)
    complete.add_argument("--glossary-version", required=True, type=int)
    complete.set_defaults(func=command_complete_batch)

    reviews = subparsers.add_parser("import-reviews")
    reviews.add_argument("--state-dir", required=True, type=Path)
    reviews.add_argument("--file", required=True, type=Path)
    reviews.set_defaults(func=command_import_reviews)

    show_review = subparsers.add_parser("show-review")
    show_review.add_argument("--state-dir", required=True, type=Path)
    show_review.add_argument("--candidate-id", required=True)
    show_review.add_argument("--context-lines", type=int, default=2)
    show_review.set_defaults(func=command_show_review)

    decisions = subparsers.add_parser("import-decisions")
    decisions.add_argument("--state-dir", required=True, type=Path)
    decisions.add_argument("--file", required=True, type=Path)
    decisions.set_defaults(func=command_import_decisions)

    glossary = subparsers.add_parser("merge-glossary")
    glossary.add_argument("--state-dir", required=True, type=Path)
    glossary.add_argument("--file", required=True, type=Path)
    glossary.set_defaults(func=command_merge_glossary)

    context = subparsers.add_parser("import-context")
    context.add_argument("--state-dir", required=True, type=Path)
    context.add_argument("--file", required=True, type=Path)
    context.set_defaults(func=command_import_context)

    show_context = subparsers.add_parser("show-context")
    show_context.add_argument("--state-dir", required=True, type=Path)
    show_context.add_argument("--kind", choices=sorted(CONTEXT_NOTE_KINDS))
    show_context.add_argument("--subject")
    show_context.set_defaults(func=command_show_context)

    consistency = subparsers.add_parser("complete-consistency")
    consistency.add_argument("--state-dir", required=True, type=Path)
    consistency.add_argument("--reviewer", required=True)
    consistency.set_defaults(func=command_complete_consistency)
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
    except ProofreadError as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(2)


if __name__ == "__main__":
    main()
