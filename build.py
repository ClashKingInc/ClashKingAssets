from __future__ import annotations

import argparse
import concurrent.futures
import json
import os
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from tempfile import TemporaryDirectory
from typing import Any

from dotenv import load_dotenv

from asset_sounds import check_sound_effects
from check_image_sources import check_image_sources
from generate_manifest import APP_METADATA_INDEX, APP_METADATA_PREFIX, ManifestError, build_app_metadata, check_manifest
from worker_purge import WorkerPurgeError, load_worker_purge_config, purge_worker_cache

load_dotenv()

CDN_CACHE_CONTROL = "public, max-age=86400, stale-while-revalidate=604800"
CDN_CONTENT_TYPES = {
    ".glb": "model/gltf-binary",
    ".json": "application/json",
    ".ogg": "audio/ogg",
    ".svg": "image/svg+xml",
    ".ttf": "font/ttf",
    ".woff2": "font/woff2",
}
RELEASE_INDEXES = frozenset({"manifest.json", APP_METADATA_INDEX})


class BuildError(RuntimeError):
    pass


@dataclass(frozen=True)
class R2Config:
    endpoint_url: str
    access_key_id: str
    secret_access_key: str
    bucket: str


@dataclass(frozen=True)
class DiffEntry:
    status: str
    path: str | None = None
    old_path: str | None = None
    new_path: str | None = None


@dataclass(frozen=True)
class UploadOperation:
    local_path: str
    key: str
    reason: str


@dataclass(frozen=True)
class DeleteOperation:
    key: str
    reason: str


def upload_extra_args(key: str) -> dict[str, str] | None:
    content_type = CDN_CONTENT_TYPES.get(Path(key).suffix.casefold())
    if content_type is None:
        return None
    return {"ContentType": content_type, "CacheControl": CDN_CACHE_CONTROL}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Sync changed asset files for a release to R2.")
    parser.add_argument("--current-ref", help="Current release ref/tag to diff from.")
    parser.add_argument("--previous-ref", help="Previous release ref/tag to diff against.")
    parser.add_argument("--assets-root", default="assets", help="Repo-relative assets root. Default: assets")
    parser.add_argument("--dry-run", action="store_true", help="Print the sync plan without writing to R2.")
    parser.add_argument("--workers", type=int, default=32, help="Concurrent R2 uploads. Default: 32")
    return parser.parse_args()


def run_git(args: list[str]) -> str:
    try:
        completed = subprocess.run(["git", *args], check=True, capture_output=True, text=True)
    except subprocess.CalledProcessError as exc:
        stderr = exc.stderr.strip()
        detail = f": {stderr}" if stderr else ""
        raise BuildError(f"git {' '.join(args)} failed{detail}") from exc
    return completed.stdout.strip()


def normalize_assets_root(assets_root: str) -> str:
    normalized = Path(assets_root).as_posix().strip("/")
    if not normalized or normalized == ".":
        raise BuildError("assets root must not be empty")
    return normalized


def path_under_root(path: str, assets_root: str) -> bool:
    normalized = Path(path).as_posix().strip("/")
    return normalized == assets_root or normalized.startswith(f"{assets_root}/")


def key_for_path(path: str, assets_root: str) -> str:
    normalized = Path(path).as_posix().strip("/")
    if not path_under_root(normalized, assets_root):
        raise BuildError(f"path is outside assets root {assets_root!r}: {path}")
    if normalized == assets_root:
        raise BuildError(f"path resolves to assets root itself, not a file: {path}")
    return normalized.removeprefix(f"{assets_root}/")


def parse_name_status(output: str) -> list[DiffEntry]:
    entries: list[DiffEntry] = []
    if not output:
        return entries

    for raw_line in output.splitlines():
        line = raw_line.strip()
        if not line:
            continue
        parts = line.split("\t")
        status = parts[0]

        if status.startswith("R"):
            if len(parts) != 3:
                raise BuildError(f"unexpected rename diff line: {raw_line}")
            entries.append(DiffEntry(status=status, old_path=parts[1], new_path=parts[2]))
            continue

        if status.startswith("C"):
            if len(parts) != 3:
                raise BuildError(f"unexpected copy diff line: {raw_line}")
            entries.append(DiffEntry(status=status, old_path=parts[1], new_path=parts[2]))
            continue

        if len(parts) != 2:
            raise BuildError(f"unexpected diff line: {raw_line}")
        entries.append(DiffEntry(status=status, path=parts[1]))

    return entries


def infer_current_ref(explicit_ref: str | None) -> str:
    if explicit_ref:
        return explicit_ref

    event_path = os.getenv("GITHUB_EVENT_PATH")
    if event_path:
        try:
            event = json.loads(Path(event_path).read_text())
        except (OSError, json.JSONDecodeError) as exc:
            raise BuildError(f"failed to read GitHub event payload at {event_path}: {exc}") from exc
        tag_name = event.get("release", {}).get("tag_name")
        if tag_name:
            return str(tag_name)

    for env_var in ("GITHUB_REF_NAME", "GITHUB_REF"):
        value = os.getenv(env_var, "").strip()
        if not value:
            continue
        if env_var == "GITHUB_REF" and value.startswith("refs/tags/"):
            return value.removeprefix("refs/tags/")
        return value

    return run_git(["describe", "--tags", "--exact-match", "HEAD"])


def infer_previous_ref(current_ref: str, explicit_ref: str | None) -> str | None:
    try:
        run_git(["rev-parse", "--verify", f"{current_ref}^{{commit}}"])
    except BuildError as exc:
        raise BuildError(f"invalid current release ref {current_ref!r}") from exc

    if explicit_ref:
        return explicit_ref
    try:
        return run_git(["describe", "--tags", "--abbrev=0", f"{current_ref}^"])
    except BuildError:
        return None


def git_diff_entries(previous_ref: str, current_ref: str) -> list[DiffEntry]:
    output = run_git(["diff", "--name-status", "-M", previous_ref, current_ref, "--"])
    return parse_name_status(output)


def collect_all_asset_entries(assets_root: str) -> list[DiffEntry]:
    root = Path(assets_root)
    if not root.exists():
        raise BuildError(f"assets root does not exist: {assets_root}")
    if not root.is_dir():
        raise BuildError(f"assets root is not a directory: {assets_root}")

    entries: list[DiffEntry] = []
    for path in sorted(root.rglob("*")):
        if path.is_file():
            entries.append(DiffEntry(status="A", path=path.as_posix()))
    return entries


def build_sync_plan(entries: list[DiffEntry], assets_root: str) -> dict[str, Any]:
    uploads: list[UploadOperation] = []
    deletes: list[DeleteOperation] = []
    skipped: list[dict[str, str]] = []
    counts = {"added": 0, "modified": 0, "deleted": 0, "renamed": 0}

    for entry in entries:
        status_code = entry.status[0]

        if status_code in {"A", "M", "T"}:
            if not entry.path or not path_under_root(entry.path, assets_root):
                continue
            reason = {"A": "added", "M": "modified", "T": "type_changed"}[status_code]
            key = key_for_path(entry.path, assets_root)
            local_path = Path(entry.path)
            if not local_path.exists():
                raise BuildError(f"missing local file for upload: {entry.path}")
            if not local_path.is_file():
                skipped.append({"path": entry.path, "reason": "not_a_file"})
                continue
            uploads.append(UploadOperation(local_path=local_path.as_posix(), key=key, reason=reason))
            counts["added" if status_code == "A" else "modified"] += 1
            continue

        if status_code == "D":
            if not entry.path or not path_under_root(entry.path, assets_root):
                continue
            deletes.append(DeleteOperation(key=key_for_path(entry.path, assets_root), reason="deleted"))
            counts["deleted"] += 1
            continue

        if status_code == "R":
            old_path = entry.old_path or ""
            new_path = entry.new_path or ""
            old_in_assets = path_under_root(old_path, assets_root)
            new_in_assets = path_under_root(new_path, assets_root)
            if not old_in_assets and not new_in_assets:
                continue
            if old_in_assets:
                deletes.append(DeleteOperation(key=key_for_path(old_path, assets_root), reason="renamed"))
            if new_in_assets:
                local_path = Path(new_path)
                if not local_path.exists():
                    raise BuildError(f"missing local file for renamed upload: {new_path}")
                if not local_path.is_file():
                    skipped.append({"path": new_path, "reason": "not_a_file"})
                    continue
                uploads.append(
                    UploadOperation(
                        local_path=local_path.as_posix(), key=key_for_path(new_path, assets_root), reason="renamed"
                    )
                )
            counts["renamed"] += 1
            continue

        if status_code == "C":
            new_path = entry.new_path or ""
            if not path_under_root(new_path, assets_root):
                continue
            local_path = Path(new_path)
            if not local_path.exists():
                raise BuildError(f"missing local file for copied upload: {new_path}")
            if not local_path.is_file():
                skipped.append({"path": new_path, "reason": "not_a_file"})
                continue
            uploads.append(
                UploadOperation(
                    local_path=local_path.as_posix(), key=key_for_path(new_path, assets_root), reason="copied"
                )
            )
            counts["added"] += 1
            continue

        skipped.append({"path": entry.path or entry.new_path or "", "reason": f"unsupported_status:{entry.status}"})

    return {
        "counts": counts,
        "uploads": [operation.__dict__ for operation in uploads],
        "deletes": [operation.__dict__ for operation in deletes],
        "skipped": skipped,
    }


def load_r2_config() -> R2Config:
    values = {
        "R2_ENDPOINT_URL": os.getenv("R2_ENDPOINT_URL", "").strip(),
        "R2_ACCESS_KEY_ID": os.getenv("R2_ACCESS_KEY_ID", "").strip(),
        "R2_SECRET_ACCESS_KEY": os.getenv("R2_SECRET_ACCESS_KEY", "").strip(),
        "R2_BUCKET": os.getenv("R2_BUCKET", "").strip(),
    }
    missing = [key for key, value in values.items() if not value]
    if missing:
        raise BuildError(f"missing required R2 configuration: {', '.join(missing)}")
    return R2Config(
        endpoint_url=values["R2_ENDPOINT_URL"],
        access_key_id=values["R2_ACCESS_KEY_ID"],
        secret_access_key=values["R2_SECRET_ACCESS_KEY"],
        bucket=values["R2_BUCKET"],
    )


def create_r2_client(config: R2Config):
    import boto3

    session = boto3.session.Session()
    return session.client(
        "s3",
        endpoint_url=config.endpoint_url,
        aws_access_key_id=config.access_key_id,
        aws_secret_access_key=config.secret_access_key,
        region_name="auto",
    )


def apply_sync_plan(plan: dict[str, Any], config: R2Config, workers: int, before_manifest=None) -> None:
    if workers < 1:
        raise BuildError("workers must be at least 1")
    client = create_r2_client(config)

    def upload_file(upload: dict[str, str]) -> None:
        extra_args = upload_extra_args(upload["key"]) or {}
        client.upload_file(upload["local_path"], config.bucket, upload["key"], ExtraArgs=extra_args)

    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
        futures = [
            executor.submit(upload_file, upload) for upload in plan["uploads"] if upload["key"] not in RELEASE_INDEXES
        ]
        for future in concurrent.futures.as_completed(futures):
            future.result()

    deletions = plan["deletes"]
    for offset in range(0, len(deletions), 1000):
        batch = deletions[offset : offset + 1000]
        response = client.delete_objects(
            Bucket=config.bucket, Delete={"Objects": [{"Key": deletion["key"]} for deletion in batch], "Quiet": True}
        )
        errors = response.get("Errors", [])
        if errors:
            details = ", ".join(f"{error.get('Key')}: {error.get('Message')}" for error in errors)
            raise BuildError(f"R2 delete failed: {details}")

    # Announce the new hashes only once all referenced uploads/deletions succeed.
    if before_manifest is not None:
        before_manifest()
    for upload in plan["uploads"]:
        if upload["key"] in RELEASE_INDEXES:
            upload_file(upload)


def build_summary(
    current_ref: str,
    previous_ref: str | None,
    assets_root: str,
    plan: dict[str, Any],
    dry_run: bool,
    first_release: bool,
) -> dict[str, Any]:
    return {
        "current_ref": current_ref,
        "previous_ref": previous_ref,
        "assets_root": assets_root,
        "dry_run": dry_run,
        "first_release": first_release,
        "counts": plan["counts"],
        "uploaded_keys": [item["key"] for item in plan["uploads"]],
        "deleted_keys": [item["key"] for item in plan["deletes"]],
        "skipped": plan["skipped"],
    }


def main() -> int:
    args = parse_args()
    assets_root = normalize_assets_root(args.assets_root)
    try:
        check_manifest(Path(assets_root), Path(assets_root) / "manifest.json")
        app_metadata = build_app_metadata(Path(assets_root))
    except ManifestError as exc:
        raise BuildError(str(exc)) from exc
    current_ref = infer_current_ref(args.current_ref)
    first_release = False

    previous_ref = infer_previous_ref(current_ref, args.previous_ref)
    if previous_ref is None:
        first_release = True
        entries = collect_all_asset_entries(assets_root)
    else:
        entries = git_diff_entries(previous_ref, current_ref)

    plan = build_sync_plan(entries, assets_root)
    try:
        source_summary = check_image_sources(Path(assets_root))
        check_sound_effects(Path(assets_root))
    except ValueError as exc:
        raise BuildError(str(exc)) from exc

    with TemporaryDirectory() as directory:
        for key, body in app_metadata.items():
            path = Path(directory) / Path(key).name
            path.write_bytes(body)
            plan["uploads"].append(UploadOperation(str(path), key, "generated_app_metadata").__dict__)
        return finish_release(args, current_ref, previous_ref, assets_root, plan, first_release, source_summary)


def finish_release(
    args: argparse.Namespace,
    current_ref: str,
    previous_ref: str | None,
    assets_root: str,
    plan: dict[str, Any],
    first_release: bool,
    source_summary: dict[str, int],
) -> int:
    summary = build_summary(
        current_ref=current_ref,
        previous_ref=previous_ref,
        assets_root=assets_root,
        plan=plan,
        dry_run=args.dry_run,
        first_release=first_release,
    )

    summary['image_sources'] = source_summary

    if not args.dry_run:
        try:
            purge_config = load_worker_purge_config()
            if purge_config is None:
                raise WorkerPurgeError('Selective Worker purge must be configured before publishing assets')
        except WorkerPurgeError as exc:
            raise BuildError(str(exc)) from exc
        config = load_r2_config()
        changed_keys = sorted({
            item["key"] for item in plan["uploads"] + plan["deletes"]
            if item["key"] not in RELEASE_INDEXES and not item["key"].startswith(APP_METADATA_PREFIX + "/")
        })
        try:
            apply_sync_plan(
                plan, config, args.workers, before_manifest=lambda: purge_worker_cache(purge_config, changed_keys)
            )
            index_keys = sorted({item['key'] for item in plan['uploads']} & RELEASE_INDEXES)
            if index_keys:
                purge_worker_cache(purge_config, index_keys)
        except Exception as exc:
            raise BuildError(
                'R2 sync or selective cache clearing failed. Some objects may have changed. '
                'Repair the upload failure and rerun this exact release.'
            ) from exc

    print(json.dumps(summary, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except BuildError as exc:
        print(json.dumps({"error": str(exc)}), file=sys.stderr)
        raise SystemExit(1) from exc
