"""Unit checks for T2 response parsing; no network calls are made."""
import unittest

from bench.lib.api_client import TimedResponse
from bench.tests.t2_concurrent_stress import (
    identity_matches,
    neutron_port_ips,
    parse_api_response,
)


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

    def test_fixed_ip_match_uses_word_boundaries(self):
        self.assertTrue(identity_matches("FelCloud sandbox ready (10.20.30.4)", "10.20.30.4"))
        self.assertFalse(identity_matches("FelCloud sandbox ready (10.20.30.41)", "10.20.30.4"))
        self.assertFalse(identity_matches("bench-t2-sandbox-id", "10.20.30.4"))

    def test_neutron_names_map_ports_back_to_sandbox_ids(self):
        ports = [
            {"Name": "sandbox-bench-t2-a", "Fixed IP Addresses": "ip_address='10.20.30.37'"},
            {"Name": "sandbox-bench-t2-b", "Fixed IP Addresses": "ip_address='10.20.30.15'"},
            {"Name": "team1", "Fixed IP Addresses": "ip_address='10.20.30.70'"},
        ]
        self.assertEqual(neutron_port_ips(ports, ["bench-t2-a", "bench-t2-b"]),
                         {"bench-t2-a": "10.20.30.37", "bench-t2-b": "10.20.30.15"})


if __name__ == "__main__":
    unittest.main()
