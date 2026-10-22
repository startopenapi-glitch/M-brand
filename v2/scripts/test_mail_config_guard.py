import importlib.util
from pathlib import Path
import tempfile
import unittest

class MailConfigTests(unittest.TestCase):
 def setUp(self):
  spec=importlib.util.spec_from_file_location('mail_config_guard',Path(__file__).with_name('mail_config_guard.py'))
  self.mod=importlib.util.module_from_spec(spec);spec.loader.exec_module(self.mod)
 def test_plain_sender_allowed(self):
  self.mod.validate_senders({'RESEND_FROM':'mail@example.test'})
 def test_display_name_rejected_without_exposing_address(self):
  with self.assertRaises(RuntimeError) as error:self.mod.validate_senders({'RESEND_FROM':'Brand <private@example.test>'})
  self.assertNotIn('private@example.test',str(error.exception))
 def test_empty_sender_rejected(self):
  with self.assertRaises(RuntimeError):self.mod.validate_senders({})
 def test_bad_fallback_rejected_even_with_valid_primary(self):
  with self.assertRaises(RuntimeError):self.mod.validate_senders({'ZEROBRAND_SMTP_FROM':'mail@example.test','RESEND_FROM':'Brand <mail@example.test>'})
 def test_multiple_recipients_and_header_injection_rejected(self):
  for value in ['a@example.test,b@example.test','a@example.test\r\nBcc: b@example.test','not-an-address']:
   with self.subTest(value=value),self.assertRaises(RuntimeError):self.mod.validate_senders({'RESEND_FROM':value})
 def test_quoted_env_and_unrelated_keys_not_executed(self):
  with tempfile.TemporaryDirectory() as directory:
   p=Path(directory)/'.env';marker=Path(directory)/'must-not-exist'
   p.write_text('RESEND_FROM="mail@example.test"\nRESEND_SMTP_PASS=$(touch '+str(marker)+')\n')
   self.mod.validate_file(p);self.assertFalse(marker.exists())
 def test_malformed_config_rejected_without_value(self):
  with tempfile.TemporaryDirectory() as directory:
   p=Path(directory)/'.env';p.write_text('RESEND_FROM="private@example.test\n')
   with self.assertRaises(RuntimeError) as error:self.mod.validate_file(p)
   self.assertNotIn('private@example.test',str(error.exception))
 def test_missing_config_rejected(self):
  with self.assertRaises(RuntimeError):self.mod.validate_file(Path('/does-not-exist-0brand-config'))

if __name__=='__main__':unittest.main()
