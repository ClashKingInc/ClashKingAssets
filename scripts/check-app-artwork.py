"""Verify the canonical native-header selection without rendering images."""
from pathlib import Path
import hashlib
import json

root = Path(__file__).resolve().parents[1]
manifest = json.loads((root / "artwork/app-headers.json").read_text())
assert manifest["version"] == 1
seen = set()
for entry in manifest["files"]:
    path = (root / "assets" / entry["path"]).resolve()
    assert path.is_relative_to(root / "assets")
    assert entry["id"] not in seen
    seen.add(entry["id"])
    data = path.read_bytes()
    assert len(data) == entry["bytes"], entry["path"]
    assert hashlib.sha256(data).hexdigest() == entry["sha256"], entry["path"]
print(f"Verified {len(seen)} canonical App header illustrations")
