import json
import os
import sys
import zipfile
from typing import Any, overload, Literal

import aiohttp
from dotenv import load_dotenv

load_dotenv()


def apk_url() -> str:
    return "https://d.apkpure.net/b/APK/com.supercell.clashofclans?version=latest"


@overload
async def download_file(url: str, as_json: Literal[True]) -> Any: ...


@overload
async def download_file(url: str, as_json: Literal[False] = False, show_progress: bool = False) -> bytes: ...


async def download_file(url: str, as_json: bool = False, show_progress: bool = False) -> Any | bytes:
    async with aiohttp.request("GET", url) as response:
        if as_json:
            body = await response.text()
            if response.status >= 400:
                snippet = body[:300].replace("\n", " ")
                raise RuntimeError(f"HTTP {response.status} while fetching JSON from {url}: {snippet}")
            try:
                return json.loads(body)
            except json.JSONDecodeError as exc:
                raise RuntimeError(f"Invalid JSON response from {url}") from exc
        if response.status >= 400:
            body = await response.text()
            snippet = body[:300].replace("\n", " ")
            raise RuntimeError(f"HTTP {response.status} while fetching bytes from {url}: {snippet}")

        if not show_progress:
            return await response.read()

        total_header = response.headers.get("Content-Length")
        total_bytes = int(total_header) if total_header and total_header.isdigit() else 0
        downloaded = 0
        chunks: list[bytes] = []
        filename = url.rsplit("/", 1)[-1] or "download"

        async for chunk in response.content.iter_chunked(128 * 1024):
            chunks.append(chunk)
            downloaded += len(chunk)
            if total_bytes > 0:
                pct = (downloaded / total_bytes) * 100
                sys.stdout.write(
                    f"\rDownloading {filename}: {pct:6.2f}% ({downloaded:,}/{total_bytes:,} bytes)"
                )
            else:
                sys.stdout.write(f"\rDownloading {filename}: {downloaded:,} bytes")
            sys.stdout.flush()

        sys.stdout.write("\n")
        return b"".join(chunks)


async def fetch_fingerprint(apk_url: str) -> str:
    data = await download_file(apk_url, show_progress=True)
    if not isinstance(data, bytes):
        raise TypeError("Expected bytes when downloading APK")

    with open("apk.zip", "wb") as f:
        f.write(data)
    with zipfile.ZipFile("apk.zip") as zf:
        with zf.open("assets/fingerprint.json") as fp:
            fingerprint = json.loads(fp.read())["sha"]

    os.remove("apk.zip")
    return fingerprint
