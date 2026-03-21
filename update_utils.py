import json
import os
import zipfile
from pathlib import Path

import aiohttp
from dotenv import load_dotenv
import boto3
load_dotenv()


def apk_url() -> str:
    return f"https://d.apkpure.net/b/APK/com.supercell.clashofclans?version=latest"


async def download_file(url: str, as_json: bool = False):
    async with aiohttp.request("GET", url) as response:
        if as_json:
            return await response.json()
        return await response.read()


async def fetch_fingerprint(apk_url: str) -> str:
    data = await download_file(apk_url)

    with open("apk.zip", "wb") as f:
        f.write(data)
    zf = zipfile.ZipFile("apk.zip")
    with zf.open("assets/fingerprint.json") as fp:
        fingerprint = json.loads(fp.read())["sha"]

    os.remove("apk.zip")
    return fingerprint


def build_r2_client(bucket: str):
    endpoint_url = os.getenv("R2_ENDPOINT_URL", "")
    account_id = os.getenv("R2_ACCOUNT_ID", "")
    access_key = os.getenv("R2_ACCESS_KEY_ID", "")
    secret_key = os.getenv("R2_SECRET_ACCESS_KEY", "")

    if not endpoint_url and account_id:
        endpoint_url = f"https://{account_id}.r2.cloudflarestorage.com"

    if not all([endpoint_url, access_key, secret_key, bucket]):
        return None

    return boto3.client(
        "s3",
        endpoint_url=endpoint_url,
        aws_access_key_id=access_key,
        aws_secret_access_key=secret_key,
        region_name="auto",
    )


def prefixed_r2_key(key: str, prefix: str) -> str:
    if not prefix:
        return key
    return f"{prefix}/{key}"


def load_r2_existing_keys(client, bucket: str, prefix: str) -> set[str]:
    if client is None or not bucket:
        return set()

    paginator = client.get_paginator("list_objects_v2")
    bucket_prefix = f"{prefix}/" if prefix else ""
    keys: set[str] = set()

    for page in paginator.paginate(Bucket=bucket, Prefix=bucket_prefix):
        for item in page.get("Contents", []):
            key = item["Key"]
            if bucket_prefix and key.startswith(bucket_prefix):
                key = key[len(bucket_prefix):]
            keys.add(key)
    return keys


def upload_file_to_r2(client, bucket: str, prefix: str, local_path: Path, key: str | None = None):
    if client is None or not bucket:
        return

    upload_key = key or local_path.as_posix()
    client.upload_file(
        Filename=str(local_path),
        Bucket=bucket,
        Key=prefixed_r2_key(upload_key, prefix),
    )
