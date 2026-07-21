from pathlib import Path


def test_viewer_consumes_hosted_manifest_contract():
    source = Path("assets/viewer.js").read_text(encoding="utf-8")

    assert 'const MANIFEST_URL = "https://assets.clashk.ing/manifest.json"' in source
    assert "api.github.com" not in source
    assert "data.assets" in source
    for field in ("entry.path", "entry.category", "entry.display_name", "entry.extension", "entry.url"):
        assert field in source


def test_viewer_keeps_supported_formats_and_bot_exclusion():
    source = Path("assets/viewer.js").read_text(encoding="utf-8")

    assert '["gif", "jpeg", "jpg", "png", "svg", "webp"]' in source
    assert 'entry.path.startsWith("bot/")' in source
