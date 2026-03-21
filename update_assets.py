"""
Downloads the asset files needed for later processing/storage steps.
"""
import asyncio
import logging
import os
from pathlib import Path

import aiohttp

from update_utils import apk_url, build_r2_client, download_file, fetch_fingerprint, load_r2_existing_keys


class AssetsUpdater:
    def __init__(self):
        self.DOWNLOAD_CONCURRENCY = int(os.getenv("ASSET_DOWNLOAD_CONCURRENCY", "32"))
        self.R2_SKIP_IMAGE_PREFIXES = (
            "image/background_icons/",
            "image/skin_icons/",
            "image/theme_icons/",
        )
        self.R2_SKIP_FILE_PREFIXES = ("music/", "sfx/")
        self.R2_PREFIX = os.getenv("R2_PREFIX", "").strip("/")
        self.r2_bucket = os.getenv("R2_BUCKET", "")
        self.r2_client = build_r2_client(self.r2_bucket)
        self.r2_existing_keys: set[str] = set()

        self.FINGERPRINT = os.getenv("FINGERPRINT", "")
        self.APK_URL = apk_url()

    def should_download_file(self, file_path: str) -> bool:
        if file_path.startswith("image/"):
            return file_path.endswith(".sctx")
        if file_path.startswith(("sc/", "ui/sc/")):
            skip_these = ["background_", "april25", "sc/clouds",
                "hero_rc", "hero_mp", "hero_gw", "hero_bk", "hero_aq", "hero_dd", "scenery_"
            ]
            if any(i in file_path for i in skip_these):
                return False
            return file_path.endswith(".sc") or file_path.endswith(".sctx")
        if file_path.startswith(("music/", "sfx/")):
            return True
        return False

    def r2_key_for_file(self, file_path: str) -> str | None:
        if file_path.startswith(self.R2_SKIP_IMAGE_PREFIXES) and file_path.endswith(".sctx"):
            return file_path.removesuffix(".sctx") + ".png"
        if file_path.startswith(self.R2_SKIP_FILE_PREFIXES):
            return file_path
        return None

    async def should_skip_download(self, file_path: str) -> bool:
        key = self.r2_key_for_file(file_path)
        if key is None:
            return False

        if key not in self.r2_existing_keys:
            return False

        local_path = Path(file_path)
        if local_path.exists():
            try:
                local_path.unlink()
            except OSError as e:
                logging.warning(f"Could not delete {local_path}: {e}")
        print(f"Skipping download for {file_path}; already present in R2 as {key}")
        return True

    async def download_asset_file(self, session: aiohttp.ClientSession, base_url: str, file_path: str):
        download_url = f"{base_url}/{file_path}"
        print(f"Downloading: {download_url}")
        async with session.get(download_url) as response:
            response.raise_for_status()
            data = await response.read()

        local_path = Path(file_path)
        local_path.parent.mkdir(parents=True, exist_ok=True)
        with open(local_path, "wb") as f:
            f.write(data)

        print(f"Downloaded: {file_path}")

    async def download_files(self):
        if not self.FINGERPRINT:
            self.FINGERPRINT = await fetch_fingerprint(self.APK_URL)
        if self.r2_client is not None and self.r2_bucket:
            self.r2_existing_keys = await asyncio.to_thread(load_r2_existing_keys, self.r2_client, self.r2_bucket, self.R2_PREFIX)
            print(f"Loaded {len(self.r2_existing_keys)} existing R2 keys")

        base_url = f"https://game-assets.clashofclans.com/{self.FINGERPRINT}"
        fingerprint_file = await download_file(url=f"{base_url}/fingerprint.json", as_json=True)
        pending_downloads: list[str] = []

        for file_data in fingerprint_file.get("files", []):
            file_path: str = file_data["file"]

            if not self.should_download_file(file_path):
                continue
            if await self.should_skip_download(file_path):
                continue
            pending_downloads.append(file_path)

        if not pending_downloads:
            return

        concurrency = max(1, min(self.DOWNLOAD_CONCURRENCY, len(pending_downloads)))
        queue: asyncio.Queue[str] = asyncio.Queue()
        for file_path in pending_downloads:
            queue.put_nowait(file_path)

        async with aiohttp.ClientSession() as session:
            async def worker():
                while True:
                    try:
                        file_path = queue.get_nowait()
                    except asyncio.QueueEmpty:
                        return
                    try:
                        await self.download_asset_file(session, base_url, file_path)
                    finally:
                        queue.task_done()

            await asyncio.gather(*(worker() for _ in range(concurrency)))

    def run(self):
        asyncio.run(self.download_files())


if __name__ == "__main__":
    AssetsUpdater().run()
