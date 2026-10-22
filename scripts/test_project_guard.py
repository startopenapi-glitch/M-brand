import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('project_guard', Path(__file__).with_name('project_guard.py'))
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)

class ProjectGuardTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(); self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / 'checkout'; self.root.mkdir()
        (self.root / 'PROJECT.json').write_text('{"project":"m-brand"}')
        (self.root / 'v2/server').mkdir(parents=True)
        (self.root / 'v2/server/go.mod').write_text('module github.com/startopenapi-glitch/M-brand/v2/server\n')

    def test_independent_installation_allowed(self):
        guard.check(self.root)

    def test_private_module_rejected(self):
        (self.root / 'v2/server/go.mod').write_text('module 0brand/v2\n')
        with self.assertRaisesRegex(RuntimeError, 'identity'): guard.check(self.root)

    def test_private_project_marker_rejected(self):
        (self.root / 'PROJECT.json').write_text('{"project":"0brand"}')
        with self.assertRaisesRegex(RuntimeError, 'identity'): guard.check(self.root)

    def test_leftover_operator_runtime_rejected(self):
        (self.root / 'v2/deploy').mkdir()
        (self.root / 'v2/deploy/runtime.json').write_text('{}')
        with self.assertRaisesRegex(RuntimeError, 'Mixed'): guard.check(self.root)

    def test_symlink_cannot_redirect_output_or_config_outside_project(self):
        outside = Path(self.temp.name) / 'other-installation'; outside.mkdir()
        for name in ('.local', 'v2/.env', 'v2/server/webdist'):
            with self.subTest(name=name):
                p = self.root / name; p.symlink_to(outside, target_is_directory=True)
                with self.assertRaisesRegex(RuntimeError, 'inside'): guard.check(self.root)
                p.unlink()

    def test_build_target_argument_fails_before_creating_target(self):
        target=Path(self.temp.name)/'other-installation'
        script=Path(__file__).with_name('build.sh')
        result=subprocess.run(['bash',str(script),'--target',str(target)],capture_output=True,text=True)
        self.assertEqual(result.returncode,2); self.assertFalse(target.exists())

if __name__ == '__main__': unittest.main()
