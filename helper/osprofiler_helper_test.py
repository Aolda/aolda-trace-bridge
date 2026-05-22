import unittest

import osprofiler_helper


class StatefulEngine:
    def __init__(self):
        self.result = {"old": "report"}
        self.started_at = 1
        self.finished_at = 2
        self.last_started_at = 3

    def get_report(self, base_id):
        if self.result != {}:
            raise AssertionError("result was not reset")
        if self.started_at is not None:
            raise AssertionError("started_at was not reset")
        if self.finished_at is not None:
            raise AssertionError("finished_at was not reset")
        if self.last_started_at is not None:
            raise AssertionError("last_started_at was not reset")
        return {"info": {"name": "total"}, "children": [], "stats": {}}


class ScanDB:
    def __init__(self):
        self.scan_calls = []

    def scan(self, cursor=0, match=None, count=None):
        self.scan_calls.append((cursor, match, count))
        return 9, [b"osprofiler_opt:base-1", b"osprofiler_error:base-ignored"]

    def lindex(self, key, index):
        self.lindex_call = (key, index)
        return b'{"timestamp":"2026-04-25T15:30:00.993142"}'


class ScanEngine:
    namespace_opt = "osprofiler_opt:"

    def __init__(self):
        self.db = ScanDB()

    def list_traces(self, fields=None):
        raise AssertionError("list_traces should not be called for Redis page scans")


class HelperTest(unittest.TestCase):
    def tearDown(self):
        osprofiler_helper._ENGINE = None

    def test_get_report_resets_osprofiler_driver_state(self):
        osprofiler_helper._ENGINE = StatefulEngine()

        response = osprofiler_helper._handle({
            "id": "1",
            "method": "get_report",
            "base_id": "base-1",
        })

        self.assertTrue(response["ok"], response)
        self.assertEqual(response["report"]["info"]["name"], "total")

    def test_list_traces_uses_bounded_redis_scan_page(self):
        engine = ScanEngine()
        osprofiler_helper._ENGINE = engine

        response = osprofiler_helper._handle({
            "id": "1",
            "method": "list_traces",
            "cursor": "7",
            "count": 25,
        })

        self.assertTrue(response["ok"], response)
        self.assertEqual(response["next_cursor"], "9")
        self.assertEqual(response["traces"], [{
            "base_id": "base-1",
            "timestamp": "2026-04-25T15:30:00.993142",
        }])
        self.assertEqual(engine.db.scan_calls, [(7, "osprofiler_opt:*", 25)])


if __name__ == "__main__":
    unittest.main()
