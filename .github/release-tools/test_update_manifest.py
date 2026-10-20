import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('update_manifest', Path(__file__).with_name('update_manifest.py'))
manifest = importlib.util.module_from_spec(spec)
spec.loader.exec_module(manifest)

class UpdateManifestTest(unittest.TestCase):
    def test_exact_image_digest_and_architecture(self):
        with tempfile.TemporaryDirectory() as temp:
            for arch, letter in [('amd64', 'a'), ('arm64', 'b')]:
                Path(temp, f'image-{arch}.json').write_text(json.dumps({'containerimage.digest': 'sha256:' + letter * 64}))
            value = manifest.generate('0.2.17-custom.13', 'f' * 40, temp)
            self.assertEqual(value['images']['amd64'], 'ghcr.io/deanzfc/sub2api@sha256:' + 'a' * 64)
            self.assertEqual(set(value['images']), {'amd64', 'arm64'})
            self.assertEqual(set(manifest.generate('0.2.17-custom.13', 'f' * 40, temp, True)['images']), {'amd64'})
            Path(temp, 'image-arm64.json').write_text('{}')
            with self.assertRaises(ValueError):
                manifest.generate('0.2.17-custom.13', 'f' * 40, temp)
            with self.assertRaises(ValueError):
                manifest.generate('latest', 'f' * 40, temp)
