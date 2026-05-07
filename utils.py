import json
import os
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
async def download_file(url: str, as_json: Literal[False] = False) -> bytes: ...


async def download_file(url: str, as_json: bool = False) -> Any | bytes:
    async with aiohttp.request("GET", url) as response:
        if as_json:
            return await response.json()
        return await response.read()


async def fetch_fingerprint(apk_url: str) -> str:
    data = await download_file(apk_url)
    if not isinstance(data, bytes):
        raise TypeError("Expected bytes when downloading APK")

    with open("apk.zip", "wb") as f:
        f.write(data)
    zf = zipfile.ZipFile("apk.zip")
    with zf.open("assets/fingerprint.json") as fp:
        fingerprint = json.loads(fp.read())["sha"]

    os.remove("apk.zip")
    return fingerprint
