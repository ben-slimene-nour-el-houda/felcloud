"""
bench.lib.prober — High-frequency HTTP prober for continuous failover and reload monitoring.
"""

import threading
import time
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional
import requests

class ContinuousProber:
    def __init__(self, target_url: str, host_header: str, interval_seconds: float = 0.1, timeout: float = 1.0):
        self.target_url = target_url
        self.host_header = host_header
        self.interval = interval_seconds
        self.timeout = timeout
        self.running = False
        self.thread: Optional[threading.Thread] = None
        self.samples: List[Dict[str, Any]] = []
        self._lock = threading.Lock()

    def _probe_loop(self):
        session = requests.Session()
        headers = {"Host": self.host_header, "User-Agent": "FelCloud-Failover-Prober/1.0"}

        while self.running:
            t0 = time.perf_counter()
            iso_ts = datetime.now(timezone.utc).isoformat()
            sample: Dict[str, Any] = {
                "timestamp": iso_ts,
                "perf_ts": t0,
                "target": self.target_url,
                "host_header": self.host_header,
                "status_code": 0,
                "latency_ms": 0.0,
                "success": False,
                "gateway_id": "",
                "error": ""
            }

            try:
                resp = session.get(self.target_url, headers=headers, timeout=self.timeout, verify=False)
                latency = (time.perf_counter() - t0) * 1000.0
                sample["latency_ms"] = round(latency, 2)
                sample["status_code"] = resp.status_code
                sample["gateway_id"] = resp.headers.get("X-Gateway-ID", "")
                if resp.status_code in (200, 201):
                    sample["success"] = True
                else:
                    sample["error"] = f"HTTP {resp.status_code}"
            except Exception as e:
                latency = (time.perf_counter() - t0) * 1000.0
                sample["latency_ms"] = round(latency, 2)
                sample["error"] = type(e).__name__

            with self._lock:
                self.samples.append(sample)

            sleep_time = self.interval - (time.perf_counter() - t0)
            if sleep_time > 0:
                time.sleep(sleep_time)

    def start(self):
        with self._lock:
            self.running = True
            self.samples.clear()
        self.thread = threading.Thread(target=self._probe_loop, daemon=True)
        self.thread.start()

    def stop(self) -> List[Dict[str, Any]]:
        self.running = False
        if self.thread and self.thread.is_alive():
            self.thread.join(timeout=3.0)
        with self._lock:
            return list(self.samples)

    def get_samples(self) -> List[Dict[str, Any]]:
        with self._lock:
            return list(self.samples)
