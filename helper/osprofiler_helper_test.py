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


if __name__ == "__main__":
    unittest.main()
