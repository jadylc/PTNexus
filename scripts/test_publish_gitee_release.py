import importlib.util
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("publish-gitee-release.py")
SPEC = importlib.util.spec_from_file_location("publish_gitee_release", SCRIPT_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load {SCRIPT_PATH}")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class CollectAssetPathsTest(unittest.TestCase):
    def test_manifest_is_required_and_uploaded_first(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            assets_dir = Path(temp_dir)
            manifest = assets_dir / MODULE.MANIFEST_NAME
            arm64 = assets_dir / MODULE.RUNTIME_NAMES[1]
            amd64 = assets_dir / MODULE.RUNTIME_NAMES[0]
            manifest.write_text("{}", encoding="utf-8")
            arm64.write_bytes(b"a" * 10)
            amd64.write_bytes(b"b" * 20)

            selected, skipped = MODULE.collect_asset_paths(assets_dir, 100)

            self.assertEqual(
                [path.name for path in selected],
                [manifest.name, arm64.name, amd64.name],
            )
            self.assertEqual(skipped, [])

    def test_missing_manifest_fails_before_release_upload(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            assets_dir = Path(temp_dir)
            (assets_dir / MODULE.RUNTIME_NAMES[0]).write_bytes(b"runtime")

            with self.assertRaisesRegex(RuntimeError, MODULE.MANIFEST_NAME):
                MODULE.collect_asset_paths(assets_dir, 100)


class UploadReleaseAssetTest(unittest.TestCase):
    @mock.patch.object(MODULE.subprocess, "run")
    def test_curl_uses_official_multipart_fields_and_validates_response(
        self, run: mock.Mock
    ) -> None:
        run.return_value = subprocess.CompletedProcess(
            args=[],
            returncode=0,
            stdout=json.dumps({"id": 123, "name": MODULE.MANIFEST_NAME}),
            stderr="",
        )
        file_path = Path("/tmp") / MODULE.MANIFEST_NAME

        result = MODULE.upload_release_asset("owner", "repo", 7, "secret", file_path)

        self.assertEqual(result["id"], 123)
        command = run.call_args.args[0]
        self.assertNotIn("--retry", command)
        self.assertNotIn("--location", command)
        self.assertEqual(command[command.index("--max-time") + 1], "60")
        self.assertIn("access_token=secret", command)
        self.assertIn(f"file=@{file_path}", command)
        self.assertNotIn("Content-Type: multipart/form-data", command)
        self.assertNotIn("owner=owner", command)
        self.assertNotIn("repo=repo", command)
        self.assertNotIn("release_id=7", command)

    @mock.patch.object(MODULE.subprocess, "run")
    def test_invalid_success_response_is_rejected(self, run: mock.Mock) -> None:
        run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="{}", stderr=""
        )

        with self.assertRaisesRegex(RuntimeError, "unexpected response"):
            MODULE.upload_release_asset(
                "owner", "repo", 7, "secret", Path("asset.bin")
            )

    @mock.patch.object(MODULE.subprocess, "run")
    def test_runtime_upload_has_bounded_time(self, run: mock.Mock) -> None:
        run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout=json.dumps({"id": 456}), stderr=""
        )

        MODULE.upload_release_asset(
            "owner", "repo", 7, "secret", Path(MODULE.RUNTIME_NAMES[0])
        )

        command = run.call_args.args[0]
        self.assertEqual(command[command.index("--max-time") + 1], "180")

    @mock.patch.object(MODULE, "list_release_assets")
    @mock.patch.object(MODULE.subprocess, "run")
    def test_failed_response_is_accepted_when_asset_exists(
        self, run: mock.Mock, list_assets: mock.Mock
    ) -> None:
        run.return_value = subprocess.CompletedProcess(
            args=[], returncode=22, stdout="bad gateway", stderr="HTTP 502"
        )
        list_assets.return_value = [{"id": 789, "name": MODULE.MANIFEST_NAME}]

        result = MODULE.upload_release_asset(
            "owner", "repo", 7, "secret", Path(MODULE.MANIFEST_NAME)
        )

        self.assertEqual(result["id"], 789)
        self.assertEqual(run.call_count, 1)

    @mock.patch.object(MODULE.time, "sleep")
    @mock.patch.object(MODULE, "list_release_assets", return_value=[])
    @mock.patch.object(MODULE.subprocess, "run")
    def test_http_502_is_retried_after_confirming_asset_missing(
        self, run: mock.Mock, _list_assets: mock.Mock, sleep: mock.Mock
    ) -> None:
        run.side_effect = [
            subprocess.CompletedProcess(
                args=[], returncode=22, stdout="bad gateway", stderr="HTTP 502"
            ),
            subprocess.CompletedProcess(
                args=[], returncode=0, stdout=json.dumps({"id": 321}), stderr=""
            ),
        ]

        result = MODULE.upload_release_asset(
            "owner", "repo", 7, "secret", Path(MODULE.RUNTIME_NAMES[0])
        )

        self.assertEqual(result["id"], 321)
        self.assertEqual(run.call_count, 2)
        sleep.assert_called_once_with(2)


class UploadReleaseAssetsTest(unittest.TestCase):
    @mock.patch.object(MODULE, "upload_release_asset")
    def test_runtime_mirror_failure_is_warning_after_manifest_success(
        self, upload: mock.Mock
    ) -> None:
        manifest = Path(MODULE.MANIFEST_NAME)
        runtime = Path(MODULE.RUNTIME_NAMES[0])
        upload.side_effect = [{"id": 1}, RuntimeError("HTTP 502")]

        warnings = MODULE.upload_release_assets(
            "owner", "repo", 7, "secret", [manifest, runtime], {}
        )

        self.assertEqual(len(warnings), 1)
        self.assertIn(runtime.name, warnings[0])

    @mock.patch.object(MODULE, "upload_release_asset")
    def test_manifest_upload_failure_remains_fatal(self, upload: mock.Mock) -> None:
        upload.side_effect = RuntimeError("HTTP 400")

        with self.assertRaisesRegex(RuntimeError, "HTTP 400"):
            MODULE.upload_release_assets(
                "owner", "repo", 7, "secret", [Path(MODULE.MANIFEST_NAME)], {}
            )

    @mock.patch.object(MODULE, "upload_release_asset")
    def test_existing_asset_is_kept_without_reupload(self, upload: mock.Mock) -> None:
        manifest = Path(MODULE.MANIFEST_NAME)

        warnings = MODULE.upload_release_assets(
            "owner", "repo", 7, "secret", [manifest], {manifest.name: 99}
        )

        self.assertEqual(warnings, [])
        upload.assert_not_called()


class PruneOldReleaseAssetsTest(unittest.TestCase):
    """仓库附件配额按累计占用计算，上传前必须清理旧版本 runtime 资产。"""

    @staticmethod
    def _release(release_id: int, tag: str) -> dict:
        return {"id": release_id, "tag_name": tag}

    def test_keeps_recent_releases_and_deletes_older_runtime_assets(self) -> None:
        releases = [self._release(i, f"v4.6.{30 - i}") for i in range(15)]
        # 最近 10 版：v4.6.30..v4.6.21；旧版 5 个（v4.6.20..v4.6.16）要清。
        older_ids = list(range(10, 15))

        with mock.patch.object(MODULE, "list_releases", return_value=releases), \
             mock.patch.object(MODULE, "list_release_assets") as list_assets, \
             mock.patch.object(MODULE, "delete_release_asset") as delete:
            list_assets.return_value = [
                {"name": MODULE.RUNTIME_NAMES[0], "id": 9001},
                {"name": MODULE.RUNTIME_NAMES[1], "id": 9002},
                {"name": MODULE.MANIFEST_NAME, "id": 9003},
            ]
            removed = MODULE.prune_old_release_assets(
                "owner", "repo", current_release_id=0, current_tag="v4.6.30", token="t"
            )

        self.assertEqual(delete.call_count, len(older_ids) * 3)
        for release_id in older_ids:
            # delete_release_asset(owner, repo, release_id, asset_id, token)
            called_ids = {
                call.args[3] for call in delete.call_args_list if call.args[2] == release_id
            }
            self.assertEqual(called_ids, {9001, 9002, 9003})
        self.assertEqual(len(removed), len(older_ids) * 3)

    def test_current_release_and_recent_tags_are_never_deleted(self) -> None:
        releases = [self._release(1, "v4.6.30"), self._release(2, "v4.6.29")]
        with mock.patch.object(MODULE, "list_releases", return_value=releases), \
             mock.patch.object(MODULE, "list_release_assets", return_value=[]), \
             mock.patch.object(MODULE, "delete_release_asset") as delete:
            MODULE.prune_old_release_assets(
                "owner", "repo", current_release_id=1, current_tag="v4.6.30", token="t",
                keep_releases=10,
            )
        delete.assert_not_called()

    def test_unrelated_assets_are_not_touched(self) -> None:
        # 15 个 release：只清 11 个之外的旧版（第 12 个 v4.6.19 起的资产），
        # 验证无关资产（浏览器扩展 zip）不被触碰。
        releases = [self._release(i, f"v4.6.{30 - i}") for i in range(15)]
        with mock.patch.object(MODULE, "list_releases", return_value=releases), \
             mock.patch.object(MODULE, "list_release_assets") as list_assets, \
             mock.patch.object(MODULE, "delete_release_asset") as delete:
            list_assets.return_value = [
                {"name": "pt-nexus-browser-extension.zip", "id": 8888},
                {"name": MODULE.RUNTIME_NAMES[0], "id": 9001},
            ]
            removed = MODULE.prune_old_release_assets(
                "owner", "repo", current_release_id=0, current_tag="v4.6.30", token="t"
            )
        # 只删 runtime 包，浏览器扩展不动（每版 3 资产 = 5 旧版 × 3 - 扩展跳过）。
        self.assertEqual(delete.call_count, 5)
        for call in delete.call_args_list:
            self.assertNotEqual(call.args[3], 8888)
        self.assertEqual(len(removed), 5)

    def test_delete_failure_does_not_raise(self) -> None:
        releases = [self._release(i, f"v4.6.{30 - i}") for i in range(12)]
        with mock.patch.object(MODULE, "list_releases", return_value=releases), \
             mock.patch.object(MODULE, "list_release_assets", return_value=[
                 {"name": MODULE.RUNTIME_NAMES[0], "id": 9001}
             ]), \
             mock.patch.object(
                 MODULE, "delete_release_asset", side_effect=RuntimeError("HTTP 403")
             ):
            removed = MODULE.prune_old_release_assets(
                "owner", "repo", current_release_id=0, current_tag="v4.6.30", token="t"
            )
        self.assertEqual(len(removed), 2)
        self.assertTrue(all("failed" in item for item in removed))


if __name__ == "__main__":
    unittest.main()
