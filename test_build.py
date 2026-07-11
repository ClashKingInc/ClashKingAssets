import threading
import time
from unittest.mock import patch

import pytest

import build


class FakeR2Client:
    def __init__(self):
        self.lock = threading.Lock()
        self.active_uploads = 0
        self.max_active_uploads = 0
        self.uploaded = []
        self.delete_batches = []

    def upload_file(self, local_path, bucket, key):
        with self.lock:
            self.active_uploads += 1
            self.max_active_uploads = max(self.max_active_uploads, self.active_uploads)
        time.sleep(0.01)
        with self.lock:
            self.uploaded.append((local_path, bucket, key))
            self.active_uploads -= 1

    def delete_objects(self, *, Bucket, Delete):
        self.delete_batches.append((Bucket, Delete["Objects"]))
        return {}


def test_apply_sync_plan_uploads_concurrently_and_batches_deletes():
    client = FakeR2Client()
    plan = {
        "uploads": [
            {"local_path": f"assets/file_{index}.webp", "key": f"file_{index}.webp"} for index in range(8)
        ],
        "deletes": [{"key": f"old_{index}.webp"} for index in range(1001)],
    }
    config = build.R2Config("https://example.invalid", "key", "secret", "assets")

    with patch("build.create_r2_client", return_value=client):
        build.apply_sync_plan(plan, config, workers=4)

    assert client.max_active_uploads > 1
    assert len(client.uploaded) == 8
    assert [len(objects) for _, objects in client.delete_batches] == [1000, 1]


def test_apply_sync_plan_rejects_invalid_worker_count():
    config = build.R2Config("https://example.invalid", "key", "secret", "assets")
    with pytest.raises(build.BuildError, match="workers must be at least 1"):
        build.apply_sync_plan({"uploads": [], "deletes": []}, config, workers=0)
