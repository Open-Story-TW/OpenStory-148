import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("simple_npcs", Path(__file__).with_name("migrate-simple-npcs.py"))
converter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(converter)


class LiteralWrapperTests(unittest.TestCase):
    def convert(self, source):
        return converter.convert(source.encode("utf-8"), "npc/123.lua")

    def test_literal_text_and_comments_preserved(self):
        result = self.convert(r'''/* ignored */ function start() {
          cm.sendOk("詞句 // /* ; \"" + '\r\n\u4e16\u754c'); cm.dispose();
        }''')
        self.assertEqual(result[1]["text"], '詞句 // /* ; "\r\n世界')
        self.assertIn(r'\r\n世界', result[0])
        self.assertIn(r'\"', result[0])

    def test_action_initializer_and_warp(self):
        result = self.convert('''var status=-1; function start(){status=-1;action(1,0,0);}
          function action(mode,type,selection){cm.warp(10000,2);cm.dispose();}''')
        self.assertEqual(result[1]["map_id"], 10000)
        self.assertEqual(result[1]["portal_id"], 2)

    def test_dynamic_or_extra_behavior_rejected(self):
        for body in [
            'cm.sendOk("one"+player);cm.dispose();',
            'if(mode==1){cm.warp(10000);cm.dispose();}',
            'cm.sendOk("one");cm.gainItem(2000000,1);cm.dispose();',
            'cm.warp(10000,256);cm.dispose();',
            'cm.sendOk("one");cm.dispose();}function action(mode,type,selection){cm.dispose();',
            'cm.sendOk("cm.warp(10000)");cm.dispose();}function exploit(){',
        ]:
            with self.subTest(body=body):
                self.assertIsNone(self.convert('function start(){' + body + '}'))

    def test_unicode_surrogate_pair_and_invalid_escape(self):
        result = self.convert(r'function start(){cm.sendOk("\uD83C\uDF41");cm.dispose();}')
        self.assertEqual(result[1]["text"], '🍁')
        for text in [r'\uD800', r'\1', r'\xGG', r'\q']:
            with self.assertRaises((ValueError, UnicodeError)):
                self.convert('function start(){cm.sendOk("' + text + '");cm.dispose();}')


if __name__ == "__main__":
    unittest.main()
