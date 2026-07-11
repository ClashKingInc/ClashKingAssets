"""
Automates updating the static files.
Now saves both the raw CSV and the generated JSON files.
If new files need to be added, then place them in the TARGETS list.
"""

import asyncio
import csv
import hashlib
import json
import logging
import lzma
import os
import shutil
import subprocess
import tempfile
from dataclasses import dataclass
from pathlib import Path

import zstandard

from utils import apk_url, download_file, fetch_fingerprint_manifest


@dataclass(frozen=True)
class SCAssetRequest:
    source_sc: str
    asset_name: str | None
    save_path: str
    first_frame: bool = False
    last_frame: bool = False
    frame_index: int | None = None
    static_only: bool = False
    preferred_frame_label: str | None = None
    base_asset_name: str | None = None
    base_source_sc: str | None = None


DIRECT_ASSET_EXTENSIONS = {".sctx", ".ttf", ".otf", ".woff", ".woff2", ".mp4", ".ogg"}
DIRECT_WEBP_EXTENSIONS = {".sctx"}


def is_sc_bundle_file(source_sc: str, candidate: str) -> bool:
    source_path = Path(source_sc)
    candidate_path = Path(candidate)
    if candidate_path.parent.as_posix() != source_path.parent.as_posix():
        return False

    base = source_path.stem
    name = candidate_path.name
    if name == f"{base}.sc" or name == f"{base}_tex.sc":
        return True
    return name.startswith(f"{base}_") and name.endswith(".sctx")


def remove_empty_parents(path: Path, stop_at: Path) -> None:
    current = path.resolve()
    stop_at = stop_at.resolve()
    while current != stop_at:
        try:
            current.rmdir()
        except OSError:
            return
        parent = current.parent
        if parent == current:
            return
        current = parent


def is_exported_via_go(source_sc: str) -> bool:
    return source_sc.endswith((".sc", ".sctx"))


def is_animated_webp(path: Path) -> bool:
    try:
        with path.open("rb") as f:
            header = f.read(12)
            if len(header) != 12 or header[:4] != b"RIFF" or header[8:] != b"WEBP":
                return False

            while chunk_header := f.read(8):
                if len(chunk_header) != 8:
                    return False
                chunk_type = chunk_header[:4]
                chunk_size = int.from_bytes(chunk_header[4:], "little")
                if chunk_type == b"ANIM":
                    return True
                f.seek(chunk_size + (chunk_size % 2), os.SEEK_CUR)
    except OSError:
        return False

    return False


def hash_15_digits(s: str) -> int:
    digest = hashlib.blake2b(s.encode("utf-8"), digest_size=8).digest()
    return int.from_bytes(digest, "big") % 10**15

class StaticUpdater:
    def __init__(self):
        self.USED_TIDS = set()

        # keep the raw CSV files
        self.KEEP_CSV = False
        # keep the raw JSON files
        self.KEEP_JSON = False
        # removes any TIDs not used in the static files
        self.PRUNE_TRANSLATIONS = True
        # base path for the static files to be stored in
        self.BASE_PATH = "assets"

        self.FINGERPRINT = os.getenv("FINGERPRINT", "")
        self.APK_URL = apk_url()

        self.translation_data = {}
        self.full_building_data = {}
        self.full_supercharges_data = {}
        self.full_abilities_data = {}
        self.full_hero_data = {}
        self.full_townhall_data = {}
        self.full_troop_data = {}
        self.full_resource_data = {}

        self.lab_to_townhall = {}
        self.smithy_to_townhall = {}
        self.bb_lab_to_townhall = {}
        self.pethouse_to_townhall = {}

        self.animations_data = {}
        self.sc_asset_requests: dict[
            tuple[str, str | None, bool, bool, int | None, bool, str | None, str | None, str | None], list[SCAssetRequest]
        ] = {}

        self.build_mappping = {}

    def register_sc_asset(
        self,
        source_sc: str,
        asset_name: str,
        save_path: str,
        first_frame: bool = False,
        last_frame: bool = False,
        frame_index: int | None = None,
        static_only: bool = False,
        preferred_frame_label: str | None = None,
        base_asset_name: str | None = None,
        base_source_sc: str | None = None,
    ) -> str:
        source_sc = source_sc.strip()
        normalized_asset_name = (asset_name or "").strip()
        save_path = save_path.strip()
        frame_modes = [first_frame, last_frame, frame_index is not None, static_only]
        if sum(1 for enabled in frame_modes if enabled) > 1:
            raise ValueError(f"asset cannot request multiple frame modes: {source_sc}:{normalized_asset_name}")
        if frame_index is not None and frame_index < 1:
            raise ValueError(f"frame_index must be 1 or greater: {source_sc}:{normalized_asset_name}")
        source_ext = Path(source_sc).suffix.lower()
        if source_ext != ".sc" and source_ext not in DIRECT_ASSET_EXTENSIONS:
            raise ValueError(f"invalid asset source: {source_sc!r}")
        if source_ext == ".sc" and not normalized_asset_name:
            raise ValueError(f"invalid SC asset name for {source_sc!r}")
        request_asset_name = normalized_asset_name if source_ext == ".sc" else None
        if not save_path:
            raise ValueError(f"save_path must not be empty for {source_sc}:{normalized_asset_name}")
        if source_ext in DIRECT_WEBP_EXTENSIONS or source_ext == ".sc":
            if Path(save_path).suffix.lower() != ".webp":
                save_path = f"{save_path}.webp"
        elif Path(save_path).suffix.lower() != source_ext:
            save_path = f"{save_path}{source_ext}"

        preferred_frame_label = (preferred_frame_label or "").strip() or None
        base_asset_name = (base_asset_name or "").strip() or None
        base_source_sc = (base_source_sc or "").strip() or None
        if base_source_sc and not base_asset_name:
            raise ValueError("base_source_sc requires base_asset_name")
        key = (
            source_sc,
            request_asset_name,
            first_frame,
            last_frame,
            frame_index,
            static_only,
            preferred_frame_label,
            base_asset_name,
            base_source_sc,
        )
        request = SCAssetRequest(
            source_sc=source_sc,
            asset_name=request_asset_name,
            save_path=save_path,
            first_frame=first_frame,
            last_frame=last_frame,
            frame_index=frame_index,
            static_only=static_only,
            preferred_frame_label=preferred_frame_label,
            base_asset_name=base_asset_name,
            base_source_sc=base_source_sc,
        )
        requests = self.sc_asset_requests.setdefault(key, [])
        if any(existing.save_path == save_path for existing in requests):
            return save_path
        requests.append(request)
        return save_path

    def should_skip_registered_asset(self, request: SCAssetRequest) -> bool:
        destination = self.resolve_asset_output_path(request.save_path)
        if not destination.exists():
            return False
        if request.base_asset_name:
            return False
        if request.last_frame:
            return False
        if request.frame_index is not None:
            return False
        if request.static_only and destination.suffix.lower() == ".webp" and is_animated_webp(destination):
            return False
        if request.preferred_frame_label:
            return False
        if request.first_frame and request.asset_name and "/" in request.asset_name:
            return False
        if (request.first_frame or request.last_frame) and destination.suffix.lower() == ".webp" and is_animated_webp(destination):
            return False
        return True

    def resolve_asset_output_path(self, save_path: str) -> Path:
        normalized = Path(save_path.strip().lstrip("/"))
        return Path(self.BASE_PATH) / normalized

    def village_asset_folder(self, root: str, village_type: int) -> str:
        village_folder = "builder-base" if village_type else "home-village"
        return f"{root}/{village_folder}"

    def building_icon_asset_name(self, building_data: dict, level_data: dict, asset_name: str) -> str:
        if building_data.get("BuildingClass") == "Wall":
            return f"{asset_name}_3"
        if building_data.get("TID") == "TID_WORKER_BUILDING" and asset_name.startswith("worker_building_armed_lvl"):
            return f"{asset_name}/turret_load"
        return asset_name

    def building_icon_uses_last_frame(self, building_data: dict) -> bool:
        return building_data.get("TID") in {
            "TID_BUILDING_GOLD_STORAGE",
            "TID_BUILDING_ELIXIR_STORAGE",
            "TID_BUILDING_DARK_ELIXIR_STORAGE",
        }

    def season_defense_archetypes_to_export(self) -> set[str]:
        full_season_data = self.open_file("logic/seasonal_defense.json")
        archetypes: set[str] = set()
        seen_tids = set()
        for season_data in full_season_data.values():
            season_tid = season_data.get("TID")
            if not season_tid or season_tid in seen_tids:
                continue
            seen_tids.add(season_tid)
            for key, value in season_data.items():
                if key.isdigit() and value.get("Archetypes"):
                    archetypes.add(value.get("Archetypes"))
        return archetypes

    def register_seasonal_defense_assets(self) -> None:
        full_seasonal_defenses = self.open_file("logic/seasonal_defense_archetypes.json")
        archetypes_to_export = self.season_defense_archetypes_to_export()
        for archetype_name, archetype_data in full_seasonal_defenses.items():
            if archetype_name not in archetypes_to_export:
                continue

            ability_data = self.full_abilities_data.get(archetype_data.get("SpecialAbility"), {})
            name_tid = ability_data.get("OverrideTID")
            if not name_tid:
                continue

            for level, level_data in ability_data.items():
                if not isinstance(level_data, dict):
                    continue
                asset_name = level_data.get("OverrideExportName")
                if not asset_name:
                    continue
                self.register_sc_asset(
                    source_sc=level_data.get("OverrideSWF") or "sc/buildings.sc",
                    asset_name=asset_name,
                    save_path=(
                        "buildings/seasonal-defense/"
                        f"{self.clean_name(self._translate(tid=name_tid))}/"
                        f"level_{level_data.get('Level') or level}"
                    ),
                    first_frame=True,
                )

    def save_registered_asset(self, request: SCAssetRequest, local_path: Path) -> None:
        destination = self.resolve_asset_output_path(request.save_path)
        if self.should_skip_registered_asset(request):
            return
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(local_path, destination)

    async def _download_sc_bundle(self, base_url: str, source_sc: str, available_files: set[str]) -> list[Path]:
        downloaded: list[Path] = []

        bundle_files = sorted(file_path for file_path in available_files if is_sc_bundle_file(source_sc, file_path))
        if source_sc not in bundle_files:
            raise FileNotFoundError(f"missing source bundle file in fingerprint: {source_sc}")

        for remote_path in bundle_files:
            data = await download_file(url=f"{base_url}/{remote_path}")
            local_path = Path(remote_path)
            local_path.parent.mkdir(parents=True, exist_ok=True)
            local_path.write_bytes(data)
            downloaded.append(local_path)

        return downloaded

    async def extract_assets(self):
        if not self.sc_asset_requests:
            return

        self.FINGERPRINT, fingerprint_file = await fetch_fingerprint_manifest(self.APK_URL, self.FINGERPRINT)
        base_url = f"https://game-assets.clashofclans.com/{self.FINGERPRINT}"
        available_files = {item.get("file") for item in fingerprint_file.get("files", []) if item.get("file")}

        grouped: dict[tuple[str, bool, bool, int | None, bool, str | None], dict[str | None, list[SCAssetRequest]]] = {}
        for requests in self.sc_asset_requests.values():
            pending_requests = [
                request for request in requests if not self.should_skip_registered_asset(request)
            ]
            if not pending_requests:
                continue
            primary = pending_requests[0]
            grouped.setdefault(
                (
                    primary.source_sc,
                    primary.first_frame,
                    primary.last_frame,
                    primary.frame_index,
                    primary.static_only,
                    primary.preferred_frame_label,
                ),
                {},
            )[primary.asset_name] = pending_requests

        for (
            source_sc,
            first_frame,
            last_frame,
            frame_index,
            static_only,
            preferred_frame_label,
        ), asset_requests in sorted(
            grouped.items(),
            key=lambda item: tuple("" if value is None else str(value) for value in item[0]),
        ):
            downloaded_files: list[Path] = []
            legacy_assets_dir = Path(source_sc).parent / f"{Path(source_sc).stem}_assets"
            try:
                if source_sc.endswith(".sc"):
                    downloaded_files = await self._download_sc_bundle(base_url, source_sc, available_files)
                else:
                    data = await download_file(url=f"{base_url}/{source_sc}")
                    local_path = Path(source_sc)
                    local_path.parent.mkdir(parents=True, exist_ok=True)
                    local_path.write_bytes(data)
                    downloaded_files = [local_path]
                base_sources = {
                    request.base_source_sc
                    for requests in asset_requests.values()
                    for request in requests
                    if request.base_source_sc
                }
                if len(base_sources) > 1:
                    raise ValueError(f"multiple base SC sources in one export group: {sorted(base_sources)}")
                if base_sources:
                    base_source_sc = next(iter(base_sources))
                    downloaded_files.extend(
                        await self._download_sc_bundle(base_url, base_source_sc, available_files)
                    )
                if not is_exported_via_go(source_sc):
                    for requests in asset_requests.values():
                        for request in requests:
                            self.save_registered_asset(request, downloaded_files[0])
                    continue
                with tempfile.TemporaryDirectory(prefix="update-static-sc-") as temp_dir:
                    command = ["go", "run", "main.go", "--workers", str(max(1, os.cpu_count() or 1)), "--prefer-webp"]
                    if first_frame:
                        command.append("--first-frame")
                    if last_frame:
                        command.append("--last-frame")
                    if frame_index is not None:
                        command.extend(["--frame", str(frame_index)])
                    if static_only:
                        command.append("--static-only")
                    if preferred_frame_label:
                        command.extend(["--prefer-frame-label", preferred_frame_label])
                    if base_sources:
                        command.extend(["--base-sc", next(iter(base_sources))])
                    if source_sc.endswith(".sc"):
                        command.extend(["--out", temp_dir])
                        for asset_name, requests in sorted(asset_requests.items()):
                            if asset_name is None:
                                raise ValueError(f"missing asset name for SC source {source_sc}")
                            temp_output_base = str(Path(temp_dir) / asset_name)
                            command.extend(["--asset", asset_name])
                            command.extend(["--asset-output", f"{asset_name}={temp_output_base}"])
                            if requests[0].base_asset_name:
                                command.extend(["--base-asset", f"{asset_name}={requests[0].base_asset_name}"])
                    else:
                        direct_output = str(Path(temp_dir) / Path(source_sc).stem)
                        command.extend(["--out", direct_output])
                    command.append(source_sc)
                    subprocess.run(command, check=True)

                    if source_sc.endswith(".sc"):
                        manifest_path = Path(temp_dir) / "manifest.json"
                        manifest_data = json.loads(manifest_path.read_text())
                        exported_files = {
                            entry["export_name"]: entry["output_file"] for entry in manifest_data.get("exports", [])
                        }
                        skipped_exports = {
                            entry["export_name"]: entry.get("reason", "unknown reason")
                            for entry in manifest_data.get("skipped", [])
                        }

                        for asset_name, requests in asset_requests.items():
                            exported_file = exported_files.get(asset_name)
                            if not exported_file:
                                skip_reason = skipped_exports.get(asset_name)
                                if skip_reason:
                                    raise RuntimeError(f"asset {source_sc}:{asset_name} was skipped: {skip_reason}")
                                raise FileNotFoundError(f"asset output missing for {source_sc}:{asset_name}")
                            for request in asset_requests[asset_name]:
                                self.save_registered_asset(request, Path(exported_file))
                    else:
                        exported_file = str(Path(temp_dir) / f"{Path(source_sc).stem}.webp")
                        for request in next(iter(asset_requests.values())):
                            self.save_registered_asset(request, Path(exported_file))
                shutil.rmtree(legacy_assets_dir, ignore_errors=True)
            finally:
                for path in downloaded_files:
                    try:
                        path.unlink()
                    except OSError:
                        pass
                shutil.rmtree(legacy_assets_dir, ignore_errors=True)
                if downloaded_files:
                    remove_empty_parents(Path(downloaded_files[0]).parent, Path.cwd())

    def decompress(self, data):
        """
        Decompresses the given bytes 'data' if needed (LZHAM, ZSTD, or LZMA).
        Returns (decompressed_bytes, compression_details).
        """
        if data[0:4] == b"SCLZ":
            logging.debug("Decompressing using LZHAM ...")
            import lzham

            dict_size = int.from_bytes(data[4:5], byteorder="big")
            uncompressed_size = int.from_bytes(data[5:9], byteorder="little")

            decompressed = lzham.decompress(data[9:], uncompressed_size, {"dict_size_log2": dict_size})
            return decompressed, {"dict_size": dict_size, "uncompressed_size": uncompressed_size}

        if int.from_bytes(data[0:4], byteorder="little") == zstandard.MAGIC_NUMBER:
            logging.debug("Decompressing using ZSTD ...")
            decompressed = zstandard.decompress(data)
            return decompressed, {"dict_size": None, "uncompressed_size": None}

        logging.debug("Decompressing using LZMA ...")
        data = data[0:9] + (b"\x00" * 4) + data[9:]
        prop = data[0]
        o_prop = prop
        if prop > (4 * 5 + 4) * 9 + 8:
            raise Exception("LZMA properties error")
        decompressed = lzma.LZMADecompressor().decompress(data)
        return decompressed, {"lzma_prop": o_prop}

    def process_csv(self, data, file_path: str):
        if self.is_compressed(data):
            if data[:4] == b"Sig:":
                logging.debug("Stripping Sig: header and signature...")
                data = data[68:]
            decompressed_data, _ = self.decompress(data)
        else:
            decompressed_data = data

        Path(file_path).parent.mkdir(parents=True, exist_ok=True)
        with open(file_path, "wb") as f:
            f.write(decompressed_data)

        with open(file_path, encoding="utf-8") as csvf:
            rows = list(csv.reader(csvf))
        if len(rows) < 2:
            return

        columns = rows[0]
        types_row = rows[1]

        if len(columns) > 1 and columns[1] == "GlobalID":
            columns = [columns[0]] + columns[2:] + [columns[1]]
            types_row = [types_row[0]] + types_row[2:] + [types_row[1]]

            reordered_rows = []
            for row in rows[2:]:
                if len(row) > 1:
                    reordered_row = [row[0]] + row[2:] + [row[1]]
                    reordered_rows.append(reordered_row)
                else:
                    reordered_rows.append(row)
            rows = rows[:2] + reordered_rows

        is_numeric_level = types_row[1].lower() == "int" or "level" in columns[1].lower()

        final_data = {}
        current_troop = None
        level_counter = None
        current_level = None

        for row in rows[2:]:
            if not any(cell.strip() for cell in row):
                continue

            if row[0].strip():
                current_troop = row[0].strip()
                final_data[current_troop] = {}
                if not is_numeric_level:
                    level_counter = 1
                current_level = None

            if current_troop is None:
                continue

            if is_numeric_level:
                if len(row) > 1 and row[1].strip():
                    current_level = row[1].strip()
                if not current_level:
                    continue
                lvl_key = current_level
            else:
                lvl_key = str(level_counter)
                level_counter += 1

            level_dict = final_data[current_troop].setdefault(lvl_key, {})

            for idx, col_name in enumerate(columns):
                if idx >= len(row):
                    break
                val = row[idx].strip()
                if val == "":
                    continue
                low = val.lower()
                if low == "true":
                    conv = True
                elif low == "false":
                    conv = False
                elif val.isdigit() or (val.startswith("-") and val[1:].isdigit()):
                    conv = int(val)
                else:
                    conv = val
                level_dict[col_name] = conv

        for troop, levels in list(final_data.items()):
            lvl_keys = sorted(levels.keys(), key=lambda x: int(x) if x.isdigit() else 999999)
            if len(lvl_keys) <= 1:
                continue
            base = lvl_keys[0]
            for col in list(levels[base].keys()):
                if not any(col in levels[l] for l in lvl_keys[1:]):
                    final_data[troop][col] = levels[base][col]
                    del levels[base][col]

        for troop in list(final_data.keys()):
            levels = final_data[troop]
            if isinstance(levels, dict) and len(levels) == 1:
                only_lvl, data_dict = next(iter(levels.items()))
                if isinstance(data_dict, dict):
                    final_data[troop] = data_dict

        save_file = file_path.replace(".csv", ".json")
        with open(save_file, "w", encoding="utf-8") as jf:
            json.dump(final_data, jf, indent=2)

        if not self.KEEP_CSV:
            try:
                os.remove(file_path)
            except OSError as e:
                logging.warning(f"Could not delete {file_path}: {e}")

    def process_animations_csv(self, data, file_path: str):
        if self.is_compressed(data):
            try:
                if data[:4] == b"Sig:":
                    data = data[68:]
                decompressed_data, _ = self.decompress(data)
            except Exception:
                decompressed_data = data
        else:
            decompressed_data = data

        Path(file_path).parent.mkdir(parents=True, exist_ok=True)
        with open(file_path, "wb") as f:
            f.write(decompressed_data)

        with open(file_path, newline="", encoding="utf-8") as csvf:
            rows = list(csv.reader(csvf))

        final_data = {}
        index = 0
        while index < len(rows):
            row = rows[index]
            character_name = row[0].strip() if row else ""
            if not character_name:
                index += 1
                continue

            if index + 1 >= len(rows):
                break

            header_row = rows[index + 1]
            columns = [cell.strip() for cell in header_row]
            if "HasDirections" not in columns:
                index += 1
                while index < len(rows) and not (rows[index] and rows[index][0].strip()):
                    index += 1
                continue

            column_map = {name: position for position, name in enumerate(columns) if name}
            current_export_name = None
            next_index = index + 3
            character_swf = None
            animations = []
            seen_animations = set()

            while next_index < len(rows):
                data_row = rows[next_index]
                if data_row and data_row[0].strip():
                    break

                has_directions = (
                    data_row[column_map["HasDirections"]].strip().upper()
                    if len(data_row) > column_map["HasDirections"]
                    else ""
                )
                export_name = (
                    data_row[column_map["ExportName"]].strip()
                    if "ExportName" in column_map and len(data_row) > column_map["ExportName"]
                    else ""
                )
                swf_value = (
                    data_row[column_map["SWF"]].strip()
                    if "SWF" in column_map and len(data_row) > column_map["SWF"]
                    else ""
                )

                if export_name:
                    current_export_name = export_name
                if swf_value and not character_swf:
                    character_swf = swf_value

                if has_directions == "TRUE" and current_export_name and current_export_name not in seen_animations:
                    seen_animations.add(current_export_name)
                    animations.append(current_export_name)

                next_index += 1

            if character_swf:
                final_data[character_name] = {"swf": character_swf, "animations": animations}

            index = next_index

        save_file = file_path.replace(".csv", ".json")
        with open(save_file, "w", encoding="utf-8") as jf:
            json.dump(final_data, jf, indent=2)

        if not self.KEEP_CSV:
            try:
                os.remove(file_path)
            except OSError as e:
                logging.warning(f"Could not delete {file_path}: {e}")

    def is_compressed(self, data):
        if data[:4] == b"Sig:":
            return True
        if data[:4] == b"SCLZ":
            return True
        if len(data) >= 4 and int.from_bytes(data[0:4], byteorder="little") == zstandard.MAGIC_NUMBER:
            return True
        if data[0] == 0x5D:
            return True
        if data[:2] == b"\x53\x43":
            return True
        if data[:6] == b'"Name"' or data[:6] == b'"name"' or data[:5] == b'"TID"':
            return False
        return True

    def open_file(self, file_path: str) -> dict:
        with open(file_path, "r", encoding="utf-8") as f:
            data: dict = json.load(f)
        return data

    def clean_name(self, s: str) -> str:
        return s.lower().replace(" ", "_").replace(".", "").replace("?", "").replace("\\q", "").replace("’", "")

    def _translate(self, tid: str):
        self.USED_TIDS.add(tid)
        return self.translation_data.get(tid, {}).get("EN")

    def _parse_upgrade_time(self, level_data: dict) -> int:
        upgrade_time_seconds = (level_data.get("BuildTimeD") or level_data.get("UpgradeTimeD", 0)) * 24 * 60 * 60
        upgrade_time_seconds += (level_data.get("BuildTimeH") or level_data.get("UpgradeTimeH", 0)) * 60 * 60
        upgrade_time_seconds += (level_data.get("BuildTimeM") or level_data.get("UpgradeTimeM", 0)) * 60
        upgrade_time_seconds += level_data.get("BuildTimeS") or level_data.get("UpgradeTimeS", 0)
        return upgrade_time_seconds

    def _parse_resource(self, resource: str) -> str:
        resource_TID: str = self.full_resource_data.get(resource, {}).get("TID")
        return self._translate(resource_TID)

    @staticmethod
    def _first_present(key: str, *sources: dict, default=None):
        for source in sources:
            if not isinstance(source, dict):
                continue
            if key in source and source[key] is not None:
                return source[key]
        return default

    def _has_alternate_defense_mode(self, building_data: dict) -> bool:
        return bool(building_data.get("AltAttackMode") or building_data.get("AlternateModeTID"))

    def _defense_level_stats(
        self,
        building_data: dict,
        level_data: dict,
        prefix: str = "",
        fallback_stats: dict | None = None,
    ) -> dict:
        range_key = f"{prefix}AttackRange" if prefix else "AttackRange"
        min_range_key = f"{prefix}MinAttackRange" if prefix else "MinAttackRange"
        dps_key = f"{prefix}DPS" if prefix else "DPS"
        damage_key = f"{prefix}Damage" if prefix else "Damage"
        fallback_stats = fallback_stats or {}

        stats = {}

        damage = self._first_present(damage_key, level_data, building_data)
        if damage is None and prefix:
            damage = fallback_stats.get("damage")
        if damage is not None:
            stats["damage"] = damage

        dps = self._first_present(dps_key, level_data, building_data)
        if dps == 0 and damage is not None:
            dps = None
        if dps is None and prefix:
            dps = fallback_stats.get("dps")
        if dps is not None:
            stats["dps"] = dps

        attack_speed = self._first_present(
            f"{prefix}AttackSpeed" if prefix else "AttackSpeed",
            level_data,
            building_data,
            default=fallback_stats.get("attack_speed"),
        )
        if attack_speed is not None:
            stats["attack_speed"] = attack_speed

        stats["attack_range"] = self._first_present(
            range_key,
            level_data,
            building_data,
            default=fallback_stats.get("attack_range", 0),
        )
        min_range = self._first_present(
            min_range_key,
            level_data,
            building_data,
            default=fallback_stats.get("min_range"),
        )
        if min_range:
            stats["min_range"] = min_range
        return stats

    def _spell_tower_effect_range(
        self,
        weapon_data: dict,
        projectile_data: dict,
        spell_data: dict,
    ) -> int | None:
        projectile_name = weapon_data.get("Projectile")
        hit_spell = projectile_data.get(projectile_name, {}).get("HitSpell")
        if not hit_spell:
            return None
        return spell_data.get(hit_spell, {}).get("Radius")

    def _parse_translation_data(self):
        full_translation_data = self.open_file("localization/texts.json")
        other_translations = []
        for path in sorted(Path("localization").glob("*.json")):
            if "text" in path.stem.lower():
                continue
            with open(path, "r", encoding="utf-8") as f:
                other_translations.append((path.stem, json.load(f)))

        new_translation_data = {}
        for translation_key, translation_data in full_translation_data.items():
            new_translation_data[translation_key] = {"EN": translation_data.get("EN")}
            for lang, language_data in other_translations:
                if not language_data.get(translation_key):
                    continue
                new_translation_data[translation_key][lang.upper()] = language_data.get(translation_key).get(
                    lang.upper()
                )

        self.translation_data = new_translation_data
        return new_translation_data

    def _parse_achievement_data(self):
        self.full_achievement_data = self.open_file("logic/achievements.json")
        new_achievement_data = {}
        for achievement_name, achievement_data in self.full_achievement_data.items():
            tid = achievement_data.get("TID")
            group_map = {0: "home", 1: "builderBase", 2: "clanCapital"}
            if tid not in new_achievement_data:
                new_achievement_data[tid] = {
                    "name": self._translate(achievement_data.get("TID")),
                    "info": self._translate(achievement_data.get("InfoTID")),
                    "completed_message": self._translate(achievement_data.get("CompletedTID")),
                    "TID": {
                        "name": achievement_data.get("TID"),
                        "info": achievement_data.get("InfoTID"),
                        "completed_message": achievement_data.get("CompletedTID"),
                    },
                    "village": group_map.get(achievement_data["UIGroup"]),
                    "ui_priority": achievement_data.get("UIPriority", 0),
                    "levels": [
                        {
                            "level": achievement_data.get("Level") + 1,
                            "action_count": achievement_data.get("ActionCount"),
                            "action_data": achievement_data.get("ActionData"),
                            "xp": achievement_data.get("ExpReward", 0),
                            "gems": achievement_data.get("DiamondReward", 0),
                        }
                    ],
                }
            else:
                new_achievement_data[tid]["levels"].append(
                    {
                        "level": achievement_data.get("Level") + 1,
                        "action_count": achievement_data.get("ActionCount"),
                        "action_data": achievement_data.get("ActionData"),
                        "xp": achievement_data.get("ExpReward", 0),
                        "gems": achievement_data.get("DiamondReward", 0),
                    }
                )

        return list(new_achievement_data.values())

    def _parse_building_data(self):
        self.full_building_data = self.open_file("logic/buildings.json")
        self.full_supercharges_data = self.open_file("logic/mini_levels.json")
        self.full_townhall_data = self.open_file("logic/townhall_levels.json")
        full_weapon_data: dict = self.open_file("logic/weapons.json")
        full_projectile_data: dict = self.open_file("logic/projectiles.json")
        full_spell_data: dict = self.open_file("logic/spells.json")
        full_globals_data: dict = self.open_file("logic/globals.json")
        self.register_seasonal_defense_assets()

        clan_castle_radius = full_globals_data.get("CLAN_CASTLE_RADIUS", {}).get("NumberValue")
        clan_castle_attack_range = clan_castle_radius * 100 if clan_castle_radius is not None else None

        new_building_data = []

        for building_name, building_data in self.full_building_data.items():
            if (
                building_data.get("BuildingClass") in ["Npc", "NonFunctional", "Npc Town Hall"]
                or "Unused" in building_name
            ):
                continue

            village_type = building_data.get("VillageType", 0)
            is_defense = building_data.get("BuildingClass") == "Defense"
            has_alternate_mode = is_defense and self._has_alternate_defense_mode(building_data)
            has_level_targeting = any(
                isinstance(level_data, dict) and level_data.get("UnlockWeaponMode")
                for level_data in building_data.values()
            )
            superchargeable = False

            supercharge_level_data = {}
            for supercharge_data in self.full_supercharges_data.values():
                if supercharge_data.get("TargetBuilding") == building_name:
                    superchargeable = True
                    hold_data = {
                        "upgrade_resource": self._parse_resource(resource=supercharge_data.get("BuildResource")),
                        "levels": [],
                    }
                    for level, level_data in supercharge_data.items():
                        if not isinstance(level_data, dict):
                            continue
                        upgrade_time_seconds = self._parse_upgrade_time(level_data)

                        DPS = level_data.get("DPS", 0)
                        # if the level doesnt have a DPS & there is no hitpoints for this row, that means it is a DPS upgrade
                        # unless it is a resource pump, but we dont handle those anyways
                        if not DPS and not level_data.get("Hitpoints"):
                            DPS = supercharge_data.get("DPS", 0)
                        hold_data["levels"].append(
                            {
                                "level": int(level),
                                "build_cost": level_data.get("BuildCost"),
                                "build_time": upgrade_time_seconds,
                                "hitpoints_buff": level_data.get("Hitpoints", 0),
                                "dps_buff": DPS,
                            }
                        )
                    supercharge_level_data = hold_data
                    break

            # for merged buildings, move the requirement to level 1 since that is when the requirement is actually needed
            if building_data.get("MergeRequirement") is not None:
                building_data["1"]["MergeRequirement"] = building_data.get("MergeRequirement")

            hold_data = {
                "_id": building_data.get("GlobalID"),
                "name": self._translate(tid=building_data.get("TID")),
                "info": self._translate(tid=building_data.get("InfoTID")),
                "TID": {"name": building_data.get("TID"), "info": building_data.get("InfoTID")},
                "type": building_data.get("BuildingClass"),
                # we are going to do this purely for builder hut purposes bc lv 1 is gems
                "upgrade_resource": self._parse_resource(
                    resource=building_data.get("BuildResource") or building_data.get("2").get("BuildResource")
                ),
                "village": "home" if not village_type else "builderBase",
                "width": building_data.get("Width", 1),  # walls are null for some reason, so let's make it 1
                "superchargeable": superchargeable,
            }

            if is_defense and not has_level_targeting:
                hold_data.update({
                    "is_air_targeting": self._first_present("AirTargets", building_data, default=False),
                    "is_ground_targeting": self._first_present("GroundTargets", building_data, default=False),
                })
                if has_alternate_mode:
                    alt_name_tid = (
                        building_data.get("AlternateModeTID")
                        or building_data.get("AltNameTID")
                        or building_data.get("AltTID")
                    )
                    hold_data["alt"] = {
                        "name": self._translate(tid=alt_name_tid),
                        "is_air_targeting": self._first_present(
                            "AltAirTargets",
                            building_data,
                            default=hold_data["is_air_targeting"],
                        ),
                        "is_ground_targeting": self._first_present(
                            "AltGroundTargets",
                            building_data,
                            default=hold_data["is_ground_targeting"],
                        ),
                    }

            if is_defense and building_data.get("TargetingConeAngle"):
                hold_data["cone_angle"] = building_data.get("TargetingConeAngle")
                if building_data.get("AimRotateStep") is not None:
                    hold_data["aim_rotate_step"] = building_data.get("AimRotateStep")

            hold_data["levels"] = []

            # put seasonal defense onto the crafting station
            if building_data.get("GlobalID") == 1000097:
                # seasonal defenses are a max townhall thing
                seasonal_def_data = self._parse_seasonal_defense_data()
                hold_data["seasonal_defenses"] = seasonal_def_data

            if building_data.get("GearUpLevelRequirement"):
                hold_data["gear_up"] = {
                    "level_required": building_data.get("GearUpLevelRequirement"),
                    "resource": self._parse_resource(resource=building_data.get("GearUpResource")),
                    "building_id": self.full_building_data.get(building_data.get("GearUpBuilding")).get("GlobalID"),
                }

            for level, level_data in building_data.items():
                if not isinstance(level_data, dict):
                    continue

                source_sc = level_data.get("SWF") or building_data.get("SWF")
                asset_name = level_data.get("ExportName") or building_data.get("ExportName")
                if source_sc and asset_name:
                    building_level = level_data.get("BuildingLevel") or level
                    building_folder = self.village_asset_folder("buildings", village_type)
                    icon_asset_name = self.building_icon_asset_name(building_data, level_data, asset_name)
                    base_asset_name = None
                    if building_data.get("TID") in {
                        "TID_BUILDING_HOUSING",
                        "TID_SIEGE_WORKSHOP",
                        "TID_PET_SHOP",
                    }:
                        base_asset_name = level_data.get("ExportNameBase") or building_data.get("ExportNameBase")
                    last_frame = self.building_icon_uses_last_frame(building_data)
                    self.register_sc_asset(
                        source_sc=source_sc,
                        asset_name=icon_asset_name,
                        save_path=(
                            f"{building_folder}/"
                            f"{self.clean_name(self._translate(tid=building_data.get('TID')))}/"
                            f"level_{building_level}"
                        ),
                        first_frame=not last_frame,
                        last_frame=last_frame,
                        base_asset_name=base_asset_name,
                        base_source_sc="sc/building_bases.sc" if base_asset_name else None,
                    )

                upgrade_time_seconds = self._parse_upgrade_time(level_data)
                hold_level_data = {
                    "level": level_data.get("BuildingLevel"),
                    "build_cost": level_data.get("BuildCost", 0),
                    "build_time": upgrade_time_seconds,
                    "required_townhall": level_data.get("TownHallLevel"),
                    "hitpoints": level_data.get("Hitpoints", 0),
                }
                if is_defense:
                    defense_stats_source = building_data
                    defense_stats_level = level_data
                    if weapon_name := level_data.get("UnlockWeaponMode"):
                        weapon_data = full_weapon_data.get(weapon_name, {})
                        defense_stats_source = weapon_data
                        defense_stats_level = {}
                        hold_level_data["name"] = self._translate(
                            tid=weapon_data.get("ShortTID") or weapon_data.get("TID")
                        )
                        effect_range = self._spell_tower_effect_range(
                            weapon_data=weapon_data,
                            projectile_data=full_projectile_data,
                            spell_data=full_spell_data,
                        )
                        if effect_range is not None:
                            hold_level_data["effect_range"] = effect_range
                        hold_level_data["is_air_targeting"] = self._first_present(
                            "AirTargets",
                            weapon_data,
                            default=False,
                        )
                        hold_level_data["is_ground_targeting"] = self._first_present(
                            "GroundTargets",
                            weapon_data,
                            default=False,
                        )

                    defense_stats = self._defense_level_stats(defense_stats_source, defense_stats_level)
                    if level_data.get("UnlockWeaponMode"):
                        defense_stats.pop("dps", None)
                        defense_stats.pop("damage", None)
                    hold_level_data.update(defense_stats)
                    if has_alternate_mode:
                        hold_level_data["alt"] = self._defense_level_stats(
                            building_data,
                            level_data,
                            prefix="Alt",
                            fallback_stats=defense_stats,
                        )
                else:
                    dps = level_data.get("DPS", 0) or level_data.get("Damage", 0)
                    hold_level_data["dps"] = dps
                    attack_speed = self._first_present("AttackSpeed", level_data, building_data) if dps else None
                    if attack_speed is not None:
                        hold_level_data["attack_speed"] = attack_speed
                    # only weaponized levels get the archetype range (e.g. the Builder's Hut is
                    # unarmed at level 1 and only becomes a defense at level 2)
                    attack_range = self._first_present("AttackRange", level_data, building_data) if dps else None
                    if attack_range is None and building_data.get("GlobalID") == 1000014:  # clan castle
                        attack_range = clan_castle_attack_range
                    if attack_range is not None:
                        hold_level_data["attack_range"] = attack_range

                if "StrengthWeight" in level_data:
                    hold_level_data["strength_weight"] = level_data["StrengthWeight"]

                if "AltBuildResource" in level_data:
                    # a wall specific thing since they can use gold + elixir at certain levels
                    hold_level_data["alt_upgrade_resource"] = self._parse_resource(
                        resource=level_data["AltBuildResource"]
                    )

                if merge_requirement := level_data.get("MergeRequirement"):
                    merge_list = []
                    buildings = merge_requirement.split(";")
                    for building in buildings:
                        name, level, geared_up = building.split(":")
                        merge_building_data = self.full_building_data.get(name)
                        merge_list.append(
                            {
                                "name": self._translate(tid=merge_building_data.get("TID")),
                                "_id": merge_building_data.get("GlobalID"),
                                "geared_up": True if geared_up == "1" else False,
                                "level": int(level),
                            }
                        )

                    hold_level_data["merge_requirement"] = merge_list

                if (weapon_name := level_data.get("Weapon")) is not None:
                    weapon_data: dict = full_weapon_data[weapon_name]

                    # if the townhall only has 1 level of weapon, then it is inherently part of the base level,
                    # so just set the dps and continue
                    if weapon_data.get("1") is None:
                        hold_level_data["dps"] = weapon_data.get("DPS")
                    weapon_attack_speed = weapon_data.get("AttackSpeed")
                    if weapon_attack_speed is not None:
                        hold_level_data["attack_speed"] = weapon_attack_speed
                    weapon_range = weapon_data.get("AttackRange")
                    if weapon_range is not None:
                        hold_level_data["attack_range"] = weapon_range

                    if weapon_data.get("1") is not None:
                        hold_weapon_data = {
                            "name": self._translate(tid=weapon_data["TID"]),
                            "info": self._translate(tid=weapon_data["InfoTID"]),
                            "TID": {"name": weapon_data.get("TID"), "info": weapon_data.get("InfoTID")},
                            "upgrade_resource": self._parse_resource(resource=building_data.get("BuildResource")),
                            "strength_weight": level_data.get("StrengthWeight", 0),
                            "levels": [],
                        }
                        for weapon_level, weapon_level_data in weapon_data.items():
                            if not isinstance(weapon_level_data, dict):
                                continue

                            upgrade_time_seconds = self._parse_upgrade_time(weapon_level_data)
                            hold_weapon_level_data = {
                                "level": weapon_level_data.get("Level"),
                                "build_cost": level_data.get("BuildCost"),
                                "build_time": upgrade_time_seconds,
                                "dps": weapon_level_data.get("DPS"),
                            }
                            weapon_level_attack_speed = weapon_level_data.get("AttackSpeed")
                            if weapon_level_attack_speed is not None:
                                hold_weapon_level_data["attack_speed"] = weapon_level_attack_speed
                            hold_weapon_data["levels"].append(hold_weapon_level_data)
                        hold_level_data["weapon"] = hold_weapon_data

                hold_data["levels"].append(hold_level_data)

            if superchargeable:
                # supercharges are always on the last available level
                hold_data["levels"][-1]["supercharge"] = supercharge_level_data
            new_building_data.append(hold_data)

        lab_data = next((item for item in new_building_data if item["name"] == "Laboratory")).get("levels")
        lab_to_townhall = {spot: level_data.get("required_townhall") for spot, level_data in enumerate(lab_data, 1)}
        lab_to_townhall[-1] = 1  # there are troops with no lab ...
        lab_to_townhall[0] = 2
        self.lab_to_townhall = lab_to_townhall

        blacksmith_data = next((item for item in new_building_data if item["name"] == "Blacksmith")).get("levels")
        self.smithy_to_townhall = {
            spot: level_data.get("required_townhall") for spot, level_data in enumerate(blacksmith_data, 1)
        }

        pet_house_data = next((item for item in new_building_data if item["name"] == "Pet House")).get("levels")
        self.pethouse_to_townhall = {
            spot: level_data.get("required_townhall") for spot, level_data in enumerate(pet_house_data, 1)
        }

        bb_lab_data = next((item for item in new_building_data if item["name"] == "Star Laboratory")).get("levels")
        self.bb_lab_to_townhall = {
            spot: level_data.get("required_townhall") for spot, level_data in enumerate(bb_lab_data, 1)
        }

        townhall_unlocks, builderhall_unlocks = self._parse_hall_data()

        for building in new_building_data:
            unlocks = []
            if building["_id"] == 1000001:  # townhall id
                unlocks = townhall_unlocks
            elif building["_id"] == 1000034:  # builderhall id
                unlocks = builderhall_unlocks

            for unlock_data in unlocks:
                building["levels"][(unlock_data["level"] - 1)]["unlocks"] = unlock_data["buildings_unlocked"]

        return new_building_data

    def _parse_seasonal_defense_data(self):
        full_seasonal_defenses = self.open_file("logic/seasonal_defense_archetypes.json")
        full_seasonal_modules = self.open_file("logic/seasonal_defense_modules.json")
        full_season_data = self.open_file("logic/seasonal_defense.json")

        seasons = []
        for season_data in full_season_data.values():
            seasons.append(season_data)

        seen_tids = set()
        current_season = {}
        for season_data in seasons:
            season_tid = season_data.get("TID")
            if not season_tid or season_tid in seen_tids:
                continue
            seen_tids.add(season_tid)
            current_season = season_data
        current_seasonal_defenses: list[str] = [v.get("Archetypes") for k, v in current_season.items() if k.isdigit()]

        for _id, (n, d) in enumerate(full_seasonal_modules.items(), 102000000):
            d["_id"] = _id

        current_max_townhall = int(list(self.full_townhall_data.keys())[-1])
        new_seasonal_defense_data = []
        for _id, (seasonal_def_name, seasonal_def_data) in enumerate(full_seasonal_defenses.items(), 103000000):
            if seasonal_def_name not in current_seasonal_defenses:
                continue

            season_defense_ability = self.full_abilities_data.get(seasonal_def_data.get("SpecialAbility"))

            name_TID = season_defense_ability.get("OverrideTID")
            info_TID = season_defense_ability.get("OverrideInfoTID")

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=name_TID),
                "info": self._translate(tid=info_TID),
                "TID": {"name": name_TID, "info": info_TID},
                "required_townhall": current_max_townhall,
                "modules": [],
            }
            for count, module in enumerate(seasonal_def_data.get("Modules").split(";"), 1):
                module_data = full_seasonal_modules.get(module)

                module_hold_data = {
                    "_id": module_data.get("_id"),
                    "name": self._translate(tid=module_data.get("TID")),
                    "TID": {"name": module_data.get("TID")},
                    "upgrade_resource": self._parse_resource(module_data.get("BuildResource")),
                    "levels": [],
                }
                for level, level_data in module_data.items():
                    if not isinstance(level_data, dict):
                        continue
                    upgrade_time_seconds = self._parse_upgrade_time(level_data)

                    ability_data = self.full_abilities_data.get(module_data.get("SpecialAbility")).get(level)
                    ability_data.pop("ActivateFromGameSystem", None)
                    ability_data.pop("DeactivateFromGameSystem", None)
                    ability_data.pop("Level", None)

                    module_hold_data["levels"].append(
                        {
                            "level": int(level),
                            "build_cost": level_data.get("BuildCost"),
                            "build_time": upgrade_time_seconds,
                            "ability_data": ability_data,
                        }
                    )

                hold_data["modules"].append(module_hold_data)

            new_seasonal_defense_data.append(hold_data)

        return new_seasonal_defense_data

    def _parse_troop_data(self):
        self.full_troop_data = self.open_file("logic/characters.json")
        full_super_troop_data = self.open_file("logic/super_licences.json")
        full_super_troop_data = {v.get("Replacement"): v for k, v in full_super_troop_data.items()}

        name_to_id = {}
        new_troop_data = []
        for troop_name, troop_data in self.full_troop_data.items():
            if troop_data.get("DisableProduction", False):
                continue
            village_type = troop_data.get("VillageType", 0)
            production_building = self.full_building_data.get(troop_data.get("ProductionBuilding")).get("TID")

            name_to_id[(troop_name, village_type)] = troop_data.get("GlobalID")

            self.register_sc_asset(
                source_sc=troop_data.get("IconSWF"),
                asset_name=troop_data.get("IconExportName"),
                save_path=f'troops/{self.clean_name(self._translate(tid=troop_data.get("TID")))}/icon',
            )

            # this is where we need to do processing, go to the file & grab that target file
            hold_data = {
                "_id": troop_data.get("GlobalID"),
                "name": self._translate(tid=troop_data.get("TID")),
                "info": self._translate(tid=troop_data.get("InfoTID")),
                "TID": {"name": troop_data.get("TID"), "info": troop_data.get("InfoTID")},
                "production_building": self._translate(tid=production_building),
                "production_building_level": troop_data.get("BarrackLevel"),
                "upgrade_resource": self._parse_resource(resource=troop_data.get("UpgradeResource")),
                "is_flying": troop_data.get("IsFlying"),
                "is_air_targeting": troop_data.get("AirTargets"),
                "is_ground_targeting": troop_data.get("GroundTargets"),
                "movement_speed": troop_data.get("Speed", 0),
                "attack_speed": troop_data.get("AttackSpeed", 0),
                "attack_range": troop_data.get("AttackRange", 0),
                "housing_space": troop_data.get("HousingSpace"),
                "village": "home" if not village_type else "builderBase",
            }

            is_super_troop = troop_data.get("EnabledBySuperLicence", False)
            is_seasonal_troop = troop_data.get("EnabledByCalendar", False)
            super_troop_data = None
            if is_super_troop:
                super_troop_data = full_super_troop_data.get(troop_name)
                hold_data["super_troop"] = {
                    "original_id": name_to_id[(super_troop_data["Original"], 0)],
                    "original_min_level": super_troop_data["MinOriginalLevel"],
                }
            if is_seasonal_troop:
                hold_data["is_seasonal"] = True
            hold_data["levels"] = []

            max_townhall_converter = self.lab_to_townhall
            if troop_data.get("ProductionBuilding") == "Barrack2":
                max_townhall_converter = self.bb_lab_to_townhall

            for level, level_data in troop_data.items():  # type: str, dict
                if not isinstance(level_data, dict):
                    continue
                # convert times to seconds, all times for all things will be in seconds
                upgrade_time_seconds = self._parse_upgrade_time(level_data)

                if not is_super_troop and not is_seasonal_troop:
                    required_lab_level = level_data.get("LaboratoryLevel")
                    required_townhall = max_townhall_converter[level_data.get("LaboratoryLevel")]
                elif is_super_troop:  # for super troops use the original troop's lab level'
                    original_troop = self.full_troop_data.get(super_troop_data["Original"])
                    required_lab_level = original_troop.get(level).get("LaboratoryLevel")
                    required_townhall = max_townhall_converter[required_lab_level]
                elif is_seasonal_troop:
                    required_lab_level = None
                    required_townhall = level_data.get("UpgradeLevelByTH")
                else:
                    continue

                new_level_data = {
                    "level": int(level),
                    "hitpoints": level_data.get("Hitpoints", 0),
                    "dps": level_data.get("DPS", 0),
                    "upgrade_time": upgrade_time_seconds,
                    "upgrade_cost": level_data.get("UpgradeCost", 0),
                    "required_lab_level": required_lab_level,
                    "required_townhall": required_townhall,
                    "strength_weight": level_data.get("StrengthWeight", 0),
                }
                hold_data["levels"].append(new_level_data)

            if not hold_data["levels"]:
                continue
            new_troop_data.append(hold_data)

        return new_troop_data

    def _parse_guardian_data(self):
        full_guardian_data = self.open_file("logic/guardians.json")

        new_guardian_data = []
        for _id, (guardian_name, guardian_data) in enumerate(full_guardian_data.items(), 107000000):
            if guardian_data.get("Deprecated", False):
                continue
            character_data = self.full_troop_data.get(guardian_data.get("CharacterDatas"))

            self.register_sc_asset(
                source_sc=guardian_data.get("IconSWF"),
                asset_name=guardian_data.get("IconExportName"),
                save_path=f'guardians/{self.clean_name(self._translate(tid=guardian_data.get("TID")))}/icon'
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=guardian_data.get("TID")),
                "info": self._translate(tid=guardian_data.get("InfoTID")),
                "TID": {"name": guardian_data.get("TID"), "info": guardian_data.get("InfoTID")},
                "upgrade_resource": self._parse_resource(resource=character_data.get("UpgradeResource")),
                "is_flying": character_data.get("IsFlying"),
                "is_air_targeting": character_data.get("AirTargets"),
                "is_ground_targeting": character_data.get("GroundTargets"),
                "movement_speed": character_data.get("Speed"),
                "attack_speed": character_data.get("AttackSpeed"),
                "attack_range": character_data.get("AttackRange"),
                "levels": [],
            }

            for level, level_data in character_data.items():
                if not isinstance(level_data, dict):
                    continue
                # convert times to seconds, all times for all things will be in seconds
                upgrade_time_seconds = self._parse_upgrade_time(level_data)

                # hard coded for now, didn't find where this is defined, except "HousesGuardians" on the Townhall data
                required_townhall = 18

                new_level_data = {
                    "level": int(level),
                    "hitpoints": level_data.get("Hitpoints"),
                    "dps": level_data.get("DPS"),
                    "upgrade_time": upgrade_time_seconds,
                    "upgrade_cost": level_data.get("UpgradeCost", 0),
                    "required_townhall": required_townhall,
                }
                hold_data["levels"].append(new_level_data)

            new_guardian_data.append(hold_data)

        return new_guardian_data

    def _parse_spell_data(self):
        full_spell_data = self.open_file("logic/spells.json")

        new_spell_data = []
        for spell_name, spell_data in full_spell_data.items():
            if spell_data.get("DisableProduction", False):
                continue

            self.register_sc_asset(
                source_sc=spell_data.get("IconSWF"),
                asset_name=spell_data.get("IconExportName"),
                save_path=f'spells/{self.clean_name(self._translate(spell_data.get("TID")))}',
            )

            production_building = self.full_building_data.get(spell_data.get("ProductionBuilding")).get("TID")
            hold_data = {
                "_id": spell_data.get("GlobalID"),
                "name": self._translate(spell_data.get("TID")),
                "info": self._translate(spell_data.get("InfoTID")),
                "TID": {"name": spell_data.get("TID"), "info": spell_data.get("InfoTID")},
                "production_building": self._translate(production_building),
                "production_building_level": spell_data.get("SpellForgeLevel"),
                "upgrade_resource": self._parse_resource(resource=spell_data.get("UpgradeResource")),
                "housing_space": spell_data.get("HousingSpace"),
            }
            is_seasonal_spell = spell_data.get("EnabledByCalendar", False)
            if is_seasonal_spell:
                hold_data["is_seasonal"] = is_seasonal_spell
            hold_data["levels"] = []

            for level, level_data in spell_data.items():
                if not isinstance(level_data, dict):
                    continue
                # convert times to seconds, all times for all things will be in seconds
                upgrade_time_seconds = self._parse_upgrade_time(level_data)

                duration_ms = level_data.get("NumberOfHits", 0) * level_data.get("TimeBetweenHitsMS", 0)
                radius = spell_data.get("Radius", 0) or level_data.get("Radius", 0)
                new_level_data = {
                    "level": int(level),
                    "duration": int(duration_ms / 1000),
                    "radius": round(radius / 100, 1),
                    "damage": level_data.get("Damage", 0) or level_data.get("PoisonDPS", 0),
                    "upgrade_time": upgrade_time_seconds,
                    "upgrade_cost": level_data.get("UpgradeCost", 0),
                    "required_lab_level": level_data.get("LaboratoryLevel"),
                    "required_townhall": level_data.get("UpgradeLevelByTH")
                    or self.lab_to_townhall[level_data.get("LaboratoryLevel")],
                    "strength_weight": level_data.get("StrengthWeight", 0),
                }
                hold_data["levels"].append(new_level_data)

            if not hold_data["levels"]:
                continue
            new_spell_data.append(hold_data)

        return new_spell_data

    def _parse_hero_data(self):
        self.full_hero_data = self.open_file("logic/heroes.json")

        new_hero_data = []
        for _id, (hero_name, hero_data) in enumerate(self.full_hero_data.items(), 28000000):

            village_type = hero_data.get("VillageType", 0)
            if not village_type: #we can only do hv hero icons for now
                self.register_sc_asset(
                    source_sc=hero_data.get("SquarePictureSWF"),
                    asset_name=hero_data.get("SquarePicture"),
                    save_path=f"heroes/{self.clean_name(self._translate(hero_data.get('TID')))}/icon.webp"
                )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=hero_data.get("TID")),
                "info": self._translate(hero_data.get("InfoTID")),
                "TID": {"name": hero_data.get("TID"), "info": hero_data.get("InfoTID")},
                "production_building": self._translate(tid="TID_HERO_TAVERN") if not village_type else None,
                "production_building_level": hero_data.get("1", {}).get("RequiredHeroTavernLevel"),
                "upgrade_resource": self._parse_resource(resource=hero_data.get("UpgradeResource")),
                "is_flying": hero_data.get("IsFlying"),
                "is_air_targeting": hero_data.get("AirTargets"),
                "is_ground_targeting": hero_data.get("GroundTargets"),
                "movement_speed": hero_data.get("Speed"),
                "attack_speed": hero_data.get("AttackSpeed"),
                "attack_range": hero_data.get("AttackRange"),
                "village": "home" if not village_type else "builderBase",
                "levels": [],
            }

            for level, level_data in hero_data.items():
                if not isinstance(level_data, dict):
                    continue
                # convert times to seconds, all times for all things will be in seconds
                upgrade_time_seconds = self._parse_upgrade_time(level_data)

                new_level_data = {
                    "level": int(level),
                    "hitpoints": level_data.get("Hitpoints"),
                    "dps": level_data.get("DPS"),
                    "upgrade_time": upgrade_time_seconds,
                    "upgrade_cost": level_data.get("UpgradeCost", 0),
                    "required_townhall": level_data.get("RequiredTownHallLevel"),
                    "required_hero_tavern_level": level_data.get("RequiredHeroTavernLevel"),
                    "strength_weight": level_data.get("StrengthWeight", 0),
                }
                hold_data["levels"].append(new_level_data)

            new_hero_data.append(hold_data)

        return new_hero_data

    def _parse_pet_data(self):
        full_pet_data = self.open_file("logic/pets.json")

        new_pet_data = []
        for _id, (pet_name, pet_data) in enumerate(full_pet_data.items(), 73000000):
            if pet_data.get("Deprecated", False) or pet_name in ["Phoenix Egg"]:
                continue

            self.register_sc_asset(
                source_sc=pet_data.get("IconSWF") ,
                asset_name=pet_data.get("IconExportName"),
                save_path=f'pets/{self.clean_name(self._translate(tid=pet_data.get("TID")))}/icon'
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=pet_data.get("TID")),
                "info": self._translate(tid=pet_data.get("InfoTID")),
                "TID": {"name": pet_data.get("TID"), "info": pet_data.get("InfoTID")},
                "production_building": self._translate(tid="TID_PET_SHOP"),
                "production_building_level": pet_data.get("1").get("LaboratoryLevel"),
                "upgrade_resource": self._parse_resource('DarkElixir'),
                "is_flying": pet_data.get("IsFlying"),
                "is_air_targeting": pet_data.get("AirTargets"),
                "is_ground_targeting": pet_data.get("GroundTargets"),
                "movement_speed": pet_data.get("Speed"),
                "attack_speed": pet_data.get("AttackSpeed"),
                "attack_range": pet_data.get("AttackRange"),
                "levels": [],
            }

            for level, level_data in pet_data.items():
                if not isinstance(level_data, dict):
                    continue
                # convert times to seconds, all times for all things will be in seconds
                upgrade_time_seconds = self._parse_upgrade_time(level_data)

                new_level_data = {
                    "level": int(level),
                    "hitpoints": level_data.get("Hitpoints"),
                    "dps": level_data.get("DPS"),
                    "upgrade_time": upgrade_time_seconds,
                    "upgrade_cost": level_data.get("UpgradeCost", 0),
                    "required_pet_house_level": level_data.get("LaboratoryLevel"),
                    "required_townhall": self.pethouse_to_townhall[level_data.get("LaboratoryLevel")],
                    "strength_weight": level_data.get("StrengthWeight", 0),
                }
                hold_data["levels"].append(new_level_data)

            new_pet_data.append(hold_data)

        return new_pet_data

    def _parse_equipment_data(self):
        full_equipment_data = self.open_file("logic/character_items.json")

        new_equipment_data = []
        for _id, (equipment_name, equipment_data) in enumerate(full_equipment_data.items(), 90000000):
            if equipment_data.get("Deprecated", False) or equipment_name.startswith("UNUSED"):
                continue

            self.register_sc_asset(
                source_sc=equipment_data.get("IconSWF"),
                asset_name=equipment_data.get("IconExportName"),
                save_path=f'equipment/{self.clean_name(self._translate(tid=equipment_data.get("TID")))}'
            )

            main_abilities = equipment_data.get("MainAbilities").split(";")
            extra_abilities = equipment_data.get("ExtraAbilities", "").split(";")
            hero_TID = self.full_hero_data.get(equipment_data.get("AllowedCharacters").split(";")[0]).get("TID")
            hold_data = {
                "_id": _id,
                "name": self._translate(tid=equipment_data.get("TID")),
                "info": self._translate(tid=equipment_data.get("InfoTID")),
                "TID": {
                    "name": equipment_data.get("TID"),
                    "info": equipment_data.get("InfoTID"),
                    "production_building": "TID_SMITHY",
                },
                "production_building": self._translate(tid="TID_SMITHY"),
                "production_building_level": equipment_data.get("1").get("RequiredBlacksmithLevel"),
                "rarity": equipment_data.get("Rarity", "").title(),
                "hero": self._translate(tid=hero_TID),
                "levels": [],
            }

            for level, level_data in equipment_data.items():
                if not isinstance(level_data, dict):
                    continue

                shiny_ore = 0
                glowy_ore = 0
                starry_ore = 0
                upgrade_resources = level_data.get("UpgradeResources", "").split(";")
                upgrade_costs = str(level_data.get("UpgradeCosts", "")).split(";")

                if upgrade_costs[0] != "":
                    for resource, cost in zip(upgrade_resources, upgrade_costs):
                        resource = resource.strip()
                        cost = int(cost)
                        if resource == "CommonOre":
                            shiny_ore += cost
                        elif resource == "RareOre":
                            glowy_ore += cost
                        elif resource == "EpicOre":
                            starry_ore += cost

                new_level_data = {
                    "level": int(level),
                    "hitpoints": level_data.get("Hitpoints", 0),
                    "dps": level_data.get("DPS", 0),
                    "heal_on_activation": level_data.get("HealOnActivation", 0),
                    "required_blacksmith_level": level_data.get("RequiredBlacksmithLevel"),
                    "required_townhall": self.smithy_to_townhall[level_data.get("RequiredBlacksmithLevel")],
                    "strength_weight": level_data.get("StrengthWeight", 0),
                    "upgrade_cost": {"shiny_ore": shiny_ore, "glowy_ore": glowy_ore, "starry_ore": starry_ore},
                }

                main_ability_levels = str(level_data.get("MainAbilityLevels", "")).split(";")

                if main_ability_levels[0] != "":
                    main_ability_json = []
                    for main_ability, main_ability_level in zip(main_abilities, main_ability_levels):
                        full_ability = self.full_abilities_data.get(main_ability)
                        ability = full_ability.get(main_ability_level)
                        ability["name"] = self._translate(tid=full_ability.get("TID"))
                        ability["info"] = self._translate(tid=full_ability.get("InfoTID"))
                        main_ability_json.append(ability)

                    if main_ability_json:
                        new_level_data["abilities"] = main_ability_json

                extra_ability_levels = str(level_data.get("ExtraAbilityLevels", "")).split(";")
                if extra_ability_levels[0] != "":
                    extra_ability_json = []
                    for extra_ability, extra_ability_level in zip(extra_abilities, extra_ability_levels):
                        full_ability = self.full_abilities_data.get(extra_ability)
                        ability = full_ability.get(extra_ability_level)
                        if ability:
                            ability["name"] = self._translate(tid=full_ability.get("TID"))
                            extra_ability_json.append(ability)

                    if extra_ability_json:
                        new_level_data["abilities"].extend(extra_ability_json)

                hold_data["levels"].append(new_level_data)

            new_equipment_data.append(hold_data)

        return new_equipment_data

    def _parse_trap_data(self):
        full_trap_data = self.open_file("logic/traps.json")

        new_trap_data = []
        for trap_name, trap_data in full_trap_data.items():
            if trap_data.get("Disabled", False) or trap_data.get("EnabledByCalendar", False):
                continue
            village_type = trap_data.get("VillageType", 0)

            hold_data = {
                "_id": trap_data.get("GlobalID"),
                "name": self._translate(tid=trap_data.get("TID")),
                "info": self._translate(tid=trap_data.get("InfoTID")),
                "TID": {"name": trap_data.get("TID"), "info": trap_data.get("InfoTID")},
                "width": trap_data.get("Width"),
                "air_trigger": trap_data.get("AirTrigger", False),
                "ground_trigger": trap_data.get("GroundTrigger", False),
                "damage_radius": trap_data.get("DamageRadius"),
                "trigger_radius": trap_data.get("TriggerRadius"),
                "village": "home" if not village_type else "builderBase",
                "upgrade_resource": self._parse_resource(resource=trap_data.get("BuildResource")),
                "levels": [],
            }
            for level, level_data in trap_data.items():
                if not isinstance(level_data, dict):
                    continue

                source_sc = level_data.get("SWF") or trap_data.get("SWF")
                asset_name = level_data.get("ExportName") or trap_data.get("ExportName")
                if source_sc and asset_name:
                    trap_folder = self.village_asset_folder("traps", village_type)
                    trap_level = level_data.get("TrapLevel") or level_data.get("Level") or level
                    if trap_data.get("TID") == "TID_PUSHER" and asset_name.endswith("_idle"):
                        asset_name = f"{asset_name}_0"
                    self.register_sc_asset(
                        source_sc=source_sc,
                        asset_name=asset_name,
                        save_path=(
                            f"{trap_folder}/"
                            f"{self.clean_name(self._translate(tid=trap_data.get('TID')))}/"
                            f"level_{trap_level}"
                        ),
                        first_frame=True,
                    )

                upgrade_time_seconds = self._parse_upgrade_time(level_data)

                hold_data["levels"].append(
                    {
                        "level": int(level),
                        "build_cost": level_data.get("BuildCost"),
                        "build_time": upgrade_time_seconds,
                        "required_townhall": level_data.get("TownHallLevel"),
                        "damage": level_data.get("Damage", 0),
                        "strength_weight": level_data.get("StrengthWeight", 0),
                    }
                )

            new_trap_data.append(hold_data)

        return new_trap_data

    def _parse_decoration_data(self):
        full_deco_data = self.open_file("logic/decos.json")
        new_deco_data = []
        for _id, (deco_name, deco_data) in enumerate(full_deco_data.items(), 18000000):
            if deco_data.get("TID") in ["TID_DECORATION_GENERIC", "TID_DECORATION_NATIONAL_FLAG"]:
                continue
            if "placeholder" in deco_name.lower() or deco_name == "Unused":
                continue

            source_sc = deco_data.get("SWF")
            asset_name = deco_data.get("ExportName")
            village_type = deco_data.get("VillageType", 0)
            if source_sc and asset_name:
                preferred_frame_label = "store_idle,idle_end,idle_start"
                if asset_name == "clasharama_superdeco_paint_3x3":
                    preferred_frame_label = "tap_end_01"
                static_only = asset_name == "wastelands_the_cogulator_superdeco_3x3"
                self.register_sc_asset(
                    source_sc=source_sc,
                    asset_name=asset_name,
                    save_path=f"{self.village_asset_folder('decorations', village_type)}/{self.clean_name(self._translate(tid=deco_data.get("TID")))}",
                    first_frame=not static_only,
                    static_only=static_only,
                    preferred_frame_label=None if static_only else preferred_frame_label,
                )

            hold_data = {
                "_id": _id,
                "name": self._translate(deco_data.get("TID")),
                "TID": {"name": deco_data.get("TID")},
                "width": deco_data.get("Width"),
                "not_in_shop": deco_data.get("NotInShop", False),
                "pass_reward": deco_data.get("BPReward", False),
                "max_count": deco_data.get("MaxCount", 1),
                "build_resource": self._parse_resource(resource=deco_data.get("BuildResource")),
                "build_cost": deco_data.get("BuildCost"),
                "village": "home" if not village_type else "builderBase",
            }
            new_deco_data.append(hold_data)

        return new_deco_data

    def _parse_capital_part_data(self):
        full_capital_part_data = self.open_file("logic/building_parts.json")
        new_capital_part_data = []
        for _id, (part_name, part_data) in enumerate(full_capital_part_data.items(), 82000000):
            if part_data.get("Deprecated", False):
                continue

            source_sc, asset_name = part_data.get("Sprite").split("#", 1)
            self.register_sc_asset(
                source_sc=source_sc,
                asset_name=asset_name,
                save_path=f'capital_house_parts/{_id}'
            )
            name = part_name.replace("PlayerHouse_", "").replace("_", " ").title()
            nums = 0
            for phrase in ["01", "02", "03", "04", "05", "06", "07", "08", "09", "10"]:
                if phrase in name:
                    name = name.replace(phrase, "")
                    nums = int(phrase)
            name = name.split(" ", 1)
            name = f"{name[1]} {name[0]}"
            if nums:
                name = f"{name} {nums}"

            # make it match the API enums
            type_mapping = {"Deco": "decoration"}
            slot_type = type_mapping.get(part_data.get("LayoutSlot"), part_data.get("LayoutSlot").lower())
            new_capital_part_data.append(
                {
                    "_id": _id,
                    "name": name.title(),
                    "slot_type": slot_type,
                    "pass_reward": part_data.get("BattlePassReward", False),
                }
            )

        return new_capital_part_data

    def _parse_obstacle_data(self):
        full_obstacle_data = self.open_file("logic/obstacles.json")

        new_obstacle_data = []
        for _id, (obstacle_name, obstacle_data) in enumerate(full_obstacle_data.items(), 8000000):
            village_type = obstacle_data.get("VillageType", 0)
            source_sc = obstacle_data.get("SWF")
            asset_name = obstacle_data.get("ExportName")
            if source_sc and asset_name:
                self.register_sc_asset(
                    source_sc=source_sc,
                    asset_name=asset_name,
                    save_path=f"{self.village_asset_folder('obstacles', village_type)}/{self.clean_name(self._translate(tid=obstacle_data.get("TID")))}",
                    first_frame=True,
                )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=obstacle_data.get("TID")),
                "TID": {"name": obstacle_data.get("TID")},
                "width": obstacle_data.get("Width"),
                "clear_resource": self._parse_resource(resource=obstacle_data.get("ClearResource")),
                "clear_cost": obstacle_data.get("ClearCost"),
                "loot_resource": self._parse_resource(resource=obstacle_data.get("LootResource")),
                "loot_count": obstacle_data.get("LootCount"),
                "village": "home" if not village_type else "builderBase",
            }
            new_obstacle_data.append(hold_data)

        return new_obstacle_data

    def _parse_scenery_data(self):
        full_scenery_data = self.open_file("logic/village_backgrounds.json")

        new_scenery_data = []
        for _id, (scenery_name, scenery_data) in enumerate(full_scenery_data.items(), 60000000):
            type_map = {"WAR": "war", "BB": "builderBase", "HOME": "home"}
            if scenery_data.get("HomeType") not in type_map:
                continue

            if not self._translate(tid=scenery_data.get("TID")):
                continue

            path_name = self.clean_name(self._translate(tid=scenery_data.get("TID")))
            if "Icon" in scenery_data:
                self.register_sc_asset(
                    source_sc=scenery_data["Icon"],
                    asset_name="",
                    save_path=f"sceneries/{path_name}/icon"
                )
            else:
                self.register_sc_asset(
                    source_sc=scenery_data["IconAnimSWF"],
                    asset_name=scenery_data["IconAnimExportName"],
                    save_path=f"sceneries/{path_name}/icon"
                )
            thumbnail_path = self.register_sc_asset(
                source_sc=scenery_data["Thumbnail"],
                asset_name="",
                save_path=f"sceneries/{path_name}/thumbnail"
            )

            music_path = None
            if "Music" in scenery_data:
                self.register_sc_asset(
                    source_sc=scenery_data["Music"],
                    asset_name="",
                    save_path=f"sceneries/{path_name}/music"
                )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=scenery_data.get("TID")),
                "TID": {"name": scenery_data.get("TID")},
                "type": type_map.get(scenery_data.get("HomeType")),
                "music": music_path,
                "thumbnail": thumbnail_path,
            }
            if scenery_data.get("FreeBackground", False):
                scenery_data["free"] = True
            if scenery_data.get("DefaultBackground", False):
                scenery_data["default"] = True

            new_scenery_data.append(hold_data)

        return new_scenery_data

    def _parse_skin_data(self):
        full_skin_data = self.open_file("logic/skins.json")

        new_skins_data = []
        for _id, (skin_name, skin_data) in enumerate(full_skin_data.items(), 52000000):
            character = skin_data.get("character") or skin_data.get("Character")
            if not skin_data.get("TID") or character not in self.full_hero_data.keys() or not skin_data.get("Tier"):
                continue


            self.register_sc_asset(
                source_sc=skin_data["Icon"],
                asset_name="",
                save_path=f'skins/{self.clean_name(self._translate(tid=skin_data.get("TID")))}/icon'
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=skin_data.get("TID")),
                "TID": {"name": skin_data.get("TID")},
                "tier": skin_data.get("Tier").title(),
                "character": character,
            }
            new_skins_data.append(hold_data)

        return new_skins_data

    def _parse_helper_data(self):
        full_helper_data = self.open_file("logic/villager_apprentices.json")

        new_helper_data = []
        for _id, (helper_name, helper_data) in enumerate(full_helper_data.items(), 93000000):

            self.register_sc_asset(
                source_sc=helper_data.get("IconSWF"),
                asset_name=helper_data.get("IconExportNameSelect"),
                save_path=f'helpers/{self.clean_name(self._translate(tid=helper_data.get("TID")))}'
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=helper_data.get("TID")),
                "info": self._translate(tid=helper_data.get("InfoTID")),
                "TID": {"name": helper_data.get("TID"), "info": helper_data.get("InfoTID")},
                "upgrade_resource": self._parse_resource(resource=helper_data.get("CostResource")),
                "levels": [],
            }
            for level, level_data in helper_data.items():
                if not isinstance(level_data, dict):
                    continue
                hold_data["levels"].append(
                    {
                        "level": int(level),
                        "required_townhall": level_data.get("RequiredTownHallLevel"),
                        "upgrade_cost": level_data.get("Cost"),
                        "boost_time_seconds": level_data.get("BoostTimeSeconds"),
                        "boost_multiplier": level_data.get("BoostMultiplier"),
                    }
                )

            new_helper_data.append(hold_data)

        return new_helper_data

    def _parse_battle_modifier_type(self, modifier_data: dict) -> str:
        if modifier_data.get("Heroes"):
            return "heroes"
        if modifier_data.get("Guardians"):
            return "guardians"
        return "buildings"

    def _parse_battle_modifiers(self):
        full_battle_modifier_data = self.open_file("logic/battle_modifiers.json")

        new_battle_modifier_data = []
        for battle_modifier_data in full_battle_modifier_data.values():
            hold_data = {
                "name": battle_modifier_data.get("Name"),
                "TID": {"name": battle_modifier_data.get("TID")},
                "modifiers": [],
            }

            for modifier_data in battle_modifier_data.values():
                if not isinstance(modifier_data, dict):
                    continue

                hold_modifier_data = {"type": self._parse_battle_modifier_type(modifier_data)}
                if modifier_data.get("Attacker") is not None:
                    hold_modifier_data["attacker"] = modifier_data.get("Attacker")
                if modifier_data.get("Defender") is not None:
                    hold_modifier_data["defender"] = modifier_data.get("Defender")
                if modifier_data.get("DamageMultiplier") is not None:
                    hold_modifier_data["damage_multiplier"] = modifier_data.get("DamageMultiplier")
                if modifier_data.get("HPMultiplier") is not None:
                    hold_modifier_data["hp_multiplier"] = modifier_data.get("HPMultiplier")

                hold_data["modifiers"].append(hold_modifier_data)

            if battle_modifier_data.get("CommonEquipmentMinusLevels") is not None:
                hold_data["modifiers"].append(
                    {
                        "type": "common_equipment",
                        "attacker": True,
                        "minus_levels": battle_modifier_data.get("CommonEquipmentMinusLevels"),
                    }
                )
            if battle_modifier_data.get("EpicEquipmentMinusLevels") is not None:
                hold_data["modifiers"].append(
                    {
                        "type": "epic_equipment",
                        "attacker": True,
                        "minus_levels": battle_modifier_data.get("EpicEquipmentMinusLevels"),
                    }
                )

            new_battle_modifier_data.append(hold_data)

        return new_battle_modifier_data

    def _parse_war_league_data(self):
        full_war_league_data = self.open_file("logic/war_leagues.json")

        new_war_league_data = []
        for _id, (war_league_name, war_league_data) in enumerate(full_war_league_data.items(), 48000000):
            if not war_league_data.get("Name"):  # skip Unranked, no data
                continue

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=war_league_data.get("TID")),
                "TID": {"name": war_league_data.get("TID")},
                "cwl_medals": {
                    "first_place": war_league_data.get("LeagueWinReward"),
                    "position_medal_diff": war_league_data.get("LeaguePosRewardEffect"),
                    "bonus_reward": war_league_data.get("BonusMedalReward"),
                    "minimum_bonus_amount": war_league_data.get("MinNumMedalBonuses"),
                },
                "promotions": war_league_data.get("NumPromotions"),
                "demotions": war_league_data.get("NumDemotions"),
                "15v15_only": war_league_data.get("AllowFirstWarSizeOnly"),
            }
            if war_league_data.get("BattleModifier") is not None:
                hold_data["battle_modifier"] = war_league_data.get("BattleModifier")

            new_war_league_data.append(hold_data)

        return new_war_league_data

    def _parse_league_tier_data(self):
        full_league_tier_data = self.open_file("logic/league_tiers.json")

        new_league_tier_data = []
        for _id, (league_name, league_data) in enumerate(full_league_tier_data.items(), 105000000):
            league_tier = _id - 105000000
            hold_data = {
                "_id": _id,
                "name": self._translate(tid=league_data.get("TID")),
                "league_tier": league_tier,
                "TID": {"name": league_data.get("TID")},
                "group_size": league_data.get("GroupSizeMax"),
                "demote_percentage": league_data.get("DemotePercentage"),
                "promote_percentage": league_data.get("PromotePercentage"),
                "battle_count": league_data.get("MaxBattleCount"),
                "trophy_start": league_data.get("TrophyFloor"),
                "clan_score": league_data.get("TopClanScore"),
                "townhall_cap": None,
                "rewards": [],
            }
            if league_data.get("BattleModifier") is not None:
                hold_data["battle_modifier"] = league_data.get("BattleModifier")

            highest_townhall = 0
            rewards = []
            for tier, level_data in league_data.items():
                if not isinstance(level_data, dict):
                    continue
                if tier == "1":  # always empty idky
                    continue
                townhall_level = level_data.get("TH")
                th_min_league_tier = self.full_townhall_data.get(str(townhall_level)).get("LeagueTier", 0)
                if league_tier < th_min_league_tier and league_tier != 0:
                    continue
                highest_townhall = max(townhall_level, highest_townhall)
                rewards.append(
                    {
                        "townhall_level": townhall_level,
                        "resources": {
                            "gold": level_data.get("GoldReward"),
                            "elixir": level_data.get("ElixirReward"),
                            "dark_elixir": level_data.get("DarkElixirReward"),
                        },
                        "star_bonus": {
                            "gold": level_data.get("GoldRewardStarBonus"),
                            "elixir": level_data.get("ElixirRewardStarBonus"),
                            "dark_elixir": level_data.get("DarkElixirRewardStarBonus"),
                            "shiny_ore": level_data.get("CommonOreRewardStarBonus"),
                            "glowy_ore": level_data.get("RareOreRewardStarBonus"),
                            "starry_ore": level_data.get("EpicOreRewardStarBonus"),
                        },
                    }
                )
            hold_data["townhall_cap"] = highest_townhall
            hold_data["rewards"] = rewards
            new_league_tier_data.append(hold_data)

        return new_league_tier_data

    def _parse_builder_league_data(self):
        full_builder_league_data = self.open_file("logic/leagues2.json")

        new_league_data = []
        for _id, (league_name, league_data) in enumerate(full_builder_league_data.items(), 44000000):
            hold_data = {
                "_id": _id,
                "name": self._translate(tid=league_data.get("TID")),
                "TID": {"name": league_data.get("TID")},
                "trophy_start": league_data.get("TrophyLimitLow", 0),
            }
            new_league_data.append(hold_data)

        return new_league_data

    def _parse_capital_league_data(self):
        full_capital_league_data = self.open_file("logic/capital_leagues.json")

        new_league_data = []
        for _id, (league_name, league_data) in enumerate(full_capital_league_data.items(), 85000000):
            hold_data = {
                "_id": _id,
                "name": self._translate(tid=league_data.get("TID")),
                "TID": {"name": league_data.get("TID")},
                "trophy_start": league_data.get("RequiredTrophies", 0),
                "clan_xp": league_data.get("ClanXP")
            }
            new_league_data.append(hold_data)

        return new_league_data

    def _parse_magic_items_data(self):
        full_magic_items_data = self.open_file("logic/boosters.json")

        new_magic_items_data = []
        for name, magic_item_data in full_magic_items_data.items():
            _id = hash_15_digits(s=name)

            self.register_sc_asset(
                source_sc=magic_item_data.get("IconSWF"),
                asset_name=magic_item_data.get("IconExportName"),
                save_path=f'magic_items/{self.clean_name(self._translate(tid=magic_item_data.get("TID")))}',
                static_only=True,
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=magic_item_data.get("TID")),
                "info": self._translate(tid=magic_item_data.get("InfoTID")),
                "TID": {"name": magic_item_data.get("TID"), "info": magic_item_data.get("InfoTID")},
                "max_inventory": magic_item_data.get("MaxItems"),
                "gem_value": magic_item_data.get("DiamondValue"),
            }
            new_magic_items_data.append(hold_data)

        return new_magic_items_data

    def _parse_magic_snacks_data(self):
        full_magic_snacks_data = self.open_file("logic/consumables.json")

        new_magic_snacks_data = []
        for name, magic_snack_data in full_magic_snacks_data.items():
            _id = hash_15_digits(s=name)

            self.register_sc_asset(
                source_sc=magic_snack_data.get("IconSWF"),
                asset_name=magic_snack_data.get("IconExportName"),
                save_path=f'magic_snacks/{self.clean_name(self._translate(tid=magic_snack_data.get("TID")))}'
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=magic_snack_data.get("TID")),
                "info": self._translate(tid=magic_snack_data.get("InfoTID")),
                "TID": {"name": magic_snack_data.get("TID"), "info": magic_snack_data.get("InfoTID")},
                "duration": magic_snack_data.get("DurationSeconds", 0),
            }
            new_magic_snacks_data.append(hold_data)

        return new_magic_snacks_data

    def _parse_capital_districts(self):
        full_district_data = self.open_file("logic/capital_districts.json")

        new_district_data = []
        for _id, (district_name, district_data) in enumerate(full_district_data.items(), 70000000):
            hold_data = {
                "_id": _id,
                "name": self._translate(tid=district_data.get("TID")),
                "TID": {"name": district_data.get("TID")},
            }
            new_district_data.append(hold_data)

        return new_district_data

    def _parse_locale_mapping(self):
        full_locale_data = self.open_file("logic/chat_locales.json")
        locales = []
        for locale_data in full_locale_data.values():
            locales.append({
                "locale": locale_data.get("Name"),
                "name": locale_data.get("DisplayName"),
            })
        return locales

    def _parse_clan_labels(self):
        full_label_data = self.open_file("logic/clan_tags.json")

        new_label_data = []
        for name, label_data in full_label_data.items():
            _id = hash_15_digits(s=name)
            self.register_sc_asset(
                source_sc=label_data.get("IconSWF"),
                asset_name=label_data.get("IconExportName"),
                save_path=f"clan_labels/{self.clean_name(self._translate(tid=label_data.get('TID')))}"
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=label_data.get("TID")),
                "TID": {"name": label_data.get("TID")},

            }
            new_label_data.append(hold_data)

        return new_label_data

    def _parse_player_labels(self):
        full_label_data = self.open_file("logic/player_tags.json")

        new_label_data = []
        for name, label_data in full_label_data.items():
            _id = hash_15_digits(s=name)
            self.register_sc_asset(
                source_sc=label_data.get("IconSWF"),
                asset_name=label_data.get("IconExportName"),
                save_path=f'player_labels/{self.clean_name(self._translate(tid=label_data.get("TID")))}'
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=label_data.get("TID")),
                "TID": {"name": label_data.get("TID")},
            }
            new_label_data.append(hold_data)

        return new_label_data

    def _parse_chests_data(self):
        full_chest_data = self.open_file("logic/random_reward_pools.json")
        # full_chest_reward_data = self.open_file("logic/random_reward_boxes.json")

        new_chests_data = []
        for name, chest_data in full_chest_data.items():
            _id = hash_15_digits(s=name)
            self.register_sc_asset(
                source_sc=chest_data.get("IconSWF"),
                asset_name=chest_data.get("IconExportName"),
                save_path=f'chests/{self.clean_name(self._translate(tid=chest_data.get("TID")))}'
            )

            hold_data = {
                "_id": _id,
                "name": self._translate(tid=chest_data.get("TID")),
                "info": self._translate(tid=chest_data.get("InfoTID")),
                "TID": {"name": chest_data.get("TID"), "info": chest_data.get("InfoTID")},
            }
            new_chests_data.append(hold_data)

        return new_chests_data

    def _parse_resource_data(self):
        full_resource_data = self.open_file("logic/resources.json")

        new_resources_data = []
        for name, resource_data in full_resource_data.items():
            _id = hash_15_digits(s=name)
            if not resource_data.get("TID") or not resource_data.get("IconExportName"):
                continue

            self.register_sc_asset(
                source_sc=resource_data.get("IconSWF"),
                asset_name=resource_data.get("IconExportName"),
                save_path=f'resources/{self.clean_name(self._translate(tid=resource_data.get("TID")))}'
            )
            hold_data = {
                "_id": _id,
                "name": self._translate(tid=resource_data.get("TID")),
                "TID": {"name": resource_data.get("TID")},
            }
            new_resources_data.append(hold_data)

        return new_resources_data

    def _parse_hall_data(self):
        builderhall_data = []
        townhall_data = []
        id_quantity_map = {}

        trap_data = self.open_file("logic/traps.json")

        for _id, (hall_level, hall_data) in enumerate(self.full_townhall_data.items(), 1):
            builderhall_unlocks = []
            townhall_unlocks = []
            for field, data in hall_data.items():
                building_data = self.full_building_data.get(field) or trap_data.get(field)
                if not building_data or building_data.get("Disabled") or building_data.get("EnabledByCalendar"):
                    continue

                village_type = building_data.get("VillageType", 0)
                id = building_data.get("GlobalID")
                quantity = data

                current_quantity = id_quantity_map.get(id, 0)
                new_quantity = quantity - current_quantity

                if not new_quantity:
                    continue

                if id not in id_quantity_map:
                    id_quantity_map[id] = new_quantity
                else:
                    id_quantity_map[id] += new_quantity

                if not village_type:  # is home village
                    townhall_unlocks.append(
                        {"name": self._translate(tid=building_data.get("TID")), "_id": id, "quantity": new_quantity}
                    )
                else:
                    builderhall_unlocks.append(
                        {"name": self._translate(tid=building_data.get("TID")), "_id": id, "quantity": new_quantity}
                    )
            townhall_data.append({"level": _id, "buildings_unlocked": townhall_unlocks})
            if builderhall_unlocks:
                builderhall_data.append({"level": _id, "buildings_unlocked": builderhall_unlocks})

        return townhall_data, builderhall_data

    def create_master_json(self):
        self._parse_translation_data()

        self.full_abilities_data = self.open_file("logic/special_abilities.json")
        self.full_resource_data = self.open_file("logic/resources.json")
        self.animations_data = self.open_file("csv/animations.json")

        master_data = {
            "buildings": sorted(self._parse_building_data(), key=lambda x: x["_id"]),
            "traps": self._parse_trap_data(),
            "troops": sorted(self._parse_troop_data(), key=lambda x: x["_id"]),
            "guardians": self._parse_guardian_data(),
            "spells": sorted(self._parse_spell_data(), key=lambda x: x["_id"]),
            "heroes": self._parse_hero_data(),
            "pets": self._parse_pet_data(),
            "equipment": self._parse_equipment_data(),
            "decorations": self._parse_decoration_data(),
            "obstacles": self._parse_obstacle_data(),
            "sceneries": self._parse_scenery_data(),
            "skins": self._parse_skin_data(),
            "capital_house_parts": self._parse_capital_part_data(),
            "helpers": self._parse_helper_data(),
            "battle_modifiers": self._parse_battle_modifiers(),
            "war_leagues": self._parse_war_league_data(),
            "league_tiers": self._parse_league_tier_data(),
            "builder_leagues": self._parse_builder_league_data(),
            "capital_leagues": self._parse_capital_league_data(),
            "magic_items": self._parse_magic_items_data(),
            "magic_snacks": self._parse_magic_snacks_data(),
            "player_labels": self._parse_player_labels(),
            "clan_labels": self._parse_clan_labels(),
            "locales": self._parse_locale_mapping(),
            "chests": self._parse_chests_data(),
            "resources": self._parse_resource_data(),
            "achievements": self._parse_achievement_data(),
        }
        with open(f"{self.BASE_PATH}/static_data.json", "w", encoding="utf-8") as jf:
            jf.write(json.dumps(master_data, indent=2))

        if self.PRUNE_TRANSLATIONS:
            for key in list(self.translation_data.keys()):
                if key not in self.USED_TIDS:
                    del self.translation_data[key]

        with open(f"{self.BASE_PATH}/translations.json", "w", encoding="utf-8") as jf:
            jf.write(json.dumps(self.translation_data, indent=2))

        if not self.KEEP_JSON:
            for folder in ("csv", "logic", "localization"):
                root = Path(folder)
                if not root.exists():
                    continue
                for file_path in root.rglob("*.json"):
                    try:
                        file_path.unlink()
                    except OSError as e:
                        logging.warning(f"Could not delete {file_path}: {e}")

    def generate_constants(self):
        static_data = self.open_file("static_data.json")

        troops = static_data["troops"]
        spells = static_data["spells"]
        heroes = static_data["heroes"]
        equipment = static_data["equipment"]
        pets = static_data["pets"]
        buildings = static_data["buildings"]
        achievements = static_data["achievements"]

        lists_to_write = {
            'ELIXIR_TROOP_ORDER': [
                t["name"]
                for t in troops
                if t["production_building"] == "Barracks" and not t.get("is_seasonal", False) and not "super_troop" in t
            ],
            'DARK_ELIXIR_TROOP_ORDER': [
                t["name"]
                for t in troops
                if t["production_building"] == "Dark Barracks"
                and not t.get("is_seasonal", False)
                and not "super_troop" in t
            ],
            'HV_TROOP_ORDER': 'ELIXIR_TROOP_ORDER + DARK_ELIXIR_TROOP_ORDER',
            'SIEGE_MACHINE_ORDER': [t["name"] for t in troops if t["production_building"] == "Workshop"],
            'SUPER_TROOP_ORDER': [t["name"] for t in troops if "super_troop" in t],
            'HOME_TROOP_ORDER': 'HV_TROOP_ORDER + SIEGE_MACHINE_ORDER',
            'SEASONAL_TROOP_ORDER': [t["name"] for t in troops if t.get("is_seasonal", False)],
            'BUILDER_TROOPS_ORDER': [t["name"] for t in troops if t["village"] == "builderBase"],
            'ELIXIR_SPELL_ORDER': [
                s["name"] for s in spells if s["upgrade_resource"] == "Elixir" and not s.get("is_seasonal", False)
            ],
            'DARK_ELIXIR_SPELL_ORDER': [s["name"] for s in spells if s["upgrade_resource"] == "Dark Elixir"],
            'SEASONAL_SPELL_ORDER': [s["name"] for s in spells if s.get("is_seasonal", False)],
            'SPELL_ORDER': 'ELIXIR_SPELL_ORDER + DARK_ELIXIR_SPELL_ORDER',
            'HOME_BASE_HERO_ORDER': [
                h["name"]
                for h in sorted(heroes, key=lambda x: x["levels"][0]["required_townhall"])
                if h["village"] == "home"
            ],
            'BUILDER_BASE_HERO_ORDER': [h["name"] for h in heroes if h["village"] == "builderBase"],
            'HERO_ORDER': 'HOME_BASE_HERO_ORDER + BUILDER_BASE_HERO_ORDER',
            'PETS_ORDER': [p["name"] for p in pets],
            'EQUIPMENT': [e["name"] for e in equipment],
            'HV_BUILDINGS': [b["name"] for b in buildings if b["village"] == "home"],
            'ACHIEVEMENT_ORDER': [
                a["name"]
                for a in sorted(
                    achievements,
                    key=lambda x: (
                        {'home': 0, 'builderBase': 1, 'clanCapital': 2}.get(x["village"], 0),
                        -x["ui_priority"],
                    ),
                )
            ],  # same order as in-game
        }
        constants_path = Path(__file__).parent.parent / "constants.py"

        with open(constants_path, 'w') as f:
            f.write('"""Auto-generated constants from static game data."""\n\n')
            for name, lst in lists_to_write.items():
                if isinstance(lst, str):
                    f.write(f"{name} = {lst}\n\n")
                else:
                    # Manual formatting: each item on its own line
                    f.write(f"{name} = [\n")
                    for item in lst:
                        f.write(f"    {repr(item)},\n")
                    f.write("]\n\n")

        print(f"Constants written to {constants_path}")

    async def download_files(self):
        self.FINGERPRINT, fingerprint_file = await fetch_fingerprint_manifest(self.APK_URL, self.FINGERPRINT)

        BASE_URL = f"https://game-assets.clashofclans.com/{self.FINGERPRINT}"

        file_paths = []
        for file_data in fingerprint_file.get("files", []):
            file_path: str = file_data["file"]
            if (
                not file_path.startswith("logic/")
                and not file_path.startswith("localization/")
                # and file_path != "csv/animations.csv"
                and not file_path.endswith("csv")
            ):
                continue
            file_paths.append(file_path)

        async def download_and_process(file_path: str):
            download_url = f"{BASE_URL}/{file_path}"
            print(f"Downloading: {download_url}")
            data = await download_file(url=download_url)

            def process():
                print(f"Processing: {file_path}")
                if file_path == "csv/animations.csv":
                    self.process_animations_csv(data=data, file_path=file_path)
                else:
                    self.process_csv(data=data, file_path=file_path)

            await asyncio.to_thread(process)

        await asyncio.gather(*(download_and_process(file_path) for file_path in file_paths))

        self.create_master_json()
        await self.extract_assets()

    def run(self):
        asyncio.run(self.download_files())


if __name__ == "__main__":
    StaticUpdater().run()
