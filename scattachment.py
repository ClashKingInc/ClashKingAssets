from __future__ import annotations

import asyncio
import hashlib
import string
import struct
from dataclasses import dataclass
from pathlib import PurePosixPath

import aiohttp

ATTACHMENT_PREFIX = "attachments/"
HEADER_PROBE_SIZE = 512
MAX_HEADER_SIZE = 4096


class SCAttachmentError(ValueError):
    """Raised when an SC attachment header or payload is invalid."""


@dataclass(frozen=True)
class SCAttachmentRef:
    embedded_path: str
    remote_path: str
    sha: str
    defer: bool
    payload_offset: int


@dataclass(frozen=True)
class SCAttachmentIndex:
    by_path: dict[str, SCAttachmentRef]
    failures: tuple[str, ...]


def _header_size(data: bytes, *, label: str) -> int:
    if len(data) < 12:
        raise SCAttachmentError(f"{label}: expected a 12-byte header")
    version, payload_offset, path_length = struct.unpack_from("<III", data)
    if version != 1:
        raise SCAttachmentError(f"{label}: unsupported version {version}")
    minimum_offset = 12 + path_length
    if path_length == 0:
        raise SCAttachmentError(f"{label}: embedded path is empty")
    if payload_offset < minimum_offset:
        raise SCAttachmentError(
            f"{label}: payload offset {payload_offset} overlaps the {path_length}-byte path"
        )
    if payload_offset > MAX_HEADER_SIZE:
        raise SCAttachmentError(f"{label}: header size {payload_offset} exceeds {MAX_HEADER_SIZE} bytes")
    return payload_offset


def parse_scattachment_header(data: bytes, *, label: str = "scattachment") -> tuple[str, int]:
    payload_offset = _header_size(data, label=label)
    if len(data) < payload_offset:
        raise SCAttachmentError(
            f"{label}: expected {payload_offset} header bytes, but received {len(data)}"
        )
    path_length = struct.unpack_from("<I", data, 8)[0]
    try:
        embedded_path = data[12 : 12 + path_length].decode("utf-8")
    except UnicodeDecodeError as exc:
        raise SCAttachmentError(f"{label}: embedded path is not valid UTF-8") from exc

    path = PurePosixPath(embedded_path)
    if (
        path.is_absolute()
        or not path.parts
        or any(part in {"", ".", ".."} for part in path.parts)
        or "\\" in embedded_path
        or path.as_posix() != embedded_path
    ):
        raise SCAttachmentError(f"{label}: unsafe embedded path {embedded_path!r}")
    if any(data[12 + path_length : payload_offset]):
        raise SCAttachmentError(f"{label}: header padding contains non-zero bytes")
    return embedded_path, payload_offset


def unwrap_scattachment(
    data: bytes,
    *,
    expected_path: str,
    expected_sha: str,
    label: str = "scattachment",
) -> bytes:
    if len(expected_sha) != 40 or any(char not in string.hexdigits for char in expected_sha):
        raise SCAttachmentError(f"{label}: expected SHA-1 is invalid")
    actual_sha = hashlib.sha1(data).hexdigest()
    if actual_sha != expected_sha.lower():
        raise SCAttachmentError(f"{label}: SHA-1 mismatch: expected {expected_sha}, got {actual_sha}")
    embedded_path, payload_offset = parse_scattachment_header(data, label=label)
    if embedded_path != expected_path:
        raise SCAttachmentError(
            f"{label}: expected embedded path {expected_path!r}, got {embedded_path!r}"
        )
    if payload_offset == len(data):
        raise SCAttachmentError(f"{label}: payload is empty")
    return data[payload_offset:]


async def discover_scattachments(
    base_url: str,
    manifest_files: list[dict],
    *,
    concurrency: int = 64,
) -> SCAttachmentIndex:
    attachment_files = [item for item in manifest_files if item.get("file", "").startswith(ATTACHMENT_PREFIX)]
    if not attachment_files:
        return SCAttachmentIndex(by_path={}, failures=())
    if concurrency < 1:
        raise ValueError("attachment concurrency must be at least 1")

    semaphore = asyncio.Semaphore(concurrency)
    timeout = aiohttp.ClientTimeout(total=30)

    async with aiohttp.ClientSession(timeout=timeout) as session:
        async def inspect(item: dict) -> SCAttachmentRef:
            remote_path = item["file"]
            label = remote_path
            async with semaphore:
                url = f"{base_url}/{remote_path}"
                async with session.get(url, headers={"Range": f"bytes=0-{HEADER_PROBE_SIZE - 1}"}) as response:
                    response.raise_for_status()
                    data = await response.content.read(HEADER_PROBE_SIZE)
                header_size = _header_size(data, label=label)
                if header_size > len(data):
                    async with session.get(url, headers={"Range": f"bytes=0-{header_size - 1}"}) as response:
                        response.raise_for_status()
                        data = await response.content.read(header_size)

            embedded_path, payload_offset = parse_scattachment_header(data, label=label)
            return SCAttachmentRef(
                embedded_path=embedded_path,
                remote_path=remote_path,
                sha=item.get("sha", ""),
                defer=bool(item.get("defer", False)),
                payload_offset=payload_offset,
            )

        results = await asyncio.gather(*(inspect(item) for item in attachment_files), return_exceptions=True)

    by_path = {}
    failures = []
    for item, result in zip(attachment_files, results, strict=True):
        if isinstance(result, BaseException):
            failures.append(f"{item.get('file')}: {result}")
            continue
        if result.embedded_path in by_path:
            raise SCAttachmentError(f"duplicate embedded attachment path {result.embedded_path!r}")
        by_path[result.embedded_path] = result
    return SCAttachmentIndex(by_path=by_path, failures=tuple(failures))
