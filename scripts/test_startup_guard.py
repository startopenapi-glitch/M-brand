"""Exercise the built entrypoint, not only the validation helper, without a DB."""
from pathlib import Path
import os
import shutil
import socket
import subprocess
import tempfile
import unittest

class StartupGuardTests(unittest.TestCase):
    def test_unsafe_direct_starts_fail_before_secrets_and_database(self):
        source=Path(__file__).resolve().parents[1]/'.local/bin/m-brand'
        self.assertTrue(source.is_file(), 'Build before running startup acceptance tests')
        env={k:v for k,v in os.environ.items() if not k.startswith(('ZEROBRAND_','MBRAND_','PG','WXPAY_','WX_','SMTP_','RESEND_'))}
        with tempfile.TemporaryDirectory() as tmp, socket.socket() as trap:
            root=Path(tmp); binary=root/'m-brand'; shutil.copy2(source,binary)
            trap.bind(('127.0.0.1',0)); trap.listen(); trap.settimeout(0.03)
            dsn='postgres://test:fixture-password@127.0.0.1:'+str(trap.getsockname()[1])+'/mbrand'
            normal=dict(env,MBRAND_DEPLOYMENT_ID='m-brand',ZEROBRAND_PG_URL=dsn,ZEROBRAND_PUBLIC_ORIGIN='https://localhost:8443')
            cases=[('no identity',{'MBRAND_DEPLOYMENT_ID':''}),
                   ('no database',{'ZEROBRAND_PG_URL':''}),
                   ('legacy database',{'ZEROBRAND_PG_URL':dsn.replace('/mbrand','/zerobrand')}),
                   ('query override',{'ZEROBRAND_PG_URL':dsn+'?dbname=zerobrand'}),
                   ('original origin',{'ZEROBRAND_PUBLIC_ORIGIN':'https://www.0-brand.com'})]
            for name, overrides in cases:
                with self.subTest(name=name):
                    p=subprocess.run([str(binary),'api'],cwd=root,env=dict(normal,**overrides),capture_output=True,text=True,timeout=8)
                    self.assertNotEqual(p.returncode,0)
                    self.assertIn('Deployment refused',p.stderr)
                    self.assertNotIn('fixture-password',p.stderr)
                    self.assertFalse((root/'secret.key').exists())
                    with self.assertRaises(socket.timeout):
                        connection,_=trap.accept(); connection.close()

if __name__ == '__main__': unittest.main()
