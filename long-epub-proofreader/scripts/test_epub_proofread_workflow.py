#!/usr/bin/env python3

from __future__ import annotations

import base64
import json
import os
import subprocess
import sys
import tempfile
import unittest
import zipfile
from pathlib import Path


SCRIPTS = Path(__file__).resolve().parent
STATE_TOOL = SCRIPTS / "proofread_epub.py"
APPLIER = SCRIPTS / "apply_reviewed_edits.py"
sys.path.insert(0, str(SCRIPTS))

from apply_reviewed_edits import apply_patches, candidates_logically_conflict, text_candidate_patches
from epub_core import scan_xhtml

PNG = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
)


class EPUBWorkflowTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self.source = self.root / "source.epub"
        self.state = self.root / ".proofread-epub"

    def tearDown(self):
        self.temporary.cleanup()

    def run_tool(self, script: Path, *arguments: object, check: bool = True) -> subprocess.CompletedProcess[str]:
        env = dict(os.environ)
        env["PYTHONDONTWRITEBYTECODE"] = "1"
        return subprocess.run(
            [sys.executable, str(script), *map(str, arguments)],
            text=True,
            capture_output=True,
            check=check,
            env=env,
        )

    def write_jsonl(self, name: str, records: list[dict]) -> Path:
        path = self.root / name
        path.write_text("".join(json.dumps(item, ensure_ascii=False) + "\n" for item in records), encoding="utf-8")
        return path

    def write_epub(self, *, include_unsupported: bool = False, encrypted: bool = False) -> str:
        chapter = """<?xml version='1.0' encoding='utf-8'?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>测试</title><style>.bold {{ font-weight: bold; }}</style></head><body>
<p id="keep" title="属性里的噗之以鼻">他对此噗<span class="bold">之</span>以鼻 &amp; 错<span>别</span>字。</p>
<p>甲<span class="bold">乙</span></p>
<p class="ad"><img src="images/ad.png"/></p>
<p class="mixed"><img src="images/mixed.png"/></p>
<p class="content"><img src="images/content.png"/></p>
</body></html>
"""
        manifest_extra = '<item id="bad" href="bad.pdf" media-type="application/pdf"/>' if include_unsupported else ""
        spine_extra = '<itemref idref="bad"/>' if include_unsupported else ""
        opf = f"""<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
<metadata/><manifest>
<item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>
<item id="ad" href="images/ad.png" media-type="image/png"/>
<item id="mixed" href="images/mixed.png" media-type="image/png"/>
<item id="content" href="images/content.png" media-type="image/png"/>
{manifest_extra}
</manifest><spine>{spine_extra}<itemref idref="chapter"/></spine></package>
"""
        container = """<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
<rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>"""
        with zipfile.ZipFile(self.source, "w") as zf:
            mimetype = zipfile.ZipInfo("mimetype", date_time=(2020, 1, 2, 3, 4, 6))
            mimetype.compress_type = zipfile.ZIP_STORED
            mimetype.external_attr = 0o100644 << 16
            zf.writestr(mimetype, b"application/epub+zip")
            for name, data in (
                ("META-INF/container.xml", container.encode()),
                ("content.opf", opf.encode()),
                ("chapter.xhtml", chapter.encode()),
                ("images/ad.png", PNG),
                ("images/mixed.png", PNG),
                ("images/content.png", PNG),
            ):
                info = zipfile.ZipInfo(name, date_time=(2020, 1, 2, 3, 4, 6))
                info.compress_type = zipfile.ZIP_DEFLATED
                info.external_attr = 0o100644 << 16
                zf.writestr(info, data)
            if include_unsupported:
                zf.writestr("bad.pdf", b"not a pdf")
            if encrypted:
                zf.writestr("META-INF/encryption.xml", b"<encryption/>")
        return chapter

    def initialize(self, **kwargs: bool) -> str:
        chapter = self.write_epub(**kwargs)
        self.run_tool(
            STATE_TOOL,
            "init",
            "--input",
            self.source,
            "--state-dir",
            self.state,
            "--batch-chars",
            "50",
            "--overlap-chars",
            "10",
        )
        return chapter

    def complete_all_batches(self) -> None:
        for batch in self.read_jsonl(self.state / "batches.jsonl"):
            self.run_tool(
                STATE_TOOL,
                "complete-batch",
                "--state-dir",
                self.state,
                "--batch-id",
                batch["batch_id"],
                "--reviewer",
                "first-agent",
                "--glossary-version",
                "1",
            )

    def complete_all_images(self) -> None:
        for image in self.read_jsonl(self.state / "images.jsonl"):
            if image["status"] == "pending":
                self.run_tool(
                    STATE_TOOL,
                    "complete-image",
                    "--state-dir",
                    self.state,
                    "--image-id",
                    image["image_id"],
                    "--reviewer",
                    "image-agent",
                )

    @staticmethod
    def read_jsonl(path: Path) -> list[dict]:
        return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]

    def test_cross_node_text_entities_empty_nodes_images_and_reproducibility(self) -> None:
        original_chapter = self.initialize()
        batch = self.read_jsonl(self.state / "batches.jsonl")[0]
        shown = json.loads(self.run_tool(
            STATE_TOOL, "show-batch", "--state-dir", self.state, "--batch-id", batch["batch_id"]
        ).stdout)
        first_line = next(line for line in shown["text"].splitlines() if "噗" in line)
        first_block = first_line.split("\t", 1)[0]
        second_line = next(line for line in shown["text"].splitlines() if "甲乙" in line)
        second_block = second_line.split("\t", 1)[0]

        images = self.read_jsonl(self.state / "images.jsonl")
        by_href = {item["resource_href"]: item for item in images}
        candidates_input = [
            {"batch_id": batch["batch_id"], "block_id": first_block, "original": "噗之以鼻", "proposed": "嗤之以鼻", "category": "wrong_character", "confidence": "high", "reason": "固定成语", "context": "他对此噗之以鼻。"},
            {"batch_id": batch["batch_id"], "block_id": first_block, "original": "&", "proposed": "和", "category": "wrong_character", "confidence": "high", "reason": "测试实体映射", "context": "以鼻 & 错别字"},
            {"batch_id": batch["batch_id"], "block_id": first_block, "original": "错别字", "proposed": "", "category": "duplicate_character", "confidence": "high", "reason": "测试跨节点删除", "context": "错别字"},
            {"batch_id": batch["batch_id"], "block_id": second_block, "original": "甲乙", "proposed": "甲的乙", "category": "missing_character", "confidence": "high", "reason": "测试边界插入", "context": "甲乙"},
            {"image_usage_id": by_href["images/ad.png"]["usages"][0]["image_usage_id"], "category": "external_ad_image", "confidence": "high", "proposed": "remove_reference", "reason": "纯广告图片"},
            {"image_usage_id": by_href["images/mixed.png"]["usages"][0]["image_usage_id"], "category": "mixed_ad_image", "confidence": "review", "proposed": "keep", "reason": "正文与广告混合"},
        ]
        self.run_tool(
            STATE_TOOL,
            "import-candidates",
            "--state-dir",
            self.state,
            "--file",
            self.write_jsonl("candidates.jsonl", candidates_input),
        )
        candidates = self.read_jsonl(self.state / "candidates.jsonl")
        reviews = []
        for candidate in candidates:
            reviews.append({
                "candidate_id": candidate["candidate_id"],
                "verdict": "review" if candidate["category"] == "mixed_ad_image" else "high",
                "proposed": candidate["proposed"],
                "reason": "隔离复核",
                "review_mode": "independent_subagent",
                "reviewer": "verify-agent",
            })
        self.run_tool(
            STATE_TOOL,
            "import-reviews",
            "--state-dir",
            self.state,
            "--file",
            self.write_jsonl("reviews.jsonl", reviews),
        )
        mixed = next(item for item in candidates if item["category"] == "mixed_ad_image")
        self.run_tool(
            STATE_TOOL,
            "import-decisions",
            "--state-dir",
            self.state,
            "--file",
            self.write_jsonl("decisions.jsonl", [{"candidate_id": mixed["candidate_id"], "action": "accept", "proposed": "keep", "reason": "保留混合图片"}]),
        )
        self.complete_all_batches()
        self.complete_all_images()
        status = json.loads(self.run_tool(STATE_TOOL, "status", "--state-dir", self.state).stdout)
        self.assertTrue(status["ready_to_apply"])
        self.assertEqual("full", status["completion_scope"])

        output1 = self.root / "revised-1.epub"
        output2 = self.root / "revised-2.epub"
        report = self.root / "report.md"
        audit = self.root / "audit.jsonl"
        for output in (output1, output2):
            self.run_tool(
                APPLIER,
                "--state-dir",
                self.state,
                "--output",
                output,
                "--report",
                report,
                "--audit",
                audit,
            )
        self.assertEqual(output1.read_bytes(), output2.read_bytes())
        with zipfile.ZipFile(self.source) as before, zipfile.ZipFile(output1) as after:
            revised = after.read("chapter.xhtml").decode()
            self.assertIn('title="属性里的噗之以鼻"', revised)
            self.assertIn("他对此嗤<span class=\"bold\">之</span>以鼻 和 <span></span>。", revised)
            self.assertIn("甲的<span class=\"bold\">乙</span>", revised)
            self.assertIn('<p class="ad"></p>', revised)
            self.assertIn('<p class="mixed"><img src="images/mixed.png"/></p>', revised)
            for name in before.namelist():
                if name != "chapter.xhtml":
                    self.assertEqual(before.read(name), after.read(name), name)
            self.assertEqual(original_chapter.encode(), before.read("chapter.xhtml"))
        audit_records = self.read_jsonl(audit)
        self.assertEqual(5, sum(item["final_status"].startswith("approved_") and item["final_status"] != "approved_no_change" for item in audit_records))
        self.assertIn("全文完成", report.read_text(encoding="utf-8"))

    def test_skipped_spine_document_produces_partial_completion(self) -> None:
        self.initialize(include_unsupported=True)
        self.complete_all_batches()
        self.complete_all_images()
        status = json.loads(self.run_tool(STATE_TOOL, "status", "--state-dir", self.state).stdout)
        self.assertTrue(status["ready_to_apply"])
        self.assertEqual("partial", status["completion_scope"])
        documents = self.read_jsonl(self.state / "documents.jsonl")
        skipped = [item for item in documents if item["status"] == "skipped"]
        self.assertEqual(1, len(skipped))
        self.assertIn("unsupported spine media type", skipped[0]["reason"])

    def test_encrypted_epub_is_rejected(self) -> None:
        self.write_epub(encrypted=True)
        result = self.run_tool(
            STATE_TOOL,
            "init",
            "--input",
            self.source,
            "--state-dir",
            self.state,
            check=False,
        )
        self.assertEqual(2, result.returncode)
        self.assertIn("encrypted/DRM EPUB is not supported", result.stderr)
        self.assertFalse(self.state.exists())

    def test_insertion_after_br_anchors_to_following_text_not_before_break(self) -> None:
        source = b'<html xmlns="http://www.w3.org/1999/xhtml"><body><p>\xe5\x89\x8d<br/>\xe7\x94\xb2\xe4\xb9\x99</p></body></html>'
        document = scan_xhtml(source, "chapter.xhtml")
        block = document.blocks[0]
        self.assertEqual("前\n甲乙", block.text)
        candidate = {
            "candidate_id": "cand-test",
            "start_offset": 2,
            "end_offset": 4,
            "original": "甲乙",
        }
        patches = text_candidate_patches(block, candidate, "的甲乙")
        revised = apply_patches(document.raw_text, patches, document.href)
        self.assertIn("<br/>的甲乙", revised)
        self.assertNotIn("的<br/>", revised)

    def test_overlapping_logical_ranges_conflict_even_when_raw_changes_differ(self) -> None:
        left = {"kind": "text", "block_id": "block-1", "start_offset": 0, "end_offset": 4}
        right = {"kind": "text", "block_id": "block-1", "start_offset": 1, "end_offset": 3}
        self.assertTrue(candidates_logically_conflict(left, right))


if __name__ == "__main__":
    unittest.main()
