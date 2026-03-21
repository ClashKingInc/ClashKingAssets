"""
Uploads locally processed assets into R2 storage.

Current scope:
- image/**
- music/**
- sfx/**
"""
import asyncio
import os
from pathlib import Path

from update_utils import build_r2_client, load_r2_existing_keys, upload_file_to_r2


class StorageUpdater:
    def __init__(self):
        self.R2_PREFIX = os.getenv("R2_PREFIX", "").strip("/")
        self.r2_bucket = os.getenv("R2_BUCKET", "")
        self.r2_client = build_r2_client(self.r2_bucket)
        self.r2_existing_keys: set[str] = set()
        self.roots = (Path("image"), Path("music"), Path("sfx"))

    def iter_local_files(self):
        for root in self.roots:
            if not root.exists():
                continue
            for path in sorted(candidate for candidate in root.rglob("*") if candidate.is_file()):
                yield path

    async def upload_files(self):
        if self.r2_client is None or not self.r2_bucket:
            raise RuntimeError("R2 is not configured in .env")

        print("Loading existing R2 keys...", flush=True)
        self.r2_existing_keys = await asyncio.to_thread(
            load_r2_existing_keys,
            self.r2_client,
            self.r2_bucket,
            self.R2_PREFIX,
        )
        print(f"Loaded {len(self.r2_existing_keys)} existing R2 keys")
        print("Scanning local storage roots...", flush=True)

        for local_path in self.iter_local_files():
            key = local_path.as_posix()
            if key in self.r2_existing_keys:
                continue
            await asyncio.to_thread(
                upload_file_to_r2,
                self.r2_client,
                self.r2_bucket,
                self.R2_PREFIX,
                local_path,
                key,
            )
            self.r2_existing_keys.add(key)
            print(f"Uploaded: {key}")

    def run(self):
        asyncio.run(self.upload_files())


if __name__ == "__main__":
    StorageUpdater().run()
