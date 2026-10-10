import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


SCRIPT = Path(__file__).parent / "benchmark-report.py"
SPEC = importlib.util.spec_from_file_location("benchmark_report", SCRIPT)
REPORT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(REPORT)


class BenchmarkReportTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name) / "input.json"

    def write_json(self, value):
        self.path.write_text(json.dumps(value), encoding="utf-8")
        return self.path

    def stress(self, mode="live"):
        return {
            "mode": mode, "date_utc": "2026-10-10T00:00:00Z", "git_commit": "abc",
            "config": {"teams": 1, "concurrency": 1}, "teams": [], "aggregates": {},
        }

    def test_mock_input_rejected_without_allow_mock(self):
        path = self.write_json(self.stress("mock"))
        with self.assertRaisesRegex(REPORT.ReportError, "mock results"):
            REPORT.load_input(path, "stress", False)
        self.assertEqual(REPORT.load_input(path, "stress", True)["mode"], "mock")

    def test_missing_failover_renders_not_measured(self):
        stress = self.stress()
        stress["_source_path"] = "fixture"
        text, _ = REPORT.generate_report(stress, None, None, False)
        self.assertIn("Gateway failover", text)
        self.assertIn("NOT MEASURED — no failover results input.", text)

    def test_percentile_math_uses_nearest_rank(self):
        self.assertEqual(REPORT.percentile(list(range(1, 11)), 0.50), 5)
        self.assertEqual(REPORT.percentile(list(range(1, 21)), 0.95), 19)

    def test_small_sample_warns_and_uses_max_instead_of_p95(self):
        result = REPORT.stats([1, 2, 3, 4, 5, 6, 7, 8, 9])
        self.assertEqual(result["p95"], None)
        self.assertEqual(result["max"], 9)
        self.assertIn("n=9", result["warning"])
        text = "\n".join(REPORT.stat_table([("latency", result)], False))
        self.assertIn("n<20; see max", text)
        self.assertIn("WARNING: sample size n=9 is below 10", text)


if __name__ == "__main__":
    unittest.main()
