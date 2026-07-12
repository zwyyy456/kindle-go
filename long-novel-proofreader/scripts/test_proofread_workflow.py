#!/usr/bin/env python3

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPTS = Path(__file__).resolve().parent
STATE_TOOL = SCRIPTS / "proofread_txt.py"
APPLIER = SCRIPTS / "apply_reviewed_edits.py"
AD = "更多精校小说尽在知轩藏书下载：https://zxcs.zip/"


class WorkflowTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self.source = self.root / "source.txt"
        self.state = self.root / ".proofread"

    def tearDown(self):
        self.temporary.cleanup()

    def run_tool(self, script, *arguments, check=True):
        return subprocess.run(
            [sys.executable, str(script), *map(str, arguments)],
            text=True,
            capture_output=True,
            check=check,
        )

    def write_jsonl(self, name, records):
        path = self.root / name
        path.write_text("".join(json.dumps(item, ensure_ascii=False) + "\n" for item in records), encoding="utf-8")
        return path

    def initialize(self, text):
        self.source.write_bytes(text.encode("utf-8"))
        self.run_tool(
            STATE_TOOL,
            "init",
            "--input",
            self.source,
            "--state-dir",
            self.state,
            "--batch-chars",
            "40",
            "--overlap-chars",
            "10",
        )

    def complete_all_batches(self):
        batches = [json.loads(line) for line in (self.state / "batches.jsonl").read_text().splitlines()]
        for batch in batches:
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

    def batch_for_line(self, line_no):
        batches = [json.loads(line) for line in (self.state / "batches.jsonl").read_text().splitlines()]
        return next(
            batch["batch_id"]
            for batch in batches
            if batch["core_start_line"] <= line_no <= batch["core_end_line"]
        )

    def test_every_character_is_in_exactly_one_core_batch(self):
        self.initialize("第一章 开始\r\n" + "一段正文。\r\n" * 20)
        manifest = json.loads((self.state / "manifest.json").read_text())
        batches = [json.loads(line) for line in (self.state / "batches.jsonl").read_text().splitlines()]
        self.assertEqual(0, batches[0]["core_start_char"])
        self.assertEqual(manifest["character_count"], batches[-1]["core_end_char"])
        for left, right in zip(batches, batches[1:]):
            self.assertEqual(left["core_end_char"], right["core_start_char"])
        self.assertEqual("crlf", manifest["newline"])

    def test_only_doubly_confirmed_candidate_is_applied(self):
        text = f"第一章 开始\n他说：‘{AD}’这是剧情。\n{AD}\n他对此噗之以鼻。\n"
        self.initialize(text)
        candidates_file = self.write_jsonl(
            "candidates-input.jsonl",
            [
                {
                    "batch_id": self.batch_for_line(3),
                    "line": 3,
                    "original": AD,
                    "proposed": "",
                    "category": "external_ad",
                    "confidence": "high",
                    "reason": "独立站外横幅",
                    "context": AD,
                },
                {
                    "batch_id": self.batch_for_line(4),
                    "line": 4,
                    "original": "噗之以鼻",
                    "proposed": "嗤之以鼻",
                    "category": "wrong_character",
                    "confidence": "high",
                    "reason": "固定成语",
                    "context": "他对此噗之以鼻。",
                },
            ],
        )
        self.run_tool(
            STATE_TOOL,
            "import-candidates",
            "--state-dir",
            self.state,
            "--file",
            candidates_file,
        )
        normalized = [json.loads(line) for line in (self.state / "candidates.jsonl").read_text().splitlines()]
        review_payload = self.run_tool(
            STATE_TOOL,
            "show-review",
            "--state-dir",
            self.state,
            "--candidate-id",
            normalized[0]["candidate_id"],
        ).stdout
        self.assertNotIn('"confidence"', review_payload)
        self.assertNotIn('"reason"', review_payload)
        self.assertNotIn("固定成语", review_payload)
        reviews_file = self.write_jsonl(
            "reviews-input.jsonl",
            [
                {
                    "candidate_id": item["candidate_id"],
                    "verdict": "high",
                    "proposed": item["proposed"],
                    "reason": "独立复核确认",
                    "review_mode": "independent_subagent",
                    "reviewer": "verify-agent",
                }
                for item in normalized
            ],
        )
        self.run_tool(
            STATE_TOOL,
            "import-reviews",
            "--state-dir",
            self.state,
            "--file",
            reviews_file,
        )
        self.complete_all_batches()
        output = self.root / "revised.txt"
        report = self.root / "report.md"
        audit = self.root / "audit.jsonl"
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
        revised = output.read_text(encoding="utf-8")
        self.assertIn(f"他说：‘{AD}’这是剧情。", revised)
        self.assertEqual(1, revised.count(AD))
        self.assertIn("嗤之以鼻", revised)
        self.assertEqual(text, self.source.read_text(encoding="utf-8"))

    def test_changed_source_blocks_resume(self):
        self.initialize("第一章 开始\n正文。\n")
        self.source.write_text("第一章 开始\n正文被改变。\n", encoding="utf-8")
        result = self.run_tool(STATE_TOOL, "status", "--state-dir", self.state, check=False)
        self.assertEqual(2, result.returncode)
        self.assertIn("source hash changed", result.stderr)


if __name__ == "__main__":
    unittest.main()
