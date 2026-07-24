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


def test_viewer_exposes_browse_preview_and_asset_actions():
    page = Path("assets/viewer.html").read_text(encoding="utf-8")
    source = Path("assets/viewer.js").read_text(encoding="utf-8")

    for element_id in (
        "searchInput",
        "sortSelect",
        "gridView",
        "listView",
        "detailsPanel",
        "downloadAsset",
        "openAsset",
        "copyUrl",
        "copyPath",
    ):
        assert f'id="{element_id}"' in page

    assert "function openDetails(" in source
    assert "function closeDetails(" in source
    assert "function setView(" in source
    assert "function copyValue(" in source
    assert 'window.addEventListener("hashchange"' in source
    assert 'window.addEventListener("keydown"' in source


def test_viewer_uses_approved_brand_assets_and_accessible_landmarks():
    page = Path("assets/viewer.html").read_text(encoding="utf-8")
    stylesheet = Path("assets/viewer.css").read_text(encoding="utf-8")
    sc3d_page = Path("internal/sc3d/static/index.html").read_text(encoding="utf-8")

    assert "fonts/clashking.woff2" in page
    assert "fonts/clashking.ttf" in stylesheet
    assert "logos/clashking-wordmark-dark.svg" in page
    assert "https://assets.clashk.ing/logos/clashking-wordmark-dark.svg" in sc3d_page
    assert "./brand/" not in sc3d_page
    assert 'class="skip-link"' in page
    assert 'aria-label="Asset results"' in page
    assert 'aria-live="polite"' in page
    assert '@media (prefers-reduced-motion: reduce)' in stylesheet
