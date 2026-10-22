import gzip,os,pathlib,subprocess,tempfile,unittest
SCRIPT=pathlib.Path(__file__).with_name("precompress-webdist.sh")
class PrecompressTests(unittest.TestCase):
 def test_failed_encoder_keeps_valid_representation_and_retry_recovers(self):
  with tempfile.TemporaryDirectory() as d:
   root=pathlib.Path(d);assets=root/"assets";assets.mkdir();source=assets/"app.js";source.write_bytes(b"export const value=42;"*100)
   for ext in ("gz","zst"):
    side=pathlib.Path(str(source)+"."+ext);side.write_bytes(b"previous-valid-representation");os.utime(side,(1,1))
   for encoder in ("gzip","zstd"):
    fake=root/"bin";fake.mkdir(exist_ok=True)
    tool=fake/encoder;tool.write_text("#!/bin/sh\nprintf broken-output\nexit 1\n");tool.chmod(0o755)
    if encoder=="zstd":
     # Let gzip finish first so the zstd failure path is exercised too.
     pathlib.Path(str(source)+".gz").write_bytes(gzip.compress(source.read_bytes()))
    result=subprocess.run(["bash",str(SCRIPT),str(assets)],env={**os.environ,"PATH":str(fake)+":"+os.environ["PATH"]},capture_output=True)
    self.assertNotEqual(result.returncode,0)
    suffix="gz" if encoder=="gzip" else "zst"
    self.assertEqual(pathlib.Path(str(source)+"."+suffix).read_bytes(),b"previous-valid-representation")
    self.assertEqual(list(assets.glob("*.tmp.*")),[])
    tool.unlink()
   result=subprocess.run(["bash",str(SCRIPT),str(assets)],capture_output=True)
   self.assertEqual(result.returncode,0,result.stderr)
   self.assertEqual(gzip.decompress(pathlib.Path(str(source)+".gz").read_bytes()),source.read_bytes())
if __name__=="__main__":unittest.main()
