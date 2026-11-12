#!/usr/bin/env python3
import argparse
import json
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(line_buffering=True)


API_BASE = "https://gitee.com/api/v5"
DEFAULT_MAX_BYTES = 100 * 1024 * 1024
# 仓库附件总配额按累计占用计算（免费版 1GB）：runtime 包每版约 22MB，
# 无限累积会打爆配额导致后续版本上传失败（400 文件大小已超出仓库附件配额）。
# 上传前自动清理旧 release 的 runtime 资产，仅保留最近 KEEP_RELEASES 个版本。
KEEP_RELEASES = 10
MANIFEST_NAME = "UPDATE_MANIFEST.json"
RUNTIME_NAMES = (
    "ptnexus-runtime-linux-amd64.tar.gz",
    "ptnexus-runtime-linux-arm64.tar.gz",
)


def add_query(url: str, **params: str) -> str:
    parsed = urllib.parse.urlsplit(url)
    query = urllib.parse.parse_qsl(parsed.query, keep_blank_values=True)
    for key, value in params.items():
        if value == "":
            continue
        query.append((key, value))
    return urllib.parse.urlunsplit(
        (
            parsed.scheme,
            parsed.netloc,
            parsed.path,
            urllib.parse.urlencode(query),
            parsed.fragment,
        )
    )


def encode_form_payload(payload: dict[str, Any]) -> bytes:
    form_items: list[tuple[str, str]] = []
    for key, value in payload.items():
        if value is None:
            continue
        if isinstance(value, bool):
            value = "true" if value else "false"
        form_items.append((key, str(value)))
    return urllib.parse.urlencode(form_items).encode("utf-8")


def api_request(
    method: str,
    url: str,
    token: str,
    payload: dict[str, Any] | None = None,
    *,
    form_encoded: bool = False,
) -> Any:
    request_url = add_query(url, access_token=token)
    data = None
    headers = {
        "Accept": "application/json",
        "User-Agent": "ptnexus-release-sync",
    }
    if payload is not None:
        if form_encoded:
            data = encode_form_payload(payload)
            headers["Content-Type"] = "application/x-www-form-urlencoded"
        else:
            data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
            headers["Content-Type"] = "application/json"

    request = urllib.request.Request(request_url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            raw = response.read()
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace").strip()
        raise RuntimeError(f"{method} {url} failed with HTTP {exc.code}: {body}") from exc
    except urllib.error.URLError as exc:
        raise RuntimeError(f"{method} {url} failed: {exc}") from exc

    if not raw:
        return None
    return json.loads(raw.decode("utf-8"))


def get_release_by_tag(owner: str, repo: str, tag: str, token: str) -> dict[str, Any] | None:
    url = f"{API_BASE}/repos/{urllib.parse.quote(owner)}/{urllib.parse.quote(repo)}/releases/tags/{urllib.parse.quote(tag)}"
    request_url = add_query(url, access_token=token)
    request = urllib.request.Request(
        request_url,
        headers={"Accept": "application/json", "User-Agent": "ptnexus-release-sync"},
        method="GET",
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        if exc.code == 404:
            return None
        body = exc.read().decode("utf-8", errors="replace").strip()
        raise RuntimeError(f"GET {url} failed with HTTP {exc.code}: {body}") from exc
    except urllib.error.URLError as exc:
        raise RuntimeError(f"GET {url} failed: {exc}") from exc


def create_release(
    owner: str,
    repo: str,
    tag: str,
    target_commitish: str,
    name: str,
    body: str,
    token: str,
) -> dict[str, Any]:
    url = f"{API_BASE}/repos/{urllib.parse.quote(owner)}/{urllib.parse.quote(repo)}/releases"
    payload = {
        "tag_name": tag,
        "target_commitish": target_commitish,
        "name": name,
        "body": body,
        "prerelease": False,
    }
    result = api_request("POST", url, token, payload=payload, form_encoded=True)
    if not isinstance(result, dict) or not result.get("id"):
        raise RuntimeError(f"unexpected create release response: {result!r}")
    return result


def update_release(
    owner: str,
    repo: str,
    release_id: int,
    tag: str,
    target_commitish: str,
    name: str,
    body: str,
    token: str,
) -> dict[str, Any]:
    url = f"{API_BASE}/repos/{urllib.parse.quote(owner)}/{urllib.parse.quote(repo)}/releases/{release_id}"
    payload = {
        "tag_name": tag,
        "target_commitish": target_commitish,
        "name": name,
        "body": body,
        "prerelease": False,
    }
    result = api_request("PATCH", url, token, payload=payload, form_encoded=True)
    if not isinstance(result, dict):
        raise RuntimeError(f"unexpected update release response: {result!r}")
    return result


def list_release_assets(owner: str, repo: str, release_id: int, token: str) -> list[dict[str, Any]]:
    url = f"{API_BASE}/repos/{urllib.parse.quote(owner)}/{urllib.parse.quote(repo)}/releases/{release_id}/attach_files"
    result = api_request("GET", url, token)
    if result is None:
        return []
    if not isinstance(result, list):
        raise RuntimeError(f"unexpected list assets response: {result!r}")
    return result


def list_releases(owner: str, repo: str, token: str, page_size: int = 100) -> list[dict[str, Any]]:
    """列出仓库全部 release（分页拉取，按创建时间倒序返回）。"""
    releases: list[dict[str, Any]] = []
    page = 1
    while True:
        url = (
            f"{API_BASE}/repos/{urllib.parse.quote(owner)}/{urllib.parse.quote(repo)}/releases"
            f"?page={page}&per_page={page_size}&direction=desc"
        )
        result = api_request("GET", url, token)
        if not isinstance(result, list) or not result:
            break
        releases.extend(item for item in result if isinstance(item, dict))
        if len(result) < page_size:
            break
        page += 1
        if page > 100:  # 防御上限：100 页 × 100 条足够覆盖任何仓库
            break
    return releases


def tag_version_key(item: dict[str, Any]) -> tuple[int, ...]:
    """tag 的版本号排序键（'v4.6.9' → (4, 6, 9)，数字段按数值比较）。"""
    tag = str(item.get("tag_name", "")).strip()
    parts = []
    for chunk in tag.lstrip("vV").split("."):
        digits = "".join(ch for ch in chunk if ch.isdigit())
        parts.append(int(digits) if digits else 0)
    return tuple(parts)


def prune_old_releases(
    owner: str,
    repo: str,
    current_release_id: int,
    current_tag: str,
    token: str,
    keep_releases: int = KEEP_RELEASES,
) -> list[str]:
    """删除最新 keep_releases 个版本之外的整个 Gitee 发行版（含全部附件）。

    仓库附件总配额按累计占用计算（免费版 1GB），无限累积会导致后续版本
    上传失败（400 文件大小已超出仓库附件配额）。
    Gitee releases 列表 API 排序不可靠（默认创建时间正序）——按 tag 版本号
    排序取保留集。
    参数/返回：返回清理动作描述列表（供日志输出）。
    失败场景：删除失败不抛错，记录为警告（不阻塞本次上传）。
    副作用：调用 Gitee API 删除发行版本体（含其附件）。
    """
    removed: list[str] = []
    try:
        releases = list_releases(owner, repo, token)
    except RuntimeError as exc:
        return [f"list releases failed, skip pruning: {exc}"]

    sorted_releases = sorted(releases, key=tag_version_key, reverse=True)
    keep_tags: set[str] = set()
    for item in sorted_releases[:keep_releases]:
        tag = str(item.get("tag_name", "")).strip()
        if tag:
            keep_tags.add(tag)
    keep_tags.add(current_tag)

    deletable = [
        item
        for item in releases
        if str(item.get("tag_name", "")).strip() not in keep_tags
        and int(item.get("id", 0) or 0) != current_release_id
    ]

    for item in deletable:
        release_id = int(item.get("id", 0) or 0)
        tag = str(item.get("tag_name", "")).strip()
        if release_id <= 0:
            continue
        try:
            url = (
                f"{API_BASE}/repos/{urllib.parse.quote(owner)}/{urllib.parse.quote(repo)}"
                f"/releases/{release_id}"
            )
            api_request("DELETE", url, token)
            removed.append(f"deleted release {tag} (id={release_id})")
        except RuntimeError as exc:
            removed.append(f"{tag}: delete release failed ({exc})")
    return removed



def upload_release_asset(
    owner: str, repo: str, release_id: int, token: str, file_path: Path
) -> dict[str, Any]:
    url = f"{API_BASE}/repos/{owner}/{repo}/releases/{release_id}/attach_files"
    is_manifest = file_path.name == MANIFEST_NAME
    max_attempts = 3 if is_manifest else 2
    max_time_seconds = "60" if is_manifest else "180"
    command = [
        "curl",
        "--fail-with-body",
        "--silent",
        "--show-error",
        "--connect-timeout",
        "20",
        "--max-time",
        max_time_seconds,
        "--speed-limit",
        "1024",
        "--speed-time",
        "60",
        "-H",
        "Expect:",
        "-F",
        f"access_token={token}",
        "-F",
        f"file=@{file_path}",
        url,
    ]
    last_detail = "unknown curl error"
    for attempt in range(1, max_attempts + 1):
        completed = subprocess.run(command, capture_output=True, text=True)
        if completed.returncode == 0:
            try:
                result = json.loads(completed.stdout)
            except json.JSONDecodeError as exc:
                raise RuntimeError(
                    f"upload {file_path.name} returned invalid JSON: "
                    f"{completed.stdout[:500]!r}"
                ) from exc
            if not isinstance(result, dict) or result.get("id") is None:
                raise RuntimeError(
                    f"upload {file_path.name} returned unexpected response: {result!r}"
                )
            return result

        stderr = completed.stderr.strip()
        stdout = completed.stdout.strip()
        last_detail = (
            " | ".join(part for part in (stderr, stdout) if part)
            or "unknown curl error"
        )

        try:
            assets = list_release_assets(owner, repo, release_id, token)
        except RuntimeError as exc:
            print(
                f"[gitee-release] failed to verify {file_path.name} after "
                f"upload error: {exc}",
                file=sys.stderr,
            )
        else:
            for asset in assets:
                if str(asset.get("name", "")).strip() != file_path.name:
                    continue
                if asset.get("id") is not None:
                    print(
                        f"[gitee-release] upload response failed but asset exists: "
                        f"{file_path.name} (id={asset['id']})"
                    )
                    return asset

        if attempt < max_attempts:
            print(
                f"[gitee-release] retrying {file_path.name} after upload failure "
                f"({attempt}/{max_attempts}): {last_detail}",
                file=sys.stderr,
            )
            time.sleep(2)

    raise RuntimeError(f"upload {file_path.name} failed: {last_detail}")


def collect_asset_paths(assets_dir: Path, max_bytes: int) -> tuple[list[Path], list[str]]:
    selected: list[Path] = []
    skipped: list[str] = []
    available_files = {path.name: path for path in assets_dir.iterdir() if path.is_file()}

    manifest_path = available_files.get(MANIFEST_NAME)
    if manifest_path is None:
        raise RuntimeError(f"{MANIFEST_NAME} not found in {assets_dir}")
    selected.append(manifest_path)

    runtime_paths: list[Path] = []
    for name in RUNTIME_NAMES:
        path = available_files.get(name)
        if path is None:
            skipped.append(f"{name}: skipped (not found)")
            continue
        size = path.stat().st_size
        if size > max_bytes:
            skipped.append(f"{name}: skipped ({size} bytes > {max_bytes})")
            continue
        runtime_paths.append(path)

    selected.extend(
        sorted(runtime_paths, key=lambda path: (path.stat().st_size, path.name))
    )

    return selected, skipped


def upload_release_assets(
    owner: str,
    repo: str,
    release_id: int,
    token: str,
    selected_paths: list[Path],
    existing_by_name: dict[str, int],
) -> list[str]:
    warnings: list[str] = []
    for path in selected_paths:
        try:
            attach_id = existing_by_name.get(path.name)
            if attach_id is not None:
                print(
                    f"[gitee-release] keeping existing {path.name} (id={attach_id})"
                )
                continue
            upload_release_asset(owner, repo, release_id, token, path)
            print(f"[gitee-release] uploaded {path.name}")
        except RuntimeError as exc:
            if path.name == MANIFEST_NAME:
                raise
            warnings.append(f"{path.name}: optional mirror upload failed: {exc}")
    return warnings


def read_body(body_file: Path) -> str:
    if not body_file.is_file():
        raise RuntimeError(f"release body file not found: {body_file}")
    return body_file.read_text(encoding="utf-8").strip()


def main() -> int:
    parser = argparse.ArgumentParser(description="Publish release assets to Gitee Release.")
    parser.add_argument("--owner", required=True, help="Gitee owner")
    parser.add_argument("--repo", required=True, help="Gitee repo")
    parser.add_argument("--tag", required=True, help="Release tag")
    parser.add_argument("--target-commitish", required=True, help="Branch or commitish used by Gitee when creating the tag")
    parser.add_argument("--name", required=True, help="Release name")
    parser.add_argument("--body-file", required=True, help="Release body markdown file")
    parser.add_argument("--assets-dir", required=True, help="Directory containing release assets")
    parser.add_argument("--token", required=True, help="Gitee access token")
    parser.add_argument("--max-bytes", type=int, default=DEFAULT_MAX_BYTES, help="Max upload size per asset")
    parser.add_argument("--dry-run", action="store_true", help="Print selected files without calling Gitee API")
    args = parser.parse_args()

    body = read_body(Path(args.body_file).resolve())
    assets_dir = Path(args.assets_dir).resolve()
    if not assets_dir.is_dir():
        raise RuntimeError(f"assets directory not found: {assets_dir}")

    selected_paths, skipped = collect_asset_paths(assets_dir, args.max_bytes)

    print(
        f"[gitee-release] repo={args.owner}/{args.repo} tag={args.tag} target_commitish={args.target_commitish}"
    )
    for message in skipped:
        print(f"[gitee-release] {message}")
    for path in selected_paths:
        print(f"[gitee-release] upload {path.name}")

    if args.dry_run:
        print("[gitee-release] dry run complete")
        return 0

    release = get_release_by_tag(args.owner, args.repo, args.tag, args.token)
    if release is None:
        print(f"[gitee-release] creating release {args.tag}")
        release = create_release(
            args.owner,
            args.repo,
            args.tag,
            args.target_commitish,
            args.name,
            body,
            args.token,
        )
    else:
        print(f"[gitee-release] updating release {args.tag} (id={release.get('id')})")
        release = update_release(
            args.owner,
            args.repo,
            int(release["id"]),
            args.tag,
            args.target_commitish,
            args.name,
            body,
            args.token,
        )

    release_id = int(release["id"])
    existing_assets = list_release_assets(args.owner, args.repo, release_id, args.token)
    existing_by_name = {
        str(item.get("name", "")).strip(): int(item["id"])
        for item in existing_assets
        if str(item.get("name", "")).strip() and item.get("id") is not None
    }

    # 上传前删除最新 KEEP_RELEASES 版之外的整个发行版（含全部附件），
    # 防止仓库附件累计超出配额（免费版 1GB）。
    for message in prune_old_releases(
        args.owner, args.repo, release_id, args.tag, args.token
    ):
        print(f"[gitee-release] prune: {message}")

    upload_warnings = upload_release_assets(
        args.owner,
        args.repo,
        release_id,
        args.token,
        selected_paths,
        existing_by_name,
    )
    for warning in upload_warnings:
        print(f"[gitee-release] warning: {warning}", file=sys.stderr)

    print(f"[gitee-release] release ready: {release.get('html_url', '')}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except RuntimeError as exc:
        print(f"[gitee-release] {exc}", file=sys.stderr)
        raise SystemExit(1)
