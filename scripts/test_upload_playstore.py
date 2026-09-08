import importlib.util
import os
import tempfile
import unittest


SCRIPT = os.path.join(os.path.dirname(__file__), "upload-playstore.py")
SPEC = importlib.util.spec_from_file_location("upload_playstore", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class UploadPlayStoreTest(unittest.TestCase):
    def test_default_gradle_fallback_is_the_real_mobile_app(self):
        self.assertTrue(MODULE.DEFAULT_GRADLE_PATH.endswith(
            os.path.join("mobile", "android", "app", "build.gradle")
        ))
        self.assertIsNotNone(MODULE.read_gradle_version_code(
            MODULE.DEFAULT_GRADLE_PATH
        ))

    def test_read_gradle_version_code(self):
        with tempfile.NamedTemporaryFile(mode="w", delete=False) as fixture:
            fixture.write("defaultConfig { versionCode 417 }\n")
            path = fixture.name
        try:
            self.assertEqual(MODULE.read_gradle_version_code(path), 417)
        finally:
            os.unlink(path)

    def test_form_factor_track_detection(self):
        self.assertTrue(MODULE.is_form_factor_track("wear:internal"))
        self.assertTrue(MODULE.is_form_factor_track("tv:production"))
        self.assertFalse(MODULE.is_form_factor_track("internal"))

    def test_internal_track_detection_includes_form_factors(self):
        for track in ("internal", "qa", "wear:internal", "tv:qa"):
            self.assertTrue(MODULE.track_is_internal(track))
        self.assertFalse(MODULE.track_is_internal("wear:production"))

    def test_build_time_version_code_hint_is_authoritative(self):
        self.assertEqual(MODULE.parse_version_code_hints("310", 1), [310])
        self.assertEqual(
            MODULE.parse_version_code_hints("310, 311", 2), [310, 311]
        )

    def test_version_code_hint_rejects_mismatch_and_invalid_values(self):
        for value, count in (("310", 2), ("zero", 1), ("0", 1), ("-1", 1)):
            with self.subTest(value=value, count=count):
                with self.assertRaises(ValueError):
                    MODULE.parse_version_code_hints(value, count)

if __name__ == "__main__":
    unittest.main()
