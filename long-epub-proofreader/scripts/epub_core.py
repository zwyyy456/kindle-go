#!/usr/bin/env python3
"""Shared deterministic EPUB parsing and state helpers for conservative proofreading."""

from __future__ import annotations

import codecs
import hashlib
import html
import json
import mimetypes
import posixpath
import re
import zipfile
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional, Sequence, Tuple
from urllib.parse import unquote, urlsplit
from xml.etree import ElementTree as ET


STATE_VERSION = 1
BLOCK_ELEMENTS = {
    "p", "h1", "h2", "h3", "h4", "h5", "h6", "li", "blockquote",
    "div", "section", "article", "aside", "td", "th", "pre", "figcaption",
}
SKIPPED_TEXT_ELEMENTS = {"script", "style"}
VOID_ELEMENTS = {"area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr"}
TEXT_CORE_CATEGORIES = {
    "wrong_character", "homophone", "missing_character", "duplicate_character", "external_ad",
}
REVIEW_ONLY_CATEGORIES = {"grammar", "punctuation", "de_di_de", "style", "ambiguous"}
IMAGE_CATEGORIES = {"external_ad_image", "mixed_ad_image"}
KNOWN_HINTS = ("噗之以鼻", "目地", "遂即", "更多精校小说尽在", "书籍免费分享微信")


class ProofreadError(RuntimeError):
    pass


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def read_json(path: Path) -> Dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ProofreadError(f"cannot read JSON {path}: {exc}") from exc
    if not isinstance(value, dict):
        raise ProofreadError(f"expected JSON object in {path}")
    return value


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
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    temporary.replace(path)


def write_jsonl(path: Path, records: Iterable[Dict[str, Any]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
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


def latest_by(records: Iterable[Dict[str, Any]], key: str) -> Dict[str, Dict[str, Any]]:
    result: Dict[str, Dict[str, Any]] = {}
    for record in records:
        result[str(record[key])] = record
    return result


def local_name(tag: str) -> str:
    return tag.rsplit("}", 1)[-1].lower()


def resolve_archive_path(base_path: str, reference: str) -> str:
    parts = urlsplit(html.unescape(reference.strip()))
    if parts.scheme or parts.netloc:
        raise ProofreadError(f"external reference is not a local EPUB path: {reference!r}")
    decoded = unquote(parts.path)
    if not decoded:
        raise ProofreadError(f"empty EPUB reference in {base_path}")
    joined = posixpath.normpath(posixpath.join(posixpath.dirname(base_path), decoded))
    if joined.startswith("../") or joined == ".." or joined.startswith("/"):
        raise ProofreadError(f"EPUB reference escapes archive root: {reference!r}")
    return joined


def detect_xml_encoding(data: bytes) -> Tuple[str, bytes, str]:
    if data.startswith(codecs.BOM_UTF8):
        codec, bom = "utf-8", codecs.BOM_UTF8
    elif data.startswith(codecs.BOM_UTF32_LE):
        codec, bom = "utf-32-le", codecs.BOM_UTF32_LE
    elif data.startswith(codecs.BOM_UTF32_BE):
        codec, bom = "utf-32-be", codecs.BOM_UTF32_BE
    elif data.startswith(codecs.BOM_UTF16_LE):
        codec, bom = "utf-16-le", codecs.BOM_UTF16_LE
    elif data.startswith(codecs.BOM_UTF16_BE):
        codec, bom = "utf-16-be", codecs.BOM_UTF16_BE
    else:
        match = re.search(br"<\?xml[^>]*encoding\s*=\s*['\"]\s*([^'\"\s]+)", data[:512], re.I)
        codec, bom = ((match.group(1).decode("ascii"), b"") if match else ("utf-8", b""))
    try:
        canonical = codecs.lookup(codec).name
        text = data[len(bom):].decode(canonical)
    except (LookupError, UnicodeDecodeError) as exc:
        raise ProofreadError(f"unsupported or invalid XHTML encoding {codec!r}: {exc}") from exc
    if bom + text.encode(canonical) != data:
        raise ProofreadError("XHTML cannot be decoded and re-encoded byte-for-byte")
    return canonical, bom, text


def encode_xml_text(text: str, codec: str, bom_hex: str) -> bytes:
    try:
        return bytes.fromhex(bom_hex) + text.encode(codec)
    except UnicodeEncodeError as exc:
        raise ProofreadError(f"replacement is not encodable as {codec}: {exc}") from exc


@dataclass
class Atom:
    char: str
    node_id: str
    raw_start: int
    raw_end: int
    virtual: bool = False


@dataclass
class Block:
    document_href: str
    owner_id: int
    tag: str
    atoms: List[Atom] = field(default_factory=list)
    block_id: str = ""
    global_index: int = -1

    @property
    def text(self) -> str:
        return "".join(atom.char for atom in self.atoms)

    @property
    def raw_start(self) -> int:
        real = [atom.raw_start for atom in self.atoms if not atom.virtual]
        return min(real) if real else 0

    @property
    def raw_end(self) -> int:
        real = [atom.raw_end for atom in self.atoms if not atom.virtual]
        return max(real) if real else 0


@dataclass
class ImageUsage:
    document_href: str
    resource_href: str
    tag: str
    raw_start: int
    raw_end: int
    owner_id: int
    context: str = ""
    usage_id: str = ""


@dataclass
class ParsedDocument:
    href: str
    codec: str
    bom: bytes
    raw_text: str
    blocks: List[Block]
    image_usages: List[ImageUsage]


@dataclass
class Frame:
    name: str
    frame_id: int
    start: int
    start_end: int
    image_usage: Optional[ImageUsage] = None


ENTITY_RE = re.compile(r"&(?:#[0-9]+|#x[0-9a-fA-F]+|[A-Za-z][A-Za-z0-9]+);")
TAG_NAME_RE = re.compile(r"<\s*(/?)\s*([A-Za-z_][\w:.-]*)", re.S)
ATTR_RE = re.compile(r"([A-Za-z_][\w:.-]*)\s*=\s*(['\"])(.*?)\2", re.S)


def markup_end(text: str, start: int) -> int:
    quote = ""
    bracket_depth = 0
    i = start + 1
    while i < len(text):
        char = text[i]
        if quote:
            if char == quote:
                quote = ""
        elif char in {'"', "'"}:
            quote = char
        elif char == "[":
            bracket_depth += 1
        elif char == "]" and bracket_depth:
            bracket_depth -= 1
        elif char == ">" and not bracket_depth:
            return i + 1
        i += 1
    raise ProofreadError(f"unterminated markup starting at character {start}")


def decoded_atoms(raw: str, base: int, node_id: str) -> List[Atom]:
    atoms: List[Atom] = []
    position = 0
    for match in ENTITY_RE.finditer(raw):
        for index, char in enumerate(raw[position:match.start()], position):
            atoms.append(Atom(char, node_id, base + index, base + index + 1))
        token = match.group(0)
        decoded = html.unescape(token)
        if decoded == token:
            for index, char in enumerate(token, match.start()):
                atoms.append(Atom(char, node_id, base + index, base + index + 1))
        else:
            atoms.extend(Atom(char, node_id, base + match.start(), base + match.end()) for char in decoded)
        position = match.end()
    for index, char in enumerate(raw[position:], position):
        atoms.append(Atom(char, node_id, base + index, base + index + 1))
    return atoms


def parse_attributes(markup: str) -> Dict[str, str]:
    values: Dict[str, str] = {}
    for match in ATTR_RE.finditer(markup):
        values[match.group(1).lower()] = html.unescape(match.group(3))
    return values


def scan_xhtml(entry_data: bytes, document_href: str) -> ParsedDocument:
    codec, bom, text = detect_xml_encoding(entry_data)
    stack: List[Frame] = []
    blocks: List[Block] = []
    images: List[ImageUsage] = []
    current_block: Optional[Block] = None
    frame_counter = 0
    node_counter = 0
    saw_body = False

    def in_body() -> bool:
        return any(frame.name == "body" for frame in stack)

    def skip_text() -> bool:
        return any(frame.name in SKIPPED_TEXT_ELEMENTS for frame in stack)

    def owner() -> Tuple[int, str]:
        for frame in reversed(stack):
            if frame.name in BLOCK_ELEMENTS:
                return frame.frame_id, frame.name
        body = next((frame for frame in reversed(stack) if frame.name == "body"), None)
        return ((body.frame_id, "body") if body else (-1, "body"))

    def add_atoms(atoms: Sequence[Atom]) -> None:
        nonlocal current_block
        if not atoms:
            return
        owner_id, tag = owner()
        if current_block is None or current_block.owner_id != owner_id:
            current_block = Block(document_href, owner_id, tag)
            blocks.append(current_block)
        current_block.atoms.extend(atoms)

    def add_text(start: int, end: int) -> None:
        nonlocal node_counter
        if not in_body() or skip_text() or start >= end:
            return
        node_counter += 1
        add_atoms(decoded_atoms(text[start:end], start, f"node-{node_counter:07d}"))

    def finalize_image(frame: Frame, end: int) -> None:
        if frame.image_usage is not None:
            frame.image_usage.raw_end = end
            images.append(frame.image_usage)

    i = 0
    while i < len(text):
        if stack and stack[-1].name in SKIPPED_TEXT_ELEMENTS:
            close = re.search(rf"</\s*{re.escape(stack[-1].name)}\b", text[i:], re.I)
            if not close:
                raise ProofreadError(f"unclosed <{stack[-1].name}> in {document_href}")
            if close.start() > 0:
                i += close.start()
                continue
        next_tag = text.find("<", i)
        if next_tag < 0:
            add_text(i, len(text))
            i = len(text)
            break
        add_text(i, next_tag)
        if text.startswith("<!--", next_tag):
            end = text.find("-->", next_tag + 4)
            if end < 0:
                raise ProofreadError(f"unterminated comment in {document_href}")
            i = end + 3
            continue
        if text.startswith("<![CDATA[", next_tag):
            end = text.find("]]>", next_tag + 9)
            if end < 0:
                raise ProofreadError(f"unterminated CDATA in {document_href}")
            add_text(next_tag + 9, end)
            i = end + 3
            continue
        if text.startswith("<?", next_tag):
            end = text.find("?>", next_tag + 2)
            if end < 0:
                raise ProofreadError(f"unterminated processing instruction in {document_href}")
            i = end + 2
            continue
        end = markup_end(text, next_tag)
        markup = text[next_tag:end]
        match = TAG_NAME_RE.match(markup)
        if not match:
            i = end
            continue
        closing = bool(match.group(1))
        name = match.group(2).split(":")[-1].lower()
        if closing:
            found = next((index for index in range(len(stack) - 1, -1, -1) if stack[index].name == name), -1)
            if found >= 0:
                for frame in reversed(stack[found:]):
                    finalize_image(frame, end)
                del stack[found:]
            if name in BLOCK_ELEMENTS or name == "body":
                current_block = None
            i = end
            continue

        frame_counter += 1
        self_closing = bool(re.search(r"/\s*>$", markup)) or name in VOID_ELEMENTS
        frame = Frame(name, frame_counter, next_tag, end)
        if name == "body":
            saw_body = True
        active_body = in_body() or name == "body"
        if active_body and not skip_text() and name in {"img", "image"}:
            attrs = parse_attributes(markup)
            reference = attrs.get("src") or attrs.get("href") or attrs.get("xlink:href")
            if reference:
                try:
                    resource = resolve_archive_path(document_href, reference)
                except ProofreadError:
                    resource = ""
                if resource:
                    owner_id, _ = owner()
                    frame.image_usage = ImageUsage(document_href, resource, name, next_tag, end, owner_id)
        if name in BLOCK_ELEMENTS or name == "body":
            current_block = None
        if name == "br" and active_body and not skip_text():
            owner_id, tag = owner()
            if current_block is None or current_block.owner_id != owner_id:
                current_block = Block(document_href, owner_id, tag)
                blocks.append(current_block)
            current_block.atoms.append(Atom("\n", f"virtual-br-{next_tag}", next_tag, end, True))
        if self_closing:
            finalize_image(frame, end)
        else:
            stack.append(frame)
        i = end

    if not saw_body:
        raise ProofreadError(f"XHTML {document_href!r} has no body")
    if any(frame.name == "body" for frame in stack):
        raise ProofreadError(f"XHTML {document_href!r} has an unclosed body")

    blocks = [block for block in blocks if block.text.strip()]
    for usage in images:
        same_owner = [block for block in blocks if block.owner_id == usage.owner_id]
        if same_owner:
            usage.context = " ".join(block.text.strip() for block in same_owner if block.text.strip())[:1200]
        else:
            before = [block for block in blocks if block.raw_end <= usage.raw_start]
            after = [block for block in blocks if block.raw_start >= usage.raw_end]
            context = []
            if before:
                context.append(before[-1].text.strip())
            if after:
                context.append(after[0].text.strip())
            usage.context = "\n".join(part for part in context if part)[:1200]
    return ParsedDocument(document_href, codec, bom, text, blocks, images)


def parse_epub_package(zf: zipfile.ZipFile) -> Tuple[str, List[Dict[str, str]], Dict[str, str], bool]:
    names = zf.namelist()
    if len(names) != len(set(names)):
        raise ProofreadError("EPUB contains duplicate ZIP entry names")
    if "META-INF/encryption.xml" in names:
        raise ProofreadError("encrypted/DRM EPUB is not supported")
    try:
        container = ET.fromstring(zf.read("META-INF/container.xml"))
    except (KeyError, ET.ParseError) as exc:
        raise ProofreadError(f"cannot read META-INF/container.xml: {exc}") from exc
    rootfile = next((item.attrib.get("full-path", "") for item in container.iter() if local_name(item.tag) == "rootfile"), "")
    if not rootfile or rootfile not in names:
        raise ProofreadError(f"invalid OPF rootfile path: {rootfile!r}")
    try:
        package = ET.fromstring(zf.read(rootfile))
    except ET.ParseError as exc:
        raise ProofreadError(f"cannot parse OPF {rootfile}: {exc}") from exc
    items: Dict[str, Dict[str, str]] = {}
    media_by_path: Dict[str, str] = {}
    for element in package.iter():
        if local_name(element.tag) != "item":
            continue
        item_id = element.attrib.get("id", "")
        href = element.attrib.get("href", "")
        media = element.attrib.get("media-type", "")
        if not item_id or not href:
            continue
        try:
            path = resolve_archive_path(rootfile, href)
        except ProofreadError:
            path = ""
        items[item_id] = {"href": path, "media_type": media}
        if path:
            media_by_path[path] = media
    spine: List[Dict[str, str]] = []
    for element in package.iter():
        if local_name(element.tag) != "itemref":
            continue
        item = items.get(element.attrib.get("idref", ""), {})
        spine.append({"href": item.get("href", ""), "media_type": item.get("media_type", "")})
    fixed_layout = any(
        local_name(element.tag) == "meta"
        and element.attrib.get("property", "").lower() == "rendition:layout"
        and (element.text or "").strip().lower() == "pre-paginated"
        for element in package.iter()
    )
    return rootfile, spine, media_by_path, fixed_layout


def estimate_unreviewed_characters(data: bytes) -> int:
    try:
        _, _, text = detect_xml_encoding(data)
    except ProofreadError:
        return 0
    visible = re.sub(r"<script\b.*?</script\s*>", "", text, flags=re.I | re.S)
    visible = re.sub(r"<style\b.*?</style\s*>", "", visible, flags=re.I | re.S)
    visible = re.sub(r"<[^>]+>", "", visible)
    return len(html.unescape(visible).strip())


def build_batches(blocks: Sequence[Block], target_chars: int, overlap_chars: int) -> List[Dict[str, Any]]:
    if not blocks:
        return []
    batches: List[Dict[str, Any]] = []
    start = 0
    while start < len(blocks):
        end = start
        size = 0
        while end < len(blocks) and (end == start or size < target_chars):
            size += len(blocks[end].text) + 1
            end += 1
        context_start = start
        context_size = 0
        while context_start > 0 and context_size < overlap_chars:
            context_start -= 1
            context_size += len(blocks[context_start].text) + 1
        context_end = end
        context_size = 0
        while context_end < len(blocks) and context_size < overlap_chars:
            context_size += len(blocks[context_end].text) + 1
            context_end += 1
        batch_id = f"batch-{len(batches) + 1:05d}"
        batches.append({
            "batch_id": batch_id,
            "core_start_index": start,
            "core_end_index": end,
            "context_start_index": context_start,
            "context_end_index": context_end,
            "core_start_block": blocks[start].block_id,
            "core_end_block": blocks[end - 1].block_id,
            "status": "pending",
        })
        start = end
    return batches


def assign_block_ids(documents: Sequence[ParsedDocument]) -> List[Block]:
    blocks: List[Block] = []
    for document in documents:
        for block in document.blocks:
            block.global_index = len(blocks)
            block.block_id = f"block-{len(blocks) + 1:07d}"
            blocks.append(block)
    return blocks


def occurrence_positions(haystack: str, needle: str) -> List[int]:
    if not needle:
        return []
    positions: List[int] = []
    start = 0
    while True:
        found = haystack.find(needle, start)
        if found < 0:
            return positions
        positions.append(found)
        start = found + len(needle)


def xml_escape_text(value: str) -> str:
    return value.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def source_manifest(state_dir: Path) -> Tuple[Dict[str, Any], Path, bytes]:
    manifest = read_json(state_dir / "manifest.json")
    if manifest.get("version") != STATE_VERSION:
        raise ProofreadError(f"unsupported state version: {manifest.get('version')}")
    source = Path(manifest["source_path"])
    if not source.is_file():
        raise ProofreadError(f"source EPUB is missing: {source}")
    data = source.read_bytes()
    if sha256_bytes(data) != manifest["source_sha256"]:
        raise ProofreadError("source EPUB hash changed; refuse to continue")
    return manifest, source, data


def load_runtime(state_dir: Path) -> Tuple[Dict[str, Any], List[ParsedDocument], List[Block]]:
    manifest, source, _ = source_manifest(state_dir)
    records = read_jsonl(state_dir / "documents.jsonl")
    parsed: List[ParsedDocument] = []
    with zipfile.ZipFile(source) as zf:
        for record in records:
            if record.get("status") != "parsed":
                continue
            href = str(record["href"])
            try:
                data = zf.read(href)
            except KeyError as exc:
                raise ProofreadError(f"parsed spine entry disappeared: {href}") from exc
            if sha256_bytes(data) != record["sha256"]:
                raise ProofreadError(f"spine entry changed: {href}")
            document = scan_xhtml(data, href)
            if document.codec != record["codec"] or document.bom.hex() != record["bom_hex"]:
                raise ProofreadError(f"spine encoding changed: {href}")
            parsed.append(document)
    blocks = assign_block_ids(parsed)
    if len(blocks) != manifest["block_count"]:
        raise ProofreadError("reconstructed block count differs from manifest")
    return manifest, parsed, blocks


def block_map(blocks: Sequence[Block]) -> Dict[str, Block]:
    return {block.block_id: block for block in blocks}


def image_maps(state_dir: Path) -> Tuple[Dict[str, Dict[str, Any]], Dict[str, Tuple[Dict[str, Any], Dict[str, Any]]]]:
    images = {item["image_id"]: item for item in read_jsonl(state_dir / "images.jsonl")}
    usages: Dict[str, Tuple[Dict[str, Any], Dict[str, Any]]] = {}
    for image in images.values():
        for usage in image.get("usages", []):
            usages[usage["image_usage_id"]] = (image, usage)
    return images, usages


def clone_zipinfo(info: zipfile.ZipInfo) -> zipfile.ZipInfo:
    clone = zipfile.ZipInfo(info.filename, date_time=info.date_time)
    clone.compress_type = info.compress_type
    clone.comment = info.comment
    clone.extra = info.extra
    clone.create_system = info.create_system
    clone.create_version = info.create_version
    clone.extract_version = info.extract_version
    clone.reserved = info.reserved
    clone.flag_bits = info.flag_bits
    clone.volume = info.volume
    clone.internal_attr = info.internal_attr
    clone.external_attr = info.external_attr
    return clone


def media_type_for(path: str, declared: str = "") -> str:
    return declared or mimetypes.guess_type(path)[0] or "application/octet-stream"
