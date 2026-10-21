"""Mirror the generated asset emoji catalog to one verified Discord application.

The default is a read-only plan. Use --apply only after reviewing its output.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import io
import json
import os
import re
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from PIL import Image, ImageOps, UnidentifiedImageError
from generate_emoji_catalog import build_catalog

API_ROOT = 'https://discord.com/api/v10'
CDN_ROOT = 'https://cdn.discordapp.com'
MAX_EMOJIS = 2000
MAX_IMAGE_BYTES = 256 * 1024
NAME_RE = re.compile(r'[A-Za-z0-9_]{2,32}\Z')
SNOWFLAKE_RE = re.compile(r'[0-9]{15,22}\Z')
SHA_RE = re.compile(r'[a-f0-9]{64}\Z')


class EmojiSyncError(RuntimeError):
    pass


@dataclass(frozen=True)
class PreparedEmoji:
    name: str
    image: bytes
    pixels: bytes
    mime: str = 'image/png'
    animated: bool = False


@dataclass(frozen=True)
class RemoteEmoji:
    id: str
    name: str
    animated: bool = False


@dataclass(frozen=True)
class SyncPlan:
    create: tuple[str, ...]
    replace: tuple[str, ...]
    recover: tuple[str, ...]
    delete: tuple[str, ...]
    unchanged: tuple[str, ...]


def _canonical_pixels(image: Image.Image) -> bytes:
    rgba = image.convert('RGBA')
    if rgba.size != (128, 128):
        raise EmojiSyncError(f'Emoji image is {rgba.width}x{rgba.height}, expected 128x128')
    # Hidden RGB is not displayed and Discord may discard it during encoding.
    pixels = bytearray(rgba.tobytes())
    for index in range(0, len(pixels), 4):
        if pixels[index + 3] == 0:
            pixels[index : index + 3] = b'\0\0\0'
    return bytes(pixels)


def _prepare_png(source: bytes, name: str) -> PreparedEmoji:
    try:
        with Image.open(io.BytesIO(source)) as original:
            original.seek(0)
            image = ImageOps.exif_transpose(original).convert('RGBA')
    except (OSError, ValueError, UnidentifiedImageError) as exc:
        raise EmojiSyncError(f'Invalid source image for {name}: {exc}') from None
    if not image.width or not image.height:
        raise EmojiSyncError(f'Empty source image for {name}')
    image.thumbnail((128, 128), Image.Resampling.LANCZOS)
    canvas = Image.new('RGBA', (128, 128), (0, 0, 0, 0))
    canvas.alpha_composite(image, ((128 - image.width) // 2, (128 - image.height) // 2))
    pixels = _canonical_pixels(canvas)
    canvas = Image.frombytes('RGBA', (128, 128), pixels)
    output = io.BytesIO()
    canvas.save(output, format='PNG', optimize=True)
    png = output.getvalue()
    if len(png) > MAX_IMAGE_BYTES:
        raise EmojiSyncError(f'Prepared image for {name} exceeds 256 KiB')
    return PreparedEmoji(name, png, pixels)


def _animation_pixels(image: Image.Image) -> bytes:
    digest = hashlib.sha256()
    for frame_index in range(image.n_frames):
        image.seek(frame_index)
        digest.update(_canonical_pixels(image))
        digest.update(int(image.info.get('duration', 100)).to_bytes(4, 'big'))
    return digest.digest()


def _prepare_gif(source: bytes, name: str) -> PreparedEmoji:
    try:
        with Image.open(io.BytesIO(source)) as original:
            if original.format != 'GIF' or original.n_frames < 2:
                raise EmojiSyncError(f'Animated GIF for {name} must contain at least two frames')
            if original.size == (128, 128) and len(source) <= MAX_IMAGE_BYTES:
                return PreparedEmoji(name, source, _animation_pixels(original), 'image/gif', True)
            frames: list[Image.Image] = []
            durations: list[int] = []
            for frame_index in range(original.n_frames):
                original.seek(frame_index)
                frame = original.convert('RGBA')
                frame.thumbnail((128, 128), Image.Resampling.LANCZOS)
                canvas = Image.new('RGBA', (128, 128), (0, 0, 0, 0))
                canvas.alpha_composite(frame, ((128 - frame.width) // 2, (128 - frame.height) // 2))
                frames.append(canvas.quantize(colors=255, method=Image.Quantize.FASTOCTREE))
                durations.append(max(20, int(original.info.get('duration', 100))))
            output = io.BytesIO()
            frames[0].save(
                output,
                format='GIF',
                save_all=True,
                append_images=frames[1:],
                duration=durations,
                loop=original.info.get('loop', 0),
                disposal=2,
                optimize=True,
            )
    except (OSError, ValueError, UnidentifiedImageError) as exc:
        raise EmojiSyncError(f'Invalid animated GIF for {name}: {exc}') from None
    gif = output.getvalue()
    if len(gif) > MAX_IMAGE_BYTES:
        raise EmojiSyncError(f'Prepared animated GIF for {name} exceeds 256 KiB')
    with Image.open(io.BytesIO(gif)) as prepared:
        return PreparedEmoji(name, gif, _animation_pixels(prepared), 'image/gif', True)


def load_catalog(catalog_path: Path | None, assets_root: Path) -> dict[str, PreparedEmoji]:
    try:
        catalog = build_catalog(assets_root) if catalog_path is None else json.loads(catalog_path.read_text(encoding='utf-8'))
    except (OSError, ValueError) as exc:
        raise EmojiSyncError(f'Cannot read emoji catalog: {exc}') from None
    if not isinstance(catalog, dict) or catalog.get('version') != 1 or not isinstance(catalog.get('emojis'), list):
        raise EmojiSyncError('Emoji catalog must be a version 1 object with an emojis array')
    rows = catalog['emojis']
    if not rows or len(rows) > MAX_EMOJIS:
        raise EmojiSyncError('Emoji catalog must contain 1–2000 entries')
    names: dict[str, PreparedEmoji] = {}
    root = assets_root.resolve()
    for row in rows:
        if (
            not isinstance(row, dict)
            or not {'name', 'path', 'sha256'} <= row.keys()
            or not row.keys() <= {'name', 'path', 'sha256', 'category'}
        ):
            raise EmojiSyncError('Each catalog entry must contain name, path, sha256 and optional category')
        name, path, digest = row['name'], row['path'], row['sha256']
        if 'category' in row and (not isinstance(row['category'], str) or not row['category']):
            raise EmojiSyncError(f'Invalid category for {name}')
        if not isinstance(name, str) or not NAME_RE.fullmatch(name) or name.startswith('cksync_') or name in names:
            raise EmojiSyncError(f'Invalid or duplicate emoji name: {name!r}')
        if (
            not isinstance(path, str)
            or not path.startswith('/')
            or '\\' in path
            or not path.lower().endswith(('.webp', '.png', '.jpg', '.jpeg', '.gif'))
        ):
            raise EmojiSyncError(f'Invalid asset path for {name}')
        parts = path.lstrip('/').split('/')
        if any(part in ('', '.', '..') for part in parts):
            raise EmojiSyncError(f'Invalid asset path for {name}')
        if not isinstance(digest, str) or not SHA_RE.fullmatch(digest):
            raise EmojiSyncError(f'Invalid SHA-256 for {name}')
        image_path = (root / Path(*parts)).resolve()
        if not image_path.is_relative_to(root) or not image_path.is_file():
            raise EmojiSyncError(f'Asset file is missing or outside assets root for {name}')
        source = image_path.read_bytes()
        if hashlib.sha256(source).hexdigest() != digest:
            raise EmojiSyncError(f'Asset SHA-256 mismatch for {name}; regenerate the catalog')
        names[name] = _prepare_gif(source, name) if path.lower().endswith('.gif') else _prepare_png(source, name)
    return names


class DiscordClient:
    def __init__(self, token: str, application_id: str):
        if not token.strip():
            raise EmojiSyncError('DISCORD_BOT_TOKEN is required')
        if not SNOWFLAKE_RE.fullmatch(application_id):
            raise EmojiSyncError('DISCORD_APPLICATION_ID must be an explicit Discord snowflake')
        self.token = token.strip()
        self.application_id = application_id
        self.opener = urllib.request.build_opener(_NoRedirect())

    def _request(self, method: str, url: str, payload: dict[str, Any] | None = None, *, cdn: bool = False) -> bytes:
        data = json.dumps(payload).encode('utf-8') if payload is not None else None
        headers = {'User-Agent': 'ClashKingEmojiSync/1.0', 'Accept': 'application/json'}
        if not cdn:
            headers['Authorization'] = f'Bot {self.token}'
        if data is not None:
            headers['Content-Type'] = 'application/json'
        request = urllib.request.Request(url, data=data, method=method, headers=headers)
        for attempt in range(4):
            try:
                with self.opener.open(request, timeout=30) as response:
                    return response.read(MAX_IMAGE_BYTES * 8 if cdn else 2_000_000)
            except urllib.error.HTTPError as exc:
                if exc.code == 429 and attempt < 3:
                    try:
                        details = json.loads(exc.read(4096))
                        wait = float(details.get('retry_after', exc.headers.get('Retry-After', '1')))
                    except (ValueError, TypeError, AttributeError):
                        wait = 1.0
                    if not 0 <= wait <= 60:
                        raise EmojiSyncError('Discord rate limit wait exceeded 60 seconds') from None
                    time.sleep(max(wait, 0.25))
                    continue
                if 500 <= exc.code <= 599 and attempt < 3:
                    time.sleep(attempt + 1)
                    continue
                raise EmojiSyncError(
                    f'Discord request failed (HTTP {exc.code}) for {method} {url.split("?", 1)[0]}'
                ) from None
            except OSError as exc:
                if attempt < 3:
                    time.sleep(attempt + 1)
                    continue
                raise EmojiSyncError(f'Discord request failed for {method}: {exc}') from None
        raise EmojiSyncError('Discord request retries exhausted')

    def _json(self, method: str, path: str, payload: dict[str, Any] | None = None) -> Any:
        try:
            return json.loads(self._request(method, API_ROOT + path, payload))
        except ValueError:
            raise EmojiSyncError(f'Discord returned invalid JSON for {method} {path}') from None

    def verify_application(self) -> None:
        identity = self._json('GET', '/oauth2/applications/@me')
        if not isinstance(identity, dict) or identity.get('id') != self.application_id:
            raise EmojiSyncError('Bot token application does not match DISCORD_APPLICATION_ID')

    def list_emojis(self) -> dict[str, RemoteEmoji]:
        result = self._json('GET', f'/applications/{self.application_id}/emojis')
        items = result.get('items') if isinstance(result, dict) else None
        if not isinstance(items, list) or len(items) > MAX_EMOJIS:
            raise EmojiSyncError('Discord returned an invalid application emoji inventory')
        remote: dict[str, RemoteEmoji] = {}
        for item in items:
            if (
                not isinstance(item, dict)
                or not isinstance(item.get('name'), str)
                or not isinstance(item.get('id'), str)
            ):
                raise EmojiSyncError('Discord returned an invalid emoji object')
            emoji = RemoteEmoji(item['id'], item['name'], item.get('animated') is True)
            if not SNOWFLAKE_RE.fullmatch(emoji.id) or emoji.name in remote:
                raise EmojiSyncError('Discord returned duplicate names or invalid emoji IDs')
            remote[emoji.name] = emoji
        return remote

    def emoji_pixels(self, emoji: RemoteEmoji) -> bytes:
        extension = 'gif' if emoji.animated else 'png'
        url = f'{CDN_ROOT}/emojis/{emoji.id}.{extension}?size=128'
        raw = self._request('GET', url, cdn=True)
        try:
            # Discord preserves aspect ratio for older uploads even at size=128.
            # Compare both sources on the same centered 128px canvas.
            return (_prepare_gif(raw, emoji.name) if emoji.animated else _prepare_png(raw, emoji.name)).pixels
        except (OSError, ValueError, UnidentifiedImageError) as exc:
            raise EmojiSyncError(f'Cannot decode Discord emoji {emoji.id}: {exc}') from None

    def create(self, name: str, prepared: PreparedEmoji) -> RemoteEmoji:
        image = f'data:{prepared.mime};base64,' + base64.b64encode(prepared.image).decode('ascii')
        result = self._json('POST', f'/applications/{self.application_id}/emojis', {'name': name, 'image': image})
        if (
            not isinstance(result, dict)
            or not isinstance(result.get('id'), str)
            or not SNOWFLAKE_RE.fullmatch(result['id'])
            or result.get('name') != name
            or (result.get('animated') is True) != prepared.animated
        ):
            raise EmojiSyncError(f'Discord did not confirm emoji creation for {name}')
        return RemoteEmoji(result['id'], name, result.get('animated') is True)

    def rename(self, emoji: RemoteEmoji, name: str) -> RemoteEmoji:
        result = self._json('PATCH', f'/applications/{self.application_id}/emojis/{emoji.id}', {'name': name})
        if not isinstance(result, dict) or result.get('id') != emoji.id or result.get('name') != name:
            raise EmojiSyncError(f'Discord did not confirm emoji rename to {name}')
        return RemoteEmoji(emoji.id, name, emoji.animated)

    def delete(self, emoji: RemoteEmoji) -> None:
        self._request('DELETE', f'{API_ROOT}/applications/{self.application_id}/emojis/{emoji.id}')


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def temporary_name(name: str, image: bytes) -> str:
    return 'cksync_' + hashlib.sha256(name.encode()).hexdigest()[:8] + '_' + hashlib.sha256(image).hexdigest()[:8]


def plan_sync(desired: dict[str, PreparedEmoji], remote: dict[str, RemoteEmoji], client: DiscordClient) -> SyncPlan:
    create: list[str] = []
    replace: list[str] = []
    recover: list[str] = []
    unchanged: list[str] = []
    # Independent CDN reads are bounded; all complete before any mutation.
    candidates = [remote[name] for name, prepared in sorted(desired.items())
                  if name in remote and remote[name].animated == prepared.animated]
    with ThreadPoolExecutor(max_workers=8) as executor:
        existing_pixels = dict(zip((item.name for item in candidates), executor.map(client.emoji_pixels, candidates)))
    # All necessary CDN reads happen before any mutation, including temporary
    # replacements left by an interrupted prior run.
    for name, prepared in sorted(desired.items()):
        existing = remote.get(name)
        if existing and existing.animated == prepared.animated and existing_pixels[name] == prepared.pixels:
            unchanged.append(name)
            continue
        temporary = remote.get(temporary_name(name, prepared.image))
        if temporary and (temporary.animated != prepared.animated or client.emoji_pixels(temporary) != prepared.pixels):
            raise EmojiSyncError(f'Temporary emoji image mismatch for {name}')
        if existing:
            replace.append(name)
        elif temporary:
            recover.append(name)
        else:
            create.append(name)
    temp_to_keep = {
        temporary_name(name, desired[name].image)
        for name in replace + recover
        if temporary_name(name, desired[name].image) in remote
    }
    delete = sorted(name for name in remote if name not in desired and name not in temp_to_keep)
    # New names are created before obsolete names are deleted. A replacement
    # uses one temporary slot and releases it before the next replacement.
    if (
        len(remote)
        + len(create)
        + (1 if any(temporary_name(name, desired[name].image) not in remote for name in replace) else 0)
        > MAX_EMOJIS
    ):
        raise EmojiSyncError('Not enough application emoji slots to stage changes safely')
    return SyncPlan(tuple(create), tuple(replace), tuple(recover), tuple(delete), tuple(unchanged))


def apply_sync(
    plan: SyncPlan, desired: dict[str, PreparedEmoji], remote: dict[str, RemoteEmoji], client: DiscordClient
) -> None:
    for name in plan.replace:
        prepared = desired[name]
        temporary = remote.get(temporary_name(name, prepared.image))
        if temporary is None:
            temporary = client.create(temporary_name(name, prepared.image), prepared)
        client.delete(remote[name])
        client.rename(temporary, name)
    for name in plan.recover:
        prepared = desired[name]
        client.rename(remote[temporary_name(name, prepared.image)], name)
    for name in plan.create:
        client.create(name, desired[name])
    # Deletions only begin once every desired name/image has been staged.
    for name in plan.delete:
        client.delete(remote[name])


def run(catalog_path: Path | None, assets_root: Path, client: DiscordClient, *, apply: bool, preserve_unmanaged: bool = False) -> SyncPlan:
    desired = load_catalog(catalog_path, assets_root)
    client.verify_application()
    remote = client.list_emojis()
    plan = plan_sync(desired, remote, client)
    if preserve_unmanaged:
        plan = SyncPlan(plan.create, plan.replace, plan.recover, (), plan.unchanged)
    if apply:
        apply_sync(plan, desired, remote, client)
    return plan


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--catalog', type=Path, help='Optional build-time inventory override; normally derive from local assets')
    parser.add_argument('--assets-root', type=Path, default=Path(__file__).parent / 'assets')
    parser.add_argument('--preserve-unmanaged', action='store_true', help='Retain remote names outside the generated asset inventory')
    parser.add_argument(
        '--apply', action='store_true', help='Create, replace and delete emojis after a complete preflight'
    )
    args = parser.parse_args()
    try:
        client = DiscordClient(os.getenv('DISCORD_BOT_TOKEN', ''), os.getenv('DISCORD_APPLICATION_ID', ''))
        plan = run(args.catalog, args.assets_root, client, apply=args.apply, preserve_unmanaged=args.preserve_unmanaged)
        print(json.dumps({'mode': 'apply' if args.apply else 'dry-run', **plan.__dict__}, sort_keys=True))
    except EmojiSyncError as exc:
        raise SystemExit(str(exc)) from None


if __name__ == '__main__':
    main()
