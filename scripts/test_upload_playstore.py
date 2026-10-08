import importlib.util
import os
import tempfile
import unittest
from unittest.mock import Mock


SCRIPT = os.path.join(os.path.dirname(__file__), "upload-playstore.py")
SPEC = importlib.util.spec_from_file_location("upload_playstore", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class UploadPlayStoreTest(unittest.TestCase):
    def test_commit_recovers_only_explicit_review_gate(self):
        service = Mock()
        failure = RuntimeError("Set changesNotSentForReview to true")
        failure.resp = Mock(status=400)
        service.edits.return_value.commit.return_value.execute.side_effect = [failure, {}]
        self.assertTrue(MODULE.commit_release_edit(service, "example.app", "edit"))
        self.assertEqual(service.edits.return_value.commit.call_count, 2)
        service.edits.return_value.commit.assert_called_with(
            packageName="example.app", editId="edit", changesNotSentForReview=True,
        )

    def test_commit_propagates_unrelated_failure_and_failed_recovery(self):
        for failures in ([RuntimeError("denied")], [self.review_gate(), RuntimeError("denied")]):
            service = Mock()
            service.edits.return_value.commit.return_value.execute.side_effect = failures
            with self.assertRaisesRegex(RuntimeError, "denied"):
                MODULE.commit_release_edit(service, "example.app", "edit")

    @staticmethod
    def review_gate():
        failure = RuntimeError("Set changesNotSentForReview to true")
        failure.resp = Mock(status=400)
        return failure

    def test_commit_success_does_not_retry(self):
        service = Mock()
        self.assertFalse(MODULE.commit_release_edit(service, "example.app", "edit"))
        service.edits.return_value.commit.assert_called_once_with(
            packageName="example.app", editId="edit",
        )

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

    def test_highest_remote_version_code_unions_bundles_and_tracks(self):
        bundles = [{"versionCode": 317}]
        tracks = [{"releases": [{"versionCodes": [316, 318]}]}]
        self.assertEqual(
            MODULE.highest_remote_version_code(bundles, tracks), 318
        )

    def test_highest_remote_version_code_handles_empty_inventory(self):
        self.assertEqual(MODULE.highest_remote_version_code([], []), 0)

if __name__ == "__main__":
    unittest.main()
