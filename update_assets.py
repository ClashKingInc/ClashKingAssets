"""
Selective asset sync pipeline:
- fetch fingerprint
- sync audio directly to R2
- download/process/upload approved image icon assets
- download/process/upload approved SC bundle outputs
"""
from __future__ import annotations

import asyncio
import os
import subprocess
from dataclasses import dataclass
from pathlib import Path

import aiohttp

from update_utils import (
    apk_url,
    build_r2_client,
    download_file,
    fetch_fingerprint,
    load_r2_existing_keys_for_prefixes,
    upload_bytes_to_r2,
    upload_file_to_r2,
)


@dataclass(frozen=True)
class FingerprintSelection:
    audio: tuple[str, ...]
    image_sctx: tuple[str, ...]
    sc_bundle_inputs: tuple[str, ...]
    sc_main_files: tuple[str, ...]


class AssetsUpdater:
    IMAGE_ROOTS = (
        "image/background_icons/",
        "image/skin_icons/",
        "image/theme_icons/",
    )
    AUDIO_ROOTS = ("music/", "sfx/")
    SC_ROOT = "sc"
    SC_INCLUDE_PREFIXES = ("buildings", "characters", "chr", "info", "ui")
    MEDIA_SUFFIXES = {".png", ".webp"}

    def __init__(self):
        self.base_dir = Path(__file__).resolve().parent
        self.download_concurrency = int(os.getenv("ASSET_DOWNLOAD_CONCURRENCY", "32"))
        self.upload_concurrency = int(os.getenv("ASSET_UPLOAD_CONCURRENCY", "32"))
        self.render_scale = os.getenv("SC_RENDER_SCALE", "2")
        self.sc_workers = int(os.getenv("SC_WORKERS", str(os.cpu_count() or 1)))
        self.sc_file_concurrency = int(os.getenv("SC_FILE_CONCURRENCY", "1"))
        self.sc_retry_workers = int(os.getenv("SC_RETRY_WORKERS", "2"))
        self.sc_profile = os.getenv("SC_PROFILE", "true").strip().lower() in {"1", "true", "yes", "on"}
        self.sc_profile_top_n = int(os.getenv("SC_PROFILE_TOP_N", "5"))
        self.skip_tiny_threshold = int(os.getenv("SC_SKIP_TINY_THRESHOLD", "5"))

        self.r2_prefix = os.getenv("R2_PREFIX", "").strip("/")
        self.r2_bucket = os.getenv("R2_BUCKET", "")
        self.r2_client = build_r2_client(self.r2_bucket)
        self._r2_existing_keys: set[str] = set()

        self.fingerprint = os.getenv("FINGERPRINT", "")
        self.apk_url = apk_url()

    def build_go_command(
        self,
        *,
        process_image_root: str | None = None,
        process_sc_root: str | None = None,
        input_path: str | None = None,
        out_dir: str | None = None,
        delete_source: bool = False,
        delete_sctx: bool = False,
        include_prefixes: tuple[str, ...] = (),
        workers_override: int | None = None,
        file_concurrency_override: int | None = None,
    ) -> list[str]:
        command = [
            "go",
            "run",
            "main.go",
            "--workers",
            str(max(1, workers_override or self.sc_workers)),
            "--file-concurrency",
            str(max(1, file_concurrency_override or self.sc_file_concurrency)),
            "--render-scale",
            self.render_scale,
            "--skip-tiny-threshold",
            str(self.skip_tiny_threshold),
        ]
        if self.sc_profile:
            command.extend(["--profile", "--profile-top-n", str(max(1, self.sc_profile_top_n))])
        for prefix in include_prefixes:
            command.extend(["--include-prefix", prefix])
        if process_image_root:
            command.extend(["--process-image-root", process_image_root])
        if process_sc_root:
            command.extend(["--process-sc-root", process_sc_root])
        if out_dir:
            command.extend(["--out", out_dir])
        if delete_source:
            command.append("--delete-source")
        if delete_sctx:
            command.append("--delete-sctx")
        if input_path:
            command.append(input_path)
        return command

    def ensure_r2_configured(self):
        if self.r2_client is None or not self.r2_bucket:
            raise RuntimeError("R2 is not configured in .env")

    def is_allowed_audio(self, file_path: str) -> bool:
        return file_path.startswith(self.AUDIO_ROOTS)

    def is_allowed_image_sctx(self, file_path: str) -> bool:
        return file_path.startswith(self.IMAGE_ROOTS) and file_path.endswith(".sctx")

    def is_allowed_sc_input(self, file_path: str) -> bool:
        path = Path(file_path)
        if path.parent.as_posix() != self.SC_ROOT:
            return False
        if path.suffix.lower() not in {".sc", ".sctx"}:
            return False
        return path.name.startswith(self.SC_INCLUDE_PREFIXES)

    def classify_fingerprint(self, fingerprint_file: dict) -> FingerprintSelection:
        audio: list[str] = []
        image_sctx: list[str] = []
        sc_bundle_inputs: list[str] = []
        sc_main_files: list[str] = []

        for file_data in fingerprint_file.get("files", []):
            file_path = file_data.get("file")
            if not file_path:
                continue
            if self.is_allowed_audio(file_path):
                audio.append(file_path)
                continue
            if self.is_allowed_image_sctx(file_path):
                image_sctx.append(file_path)
                continue
            if self.is_allowed_sc_input(file_path):
                sc_bundle_inputs.append(file_path)
                if file_path.endswith(".sc") and not file_path.endswith("_tex.sc"):
                    sc_main_files.append(file_path)

        return FingerprintSelection(
            audio=tuple(sorted(audio)),
            image_sctx=tuple(sorted(image_sctx)),
            sc_bundle_inputs=tuple(sorted(sc_bundle_inputs)),
            sc_main_files=tuple(sorted(sc_main_files)),
        )

    def image_output_key(self, file_path: str) -> str:
        return file_path.removesuffix(".sctx") + ".png"

    def sc_output_dirs(self, sc_main_files: tuple[str, ...]) -> tuple[Path, ...]:
        output_dirs = {self.base_dir / Path(file_path).with_suffix("") for file_path in sc_main_files}
        return tuple(sorted(output_dirs))

    def preload_r2_existing_keys(self):
        prefixes = (*self.AUDIO_ROOTS, *self.IMAGE_ROOTS)
        self._r2_existing_keys = load_r2_existing_keys_for_prefixes(
            self.r2_client,
            self.r2_bucket,
            self.r2_prefix,
            prefixes,
        )
        print(f"[Assets] Loaded {len(self._r2_existing_keys)} existing R2 keys for scoped prefixes", flush=True)

    def r2_key_exists_cached(self, key: str) -> bool:
        return key in self._r2_existing_keys

    def mark_uploaded(self, key: str):
        self._r2_existing_keys.add(key)

    async def download_bytes(self, session: aiohttp.ClientSession, url: str) -> bytes:
        async with session.get(url) as response:
            response.raise_for_status()
            return await response.read()

    async def download_to_disk(self, session: aiohttp.ClientSession, base_url: str, file_path: str):
        data = await self.download_bytes(session, f"{base_url}/{file_path}")
        local_path = self.base_dir / file_path
        local_path.parent.mkdir(parents=True, exist_ok=True)
        local_path.write_bytes(data)

    async def download_many_to_disk(self, session: aiohttp.ClientSession, base_url: str, file_paths: tuple[str, ...]):
        if not file_paths:
            return

        semaphore = asyncio.Semaphore(max(1, min(self.download_concurrency, len(file_paths))))

        async def worker(file_path: str):
            async with semaphore:
                await self.download_to_disk(session, base_url, file_path)

        await asyncio.gather(*(worker(file_path) for file_path in file_paths))

    async def sync_audio_file(self, session: aiohttp.ClientSession, base_url: str, file_path: str) -> str:
        if self.r2_key_exists_cached(file_path):
            return "skipped"

        data = await self.download_bytes(session, f"{base_url}/{file_path}")
        await asyncio.to_thread(
            upload_bytes_to_r2,
            self.r2_client,
            self.r2_bucket,
            self.r2_prefix,
            data,
            file_path,
        )
        self.mark_uploaded(file_path)
        return "uploaded"

    async def sync_audio(self, session: aiohttp.ClientSession, base_url: str, audio_files: tuple[str, ...]):
        print(f"[Assets] Audio candidates: {len(audio_files)}", flush=True)
        if not audio_files:
            return

        semaphore = asyncio.Semaphore(max(1, min(self.download_concurrency, len(audio_files))))

        async def worker(file_path: str) -> str:
            async with semaphore:
                return await self.sync_audio_file(session, base_url, file_path)

        results = await asyncio.gather(*(worker(file_path) for file_path in audio_files))
        uploaded = sum(1 for result in results if result == "uploaded")
        skipped = len(results) - uploaded
        print(f"[Assets] Audio complete: uploaded={uploaded} skipped={skipped}", flush=True)

    def process_image_dirs(self, image_dirs: tuple[str, ...]):
        for image_dir in image_dirs:
            local_dir = self.base_dir / image_dir
            if not local_dir.exists():
                continue
            print(f"[Assets] Process image root: {image_dir}", flush=True)
            subprocess.run(
                self.build_go_command(process_image_root=image_dir, delete_source=True),
                check=True,
                cwd=self.base_dir,
            )

    async def sync_images(self, session: aiohttp.ClientSession, base_url: str, image_files: tuple[str, ...]):
        print(f"[Assets] Image candidates: {len(image_files)}", flush=True)
        if not image_files:
            return

        pending = []
        skipped = 0
        for file_path in image_files:
            output_key = self.image_output_key(file_path)
            if self.r2_key_exists_cached(output_key):
                skipped += 1
                continue
            pending.append(file_path)

        if not pending:
            print(f"[Assets] Image complete: uploaded=0 skipped={skipped} tiny=0", flush=True)
            return

        pending_files = tuple(sorted(pending))
        await self.download_many_to_disk(session, base_url, pending_files)
        image_dirs = tuple(sorted({Path(file_path).parent.as_posix() for file_path in pending_files}))
        self.process_image_dirs(image_dirs)

        uploaded = 0
        tiny_skipped = 0
        upload_jobs: list[tuple[Path, str]] = []
        for file_path in pending_files:
            output_key = self.image_output_key(file_path)
            local_png = self.base_dir / output_key
            if not local_png.exists():
                tiny_skipped += 1
                continue
            upload_jobs.append((local_png, output_key))

        async def upload_job(local_png: Path, output_key: str):
            await asyncio.to_thread(
                upload_file_to_r2,
                self.r2_client,
                self.r2_bucket,
                self.r2_prefix,
                local_png,
                output_key,
            )
            self.mark_uploaded(output_key)

        if upload_jobs:
            semaphore = asyncio.Semaphore(max(1, min(self.upload_concurrency, len(upload_jobs))))

            async def worker(local_png: Path, output_key: str):
                async with semaphore:
                    await upload_job(local_png, output_key)

            await asyncio.gather(*(worker(local_png, output_key) for local_png, output_key in upload_jobs))
            uploaded = len(upload_jobs)

        print(
            f"[Assets] Image complete: uploaded={uploaded} skipped={skipped} tiny={tiny_skipped}",
            flush=True,
        )

    def process_sc_root(self, sc_main_files: tuple[str, ...]):
        if not sc_main_files:
            print("[Assets] No SC main files selected", flush=True)
            return

        local_root = self.base_dir / self.SC_ROOT
        if not local_root.exists():
            return

        command = self.build_go_command(
            process_sc_root=self.SC_ROOT,
            delete_source=True,
            delete_sctx=True,
            include_prefixes=self.SC_INCLUDE_PREFIXES,
        )
        print(f"[Assets] Process SC root: {self.SC_ROOT}", flush=True)
        print(
            "[Assets] SC settings:"
            f" workers={self.sc_workers}"
            f" file_concurrency={self.sc_file_concurrency}"
            f" profile={'on' if self.sc_profile else 'off'}"
            f" profile_top_n={max(1, self.sc_profile_top_n)}",
            flush=True,
        )
        try:
            subprocess.run(command, check=True, cwd=self.base_dir)
        except subprocess.CalledProcessError:
            retry_workers = max(1, min(self.sc_retry_workers, self.sc_workers))
            retry_file_concurrency = 1
            if retry_workers == self.sc_workers and retry_file_concurrency == self.sc_file_concurrency:
                raise

            print(
                "[Assets] SC processing failed; retrying with reduced concurrency:"
                f" workers={retry_workers} file_concurrency={retry_file_concurrency}",
                flush=True,
            )
            retry_command = self.build_go_command(
                process_sc_root=self.SC_ROOT,
                delete_source=True,
                delete_sctx=True,
                include_prefixes=self.SC_INCLUDE_PREFIXES,
                workers_override=retry_workers,
                file_concurrency_override=retry_file_concurrency,
            )
            subprocess.run(retry_command, check=True, cwd=self.base_dir)

    async def upload_sc_outputs(self, sc_main_files: tuple[str, ...]) -> int:
        upload_jobs: list[tuple[Path, str]] = []
        for output_dir in self.sc_output_dirs(sc_main_files):
            if not output_dir.exists():
                continue
            for local_path in sorted(path for path in output_dir.rglob("*") if path.is_file()):
                if local_path.suffix.lower() not in self.MEDIA_SUFFIXES:
                    continue
                key = local_path.relative_to(self.base_dir).as_posix()
                upload_jobs.append((local_path, key))

        if not upload_jobs:
            return 0

        semaphore = asyncio.Semaphore(max(1, min(self.upload_concurrency, len(upload_jobs))))

        async def worker(local_path: Path, key: str):
            async with semaphore:
                await asyncio.to_thread(
                    upload_file_to_r2,
                    self.r2_client,
                    self.r2_bucket,
                    self.r2_prefix,
                    local_path,
                    key,
                )

        await asyncio.gather(*(worker(local_path, key) for local_path, key in upload_jobs))
        return len(upload_jobs)

    async def sync_sc(
        self,
        session: aiohttp.ClientSession,
        base_url: str,
        sc_bundle_inputs: tuple[str, ...],
        sc_main_files: tuple[str, ...],
    ):
        print(
            f"[Assets] SC candidates: bundle_inputs={len(sc_bundle_inputs)} main_files={len(sc_main_files)}",
            flush=True,
        )
        if not sc_bundle_inputs or not sc_main_files:
            return

        await self.download_many_to_disk(session, base_url, sc_bundle_inputs)
        self.process_sc_root(sc_main_files)
        uploaded = await self.upload_sc_outputs(sc_main_files)
        print(f"[Assets] SC complete: uploaded={uploaded}", flush=True)

    async def run_pipeline(self):
        self.ensure_r2_configured()
        await asyncio.to_thread(self.preload_r2_existing_keys)

        if not self.fingerprint:
            self.fingerprint = await fetch_fingerprint(self.apk_url)

        base_url = f"https://game-assets.clashofclans.com/{self.fingerprint}"
        fingerprint_file = await download_file(url=f"{base_url}/fingerprint.json", as_json=True)
        selection = self.classify_fingerprint(fingerprint_file)

        print(
            "[Assets] Selection:"
            f" audio={len(selection.audio)}"
            f" image_sctx={len(selection.image_sctx)}"
            f" sc_bundle_inputs={len(selection.sc_bundle_inputs)}"
            f" sc_main_files={len(selection.sc_main_files)}",
            flush=True,
        )

        timeout = aiohttp.ClientTimeout(total=None, sock_connect=60, sock_read=600)
        async with aiohttp.ClientSession(timeout=timeout) as session:
            await self.sync_audio(session, base_url, selection.audio)
            await self.sync_images(session, base_url, selection.image_sctx)
            await self.sync_sc(session, base_url, selection.sc_bundle_inputs, selection.sc_main_files)

    def run(self):
        asyncio.run(self.run_pipeline())


if __name__ == "__main__":
    AssetsUpdater().run()
