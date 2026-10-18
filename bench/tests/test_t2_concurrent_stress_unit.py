"""Unit checks for T2 response parsing; no network calls are made."""
import unittest

from bench.lib.api_client import TimedResponse
from bench.tests.t2_concurrent_stress import parse_api_response


class TimedResponseParsingTest(unittest.TestCase):
    def test_parses_harness_timed_response_data(self):
        response = TimedResponse(
            status_code=201,
            data={"sandbox_id": "bench-t2-check", "fixed_ip": "192.0.2.9"},
            elapsed_seconds=0.25,
            started_utc="2026-10-18T00:00:00.000Z",
            finished_utc="2026-10-18T00:00:00.250Z",
            started_perf_counter=1.0,
            finished_perf_counter=1.25,
        )
        self.assertEqual(parse_api_response(response), (201, response.data))


if __name__ == "__main__":
    unittest.main()
