import hashlib
import struct

import pytest

from scattachment import SCAttachmentError, parse_scattachment_header, unwrap_scattachment


def _attachment(path: str, payload: bytes, *, offset: int = 32) -> bytes:
    encoded_path = path.encode()
    assert 12 + len(encoded_path) <= offset
    padding = bytes(offset - 12 - len(encoded_path))
    return struct.pack("<III", 1, offset, len(encoded_path)) + encoded_path + padding + payload


def test_scattachment_header_and_payload_are_decoded():
    attachment = _attachment("sc/decos.sc", b"SC\x06payload", offset=24)

    assert parse_scattachment_header(attachment[:24]) == ("sc/decos.sc", 24)
    assert unwrap_scattachment(
        attachment,
        expected_path="sc/decos.sc",
        expected_sha=hashlib.sha1(attachment).hexdigest(),
    ) == b"SC\x06payload"


@pytest.mark.parametrize("path", ["../decos.sc", "/sc/decos.sc", "sc\\decos.sc"])
def test_scattachment_rejects_unsafe_paths(path):
    attachment = _attachment(path, b"payload")

    with pytest.raises(SCAttachmentError, match="unsafe embedded path"):
        parse_scattachment_header(attachment)


def test_scattachment_rejects_checksum_mismatch():
    attachment = _attachment("sc/decos.sc", b"payload")

    with pytest.raises(SCAttachmentError, match="SHA-1 mismatch"):
        unwrap_scattachment(
            attachment,
            expected_path="sc/decos.sc",
            expected_sha="0" * 40,
        )
