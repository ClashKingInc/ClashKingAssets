from __future__ import annotations

import struct
from dataclasses import dataclass

UINT32_MAX = (1 << 32) - 1


class SCIndexError(ValueError):
    """Raised when an SC index is malformed or has an unsupported shape."""


class _Reader:
    def __init__(self, data: bytes, label: str):
        self.data = data
        self.label = label
        self.position = 0

    def _take(self, size: int) -> bytes:
        if size < 0 or self.position + size > len(self.data):
            raise SCIndexError(
                f"{self.label}: expected {size} bytes at offset {self.position}, "
                f"but the input is {len(self.data)} bytes"
            )
        value = self.data[self.position : self.position + size]
        self.position += size
        return value

    def uint8(self) -> int:
        return self._take(1)[0]

    def uint32(self) -> int:
        return struct.unpack("<I", self._take(4))[0]

    def uint64(self) -> int:
        return struct.unpack("<Q", self._take(8))[0]

    def sized_bytes(self) -> bytes:
        return self._take(self.uint32())

    def finish(self) -> None:
        if self.position != len(self.data):
            raise SCIndexError(
                f"{self.label}: {len(self.data) - self.position} trailing bytes at offset {self.position}"
            )


@dataclass(frozen=True)
class SCIndexRow:
    table_name: str
    key_index: int
    checksum: int
    payload: bytes


@dataclass(frozen=True)
class SCIndex:
    version: int
    fingerprint: bytes
    values: tuple[bytes, ...]
    paths: dict[int, str]
    rows: tuple[SCIndexRow, ...]

    def value(self, index: int, *, context: str) -> bytes:
        if not 0 <= index < len(self.values):
            raise SCIndexError(f"{context}: value index {index} is out of range")
        return self.values[index]

    def rows_by_value(self) -> dict[bytes, SCIndexRow]:
        result = {}
        for row in self.rows:
            key = self.value(row.key_index, context=f"{row.table_name} row key")
            if key in result:
                raise SCIndexError(f"duplicate row key in {row.table_name}: {key.hex()}")
            result[key] = row
        return result


def parse_scindex(data: bytes, *, label: str = "scindex") -> SCIndex:
    reader = _Reader(data, label)
    version = reader.uint32()
    if version != 1:
        raise SCIndexError(f"{label}: unsupported version {version}")

    fingerprint = reader._take(16)
    declared_row_count = reader.uint32()
    value_count = reader.uint32()
    values = tuple(reader._take(16) for _ in range(value_count))

    paths = {}
    for index in range(value_count):
        has_path = reader.uint8()
        if has_path not in (0, 1):
            raise SCIndexError(f"{label}: invalid path flag {has_path} for value {index}")
        if has_path:
            try:
                paths[index] = reader.sized_bytes().decode("utf-8")
            except UnicodeDecodeError as exc:
                raise SCIndexError(f"{label}: path {index} is not valid UTF-8") from exc

    rows = []
    table_count = reader.uint32()
    for table_number in range(table_count):
        try:
            table_name = reader.sized_bytes().decode("utf-8")
        except UnicodeDecodeError as exc:
            raise SCIndexError(f"{label}: table {table_number} has an invalid UTF-8 name") from exc
        row_count = reader.uint32()
        for _ in range(row_count):
            key_index = reader.uint32()
            if key_index >= value_count:
                raise SCIndexError(f"{label}: {table_name} row key index {key_index} is out of range")
            rows.append(
                SCIndexRow(
                    table_name=table_name,
                    key_index=key_index,
                    checksum=reader.uint64(),
                    payload=reader.sized_bytes(),
                )
            )

    reader.finish()
    if len(rows) != declared_row_count:
        raise SCIndexError(
            f"{label}: header declares {declared_row_count} rows, but the tables contain {len(rows)}"
        )
    return SCIndex(
        version=version,
        fingerprint=fingerprint,
        values=values,
        paths=paths,
        rows=tuple(rows),
    )


def split_row_blocks(payload: bytes, *, context: str) -> tuple[bytes, ...]:
    reader = _Reader(payload, context)
    padding_size = reader.uint8()
    padding = reader._take(padding_size)
    if any(padding):
        raise SCIndexError(f"{context}: row padding contains non-zero bytes")

    blocks = []
    while reader.position < len(payload):
        blocks.append(reader.sized_bytes())
    if not blocks:
        raise SCIndexError(f"{context}: row has no data blocks")
    return tuple(blocks)


def parse_string_block(data: bytes, *, context: str) -> tuple[str, ...]:
    reader = _Reader(data, context)
    strings = []
    while reader.position < len(data):
        try:
            strings.append(reader.sized_bytes().decode("utf-8"))
        except UnicodeDecodeError as exc:
            raise SCIndexError(f"{context}: string is not valid UTF-8") from exc
    return tuple(strings)


class FlatBufferTable:
    def __init__(self, data: bytes, *, context: str):
        self.data = data
        self.context = context
        self.table = self._uint32_at(0)
        if self.table + 4 > len(data):
            raise SCIndexError(f"{context}: root table offset {self.table} is out of range")

        vtable_distance = self._int32_at(self.table)
        if vtable_distance <= 0 or vtable_distance > self.table:
            raise SCIndexError(f"{context}: invalid vtable distance {vtable_distance}")
        self.vtable = self.table - vtable_distance
        self.vtable_size = self._uint16_at(self.vtable)
        self.object_size = self._uint16_at(self.vtable + 2)
        if self.vtable_size < 4 or self.vtable + self.vtable_size > len(data):
            raise SCIndexError(f"{context}: invalid vtable size {self.vtable_size}")
        if self.object_size < 4 or self.table + self.object_size > len(data):
            raise SCIndexError(f"{context}: invalid table size {self.object_size}")

    def _require(self, offset: int, size: int) -> None:
        if offset < 0 or offset + size > len(self.data):
            raise SCIndexError(f"{self.context}: expected {size} bytes at offset {offset}")

    def _uint16_at(self, offset: int) -> int:
        self._require(offset, 2)
        return struct.unpack_from("<H", self.data, offset)[0]

    def _uint32_at(self, offset: int) -> int:
        self._require(offset, 4)
        return struct.unpack_from("<I", self.data, offset)[0]

    def _int32_at(self, offset: int) -> int:
        self._require(offset, 4)
        return struct.unpack_from("<i", self.data, offset)[0]

    def field_offset(self, field: int) -> int | None:
        entry = self.vtable + 4 + field * 2
        if entry + 2 > self.vtable + self.vtable_size:
            return None
        relative = self._uint16_at(entry)
        if relative == 0:
            return None
        if relative >= self.object_size:
            raise SCIndexError(f"{self.context}: field {field} offset {relative} is outside the table")
        return self.table + relative

    def uint32(self, field: int, *, default: int = 0) -> int:
        offset = self.field_offset(field)
        return default if offset is None else self._uint32_at(offset)

    def boolean(self, field: int, *, default: bool = False) -> bool:
        offset = self.field_offset(field)
        if offset is None:
            return default
        self._require(offset, 1)
        value = self.data[offset]
        if value not in (0, 1):
            raise SCIndexError(f"{self.context}: field {field} has invalid boolean value {value}")
        return bool(value)

    def string(self, field: int) -> str | None:
        offset = self.field_offset(field)
        if offset is None:
            return None
        target = offset + self._uint32_at(offset)
        length = self._uint32_at(target)
        self._require(target + 4, length)
        try:
            return self.data[target + 4 : target + 4 + length].decode("utf-8")
        except UnicodeDecodeError as exc:
            raise SCIndexError(f"{self.context}: field {field} is not a valid UTF-8 string") from exc


def _global_id(value: bytes, *, context: str) -> int:
    low, high = struct.unpack("<QQ", value)
    if high != 1 << 63:
        raise SCIndexError(f"{context}: expected a global ID, got UUID {value.hex()}")
    return low


def decode_decorations(deco_data: bytes, asset_data: bytes) -> dict[str, dict]:
    decorations = parse_scindex(deco_data, label="logic/decos_logic.scindex")
    assets = parse_scindex(asset_data, label="data/assetdata.scindex")
    asset_rows = assets.rows_by_value()
    result = {}

    for row in decorations.rows:
        if row.table_name != "logic-deco-data":
            raise SCIndexError(f"unexpected decoration table {row.table_name!r}")
        global_id = _global_id(
            decorations.value(row.key_index, context="decoration row"),
            context="decoration row",
        )
        context = f"decoration {global_id}"
        blocks = split_row_blocks(row.payload, context=context)
        if len(blocks) != 3:
            raise SCIndexError(f"{context}: expected 3 data blocks, got {len(blocks)}")
        names = parse_string_block(blocks[0], context=f"{context} names")
        if len(names) < 2 or not names[0] or not names[1]:
            raise SCIndexError(f"{context}: missing name or TID")

        base = FlatBufferTable(blocks[1], context=f"{context} base data")
        details = FlatBufferTable(blocks[2], context=f"{context} details")
        decoded = {
            "GlobalID": global_id,
            "TID": names[1],
            "Width": details.uint32(0),
            "MaxCount": details.uint32(5),
            "BuildResource": details.string(13),
            "BuildCost": details.uint32(14),
            "NotInShop": not details.boolean(20),
            "BPReward": details.boolean(33),
            "VillageType": base.uint32(0),
        }

        movie_clip_index = details.uint32(12, default=UINT32_MAX)
        if movie_clip_index != UINT32_MAX:
            movie_clip_key = decorations.value(movie_clip_index, context=f"{context} movie clip")
            movie_clip_row = asset_rows.get(movie_clip_key)
            if movie_clip_row is None or movie_clip_row.table_name != "movie-clip":
                actual = None if movie_clip_row is None else movie_clip_row.table_name
                raise SCIndexError(f"{context}: expected movie-clip asset, got {actual!r}")
            movie_blocks = split_row_blocks(movie_clip_row.payload, context=f"{context} movie clip")
            if len(movie_blocks) != 1:
                raise SCIndexError(f"{context}: movie clip has {len(movie_blocks)} data blocks")
            movie_clip = FlatBufferTable(movie_blocks[0], context=f"{context} movie clip")
            swf_index = movie_clip.uint32(0, default=UINT32_MAX)
            export_name = movie_clip.string(1)
            if swf_index == UINT32_MAX or not export_name:
                raise SCIndexError(f"{context}: movie clip is missing its SWF or export name")
            swf_key = assets.value(swf_index, context=f"{context} SWF")
            swf_row = asset_rows.get(swf_key)
            if swf_row is None or swf_row.table_name != "swf":
                actual = None if swf_row is None else swf_row.table_name
                raise SCIndexError(f"{context}: expected SWF asset, got {actual!r}")
            source_sc = assets.paths.get(swf_index)
            if not source_sc or not source_sc.endswith(".sc"):
                raise SCIndexError(f"{context}: SWF has invalid source path {source_sc!r}")
            decoded["SWF"] = source_sc
            decoded["ExportName"] = export_name

        if names[0] in result:
            raise SCIndexError(f"duplicate decoration name {names[0]!r}")
        result[names[0]] = decoded

    return result
