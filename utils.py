import json
import string
import tempfile
import zipfile

import aiohttp
from dotenv import load_dotenv

load_dotenv()


def apk_url() -> str:
    return "https://d.apkpure.net/b/XAPK/com.supercell.clashofclans?version=latest"


async def download_file(url: str, as_json: bool = False):
    async with aiohttp.request("GET", url) as response:
        response.raise_for_status()
        if as_json:
            return await response.json()
        return await response.read()


def _read_fingerprint(archive: zipfile.ZipFile) -> str:
    try:
        fingerprint_file = archive.open("assets/fingerprint.json")
    except KeyError:
        fingerprint_file = None

    if fingerprint_file is not None:
        with fingerprint_file:
            fingerprint = json.load(fingerprint_file)
        return _validate_fingerprint(fingerprint)

    try:
        manifest = json.loads(archive.read("manifest.json"))
    except KeyError as exc:
        raise FileNotFoundError("package does not contain assets/fingerprint.json or an XAPK manifest") from exc

    asset_packs = [
        split["file"]
        for split in manifest.get("split_apks", [])
        if "asset_pack" in split.get("id", "") and split.get("file")
    ]
    for asset_pack in asset_packs:
        try:
            with archive.open(asset_pack) as asset_pack_file:
                with zipfile.ZipFile(asset_pack_file) as asset_archive:
                    return _read_fingerprint(asset_archive)
        except (FileNotFoundError, KeyError, zipfile.BadZipFile):
            continue

    raise FileNotFoundError("XAPK asset packs do not contain assets/fingerprint.json")


def _validate_fingerprint(fingerprint: dict) -> str:
    sha = fingerprint.get("sha")
    if not isinstance(sha, str) or len(sha) != 40 or any(char not in string.hexdigits for char in sha):
        raise ValueError("fingerprint sha must be a 40-character hexadecimal string")
    return sha.lower()


async def fetch_fingerprint(package_url: str) -> str:
    with tempfile.SpooledTemporaryFile(max_size=64 * 1024 * 1024) as package_file:
        async with aiohttp.request("GET", package_url) as response:
            response.raise_for_status()
            async for chunk in response.content.iter_chunked(1024 * 1024):
                package_file.write(chunk)

        package_file.seek(0)
        with zipfile.ZipFile(package_file) as package_archive:
            return _read_fingerprint(package_archive)


async def fetch_fingerprint_manifest(package_url: str, fingerprint: str = "") -> tuple[str, dict]:
    if fingerprint:
        try:
            manifest = await download_file(
                f"https://game-assets.clashofclans.com/{fingerprint}/fingerprint.json", as_json=True
            )
            return fingerprint, manifest
        except aiohttp.ClientResponseError as exc:
            if exc.status not in {403, 404}:
                raise

    fingerprint = await fetch_fingerprint(package_url)
    manifest = await download_file(
        f"https://game-assets.clashofclans.com/{fingerprint}/fingerprint.json", as_json=True
    )
    return fingerprint, manifest
