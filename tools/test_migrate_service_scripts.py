import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("services", Path(__file__).with_name("migrate-service-scripts.py"))
converter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(converter)


class NamedPortalTests(unittest.TestCase):
    def test_literal_name_preserved(self):
        result = converter.convert_portal(b'function enter(pi) { pi.warp(10000, "out00"); return true; }', "portal/test.js")
        self.assertEqual(result[1]["portal_name"], "out00")
        self.assertIn('portal:warp(10000, "out00")', result[0])

    def test_unknown_syntax_and_literal_changes_rejected(self):
        for source in [
            'function enter(pi){pi.warp(10000,"out 00");}',
            'function enter(pi){pi.warp(10000,"out00\');}',
            'function enter(pi){pi.warp(10000,"out"+"00");}',
            'function enter(pi){pi.warp(10000,name);}',
            'function enter(pi){pi.warp(010000,0);}',
            'function enter(pi){pi.warp(010000,"out00");}',
            'function enter(pi){pi.gainItem(2000000,1);pi.warp(10000,"out00");}',
            'function enter(pi){if (true) pi.warp(10000,"out00");}',
        ]:
            self.assertIsNone(converter.convert_portal(source.encode(), "portal/test.js"), source)


if __name__ == "__main__":
    unittest.main()
