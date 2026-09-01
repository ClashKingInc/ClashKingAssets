import struct

import pytest

from scindex import SCIndexError, decode_decorations, parse_scindex


def _align(value: int, alignment: int = 4) -> int:
    return (value + alignment - 1) // alignment * alignment


def _flatbuffer_table(
    field_count: int,
    *,
    integers: dict[int, int] | None = None,
    booleans: dict[int, bool] | None = None,
    strings: dict[int, str] | None = None,
) -> bytes:
    integers = integers or {}
    booleans = booleans or {}
    strings = strings or {}
    populated_fields = set(integers) | set(booleans) | set(strings)

    vtable_offset = 4
    vtable_size = 4 + field_count * 2
    table_offset = _align(vtable_offset + vtable_size)
    object_size = 4 + field_count * 4
    data = bytearray(table_offset + object_size)
    struct.pack_into("<I", data, 0, table_offset)
    struct.pack_into("<HH", data, vtable_offset, vtable_size, object_size)
    struct.pack_into("<i", data, table_offset, table_offset - vtable_offset)

    for field in populated_fields:
        relative = 4 + field * 4
        struct.pack_into("<H", data, vtable_offset + 4 + field * 2, relative)
    for field, value in integers.items():
        struct.pack_into("<I", data, table_offset + 4 + field * 4, value)
    for field, value in booleans.items():
        struct.pack_into("<B", data, table_offset + 4 + field * 4, int(value))
    for field, value in strings.items():
        while len(data) % 4:
            data.append(0)
        target = len(data)
        encoded = value.encode("utf-8")
        data.extend(struct.pack("<I", len(encoded)))
        data.extend(encoded)
        data.append(0)
        field_offset = table_offset + 4 + field * 4
        struct.pack_into("<I", data, field_offset, target - field_offset)
    return bytes(data)


def _string_block(*values: str) -> bytes:
    return b"".join(struct.pack("<I", len(value.encode())) + value.encode() for value in values)


def _row_payload(*blocks: bytes) -> bytes:
    return b"\0" + b"".join(struct.pack("<I", len(block)) + block for block in blocks)


def _global_id(value: int) -> bytes:
    return struct.pack("<QQ", value, 1 << 63)


def _scindex(values: list[bytes], paths: dict[int, str], tables: dict[str, list[tuple[int, bytes]]]) -> bytes:
    output = bytearray(struct.pack("<I16sII", 1, b"test-schema-id!!", sum(map(len, tables.values())), len(values)))
    for value in values:
        assert len(value) == 16
        output.extend(value)
    for index in range(len(values)):
        path = paths.get(index)
        output.append(int(path is not None))
        if path is not None:
            encoded = path.encode()
            output.extend(struct.pack("<I", len(encoded)))
            output.extend(encoded)
    output.extend(struct.pack("<I", len(tables)))
    for name, rows in tables.items():
        encoded_name = name.encode()
        output.extend(struct.pack("<I", len(encoded_name)))
        output.extend(encoded_name)
        output.extend(struct.pack("<I", len(rows)))
        for key_index, payload in rows:
            output.extend(struct.pack("<IQI", key_index, 0, len(payload)))
            output.extend(payload)
    return bytes(output)


def _decoration_indexes(*, movie_table_name: str = "movie-clip") -> tuple[bytes, bytes]:
    movie_uuid = bytes.fromhex("00112233445566778899aabbccddeeff")
    swf_uuid = bytes.fromhex("ffeeddccbbaa99887766554433221100")
    decoration = _scindex(
        [_global_id(18000999), movie_uuid],
        {},
        {
            "logic-deco-data": [
                (
                    0,
                    _row_payload(
                        _string_block("Test Decoration", "TID_TEST_DECORATION", ""),
                        _flatbuffer_table(1, integers={0: 1}),
                        _flatbuffer_table(
                            34,
                            integers={0: 2, 5: 4, 12: 1, 14: 500},
                            booleans={20: True, 33: True},
                            strings={13: "Diamonds"},
                        ),
                    ),
                )
            ]
        },
    )
    assets = _scindex(
        [swf_uuid, movie_uuid],
        {0: "sc/decos.sc"},
        {
            "swf": [(0, b"")],
            movie_table_name: [
                (1, _row_payload(_flatbuffer_table(2, integers={0: 0}, strings={1: "test_decoration"})))
            ],
        },
    )
    return decoration, assets


def test_decode_decorations_normalizes_metadata_and_resolves_movie_clip():
    decoration_index, asset_index = _decoration_indexes()

    assert decode_decorations(decoration_index, asset_index) == {
        "Test Decoration": {
            "GlobalID": 18000999,
            "TID": "TID_TEST_DECORATION",
            "Width": 2,
            "MaxCount": 4,
            "BuildResource": "Diamonds",
            "BuildCost": 500,
            "NotInShop": False,
            "BPReward": True,
            "VillageType": 1,
            "SWF": "sc/decos.sc",
            "ExportName": "test_decoration",
        }
    }


def test_decode_decorations_rejects_non_movie_clip_asset():
    decoration_index, asset_index = _decoration_indexes(movie_table_name="sound")

    with pytest.raises(SCIndexError, match="expected movie-clip asset, got 'sound'"):
        decode_decorations(decoration_index, asset_index)


def test_parse_scindex_rejects_mismatched_row_count():
    decoration_index, _ = _decoration_indexes()
    malformed = bytearray(decoration_index)
    struct.pack_into("<I", malformed, 20, 2)

    with pytest.raises(SCIndexError, match="header declares 2 rows"):
        parse_scindex(bytes(malformed))
