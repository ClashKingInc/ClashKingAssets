import json
import re
import subprocess
from pathlib import Path


ROOT_CATALOG = Path("locales/en.json")
VIEWER_SOURCES = (
    Path("assets/viewer.html"),
    Path("assets/viewer.js"),
    Path("internal/sc3d/static/index.html"),
    Path("internal/sc3d/static/viewer.js"),
)


def test_catalog_is_complete_and_shipped_with_both_viewers():
    catalog = json.loads(ROOT_CATALOG.read_text(encoding="utf-8"))
    referenced = set()
    pattern = re.compile(r'["\']((?:assets|sc3d)\.[A-Za-z0-9.]+)["\']')
    for source_path in VIEWER_SOURCES:
        referenced.update(pattern.findall(source_path.read_text(encoding="utf-8")))

    assert referenced <= catalog.keys(), sorted(referenced - catalog.keys())
    assert json.loads(Path("assets/locales/en.json").read_text()) == catalog
    assert json.loads(Path("internal/sc3d/static/locales/en.json").read_text()) == catalog


def test_missing_key_fallback_and_parameter_interpolation():
    script = """
      import { createTranslator } from './assets/localization.mjs';
      const t = createTranslator(
        { greeting: 'Hallo {name}' },
        { greeting: 'Hello {name}', fallback: 'Found {count} items' },
      );
      if (t('greeting', {name: 'Ada'}) !== 'Hallo Ada') process.exit(1);
      if (t('fallback', {count: 3}) !== 'Found 3 items') process.exit(2);
      if (t('missing') !== 'missing') process.exit(3);
    """
    subprocess.run(["node", "--input-type=module", "--eval", script], check=True)


def test_locale_selection_precedence_and_normalization_are_defined():
    source = Path("assets/localization.mjs").read_text(encoding="utf-8")
    assert 'params.get("locale")' in source
    assert "LOCALE_PREFERENCE_KEY" in source
    assert "navigator.languages" in source
    assert "Intl.getCanonicalLocales" in source
    assert "localeCandidates" in source
