from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path

import pytest

import build

BUILD_SCRIPT = Path(build.__file__).resolve()


def git(cwd: Path, *args: str) -> str:
    completed = subprocess.run(
        ["git", *args],
        cwd=cwd,
        check=True,
        capture_output=True,
        text=True,
    )
    return completed.stdout.strip()


def write_file(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)


@pytest.fixture
def tagged_repo(tmp_path: Path) -> Path:
    git(tmp_path, "init")
    git(tmp_path, "config", "user.name", "Codex")
    git(tmp_path, "config", "user.email", "codex@example.com")
    git(tmp_path, "config", "commit.gpgsign", "false")

    write_file(tmp_path / "assets" / "one.txt", "v1\n")
    git(tmp_path, "add", ".")
    git(tmp_path, "commit", "-m", "initial")
    git(tmp_path, "tag", "v1.0.0")

    write_file(tmp_path / "assets" / "one.txt", "v2\n")
    write_file(tmp_path / "assets" / "two.txt", "new\n")
    git(tmp_path, "add", ".")
    git(tmp_path, "commit", "-m", "second")
    git(tmp_path, "tag", "v2.0.0")
    return tmp_path


def test_parse_name_status_variants() -> None:
    entries = build.parse_name_status(
        "\n".join(
            [
                "A\tassets/new.txt",
                "M\tassets/changed.txt",
                "D\tassets/removed.txt",
                "R100\tassets/old.txt\tassets/newer.txt",
                "R087\tdocs/skip.md\tassets/incoming.txt",
            ]
        )
    )

    assert entries[0] == build.DiffEntry(status="A", path="assets/new.txt")
    assert entries[1] == build.DiffEntry(status="M", path="assets/changed.txt")
    assert entries[2] == build.DiffEntry(status="D", path="assets/removed.txt")
    assert entries[3] == build.DiffEntry(status="R100", old_path="assets/old.txt", new_path="assets/newer.txt")
    assert entries[4] == build.DiffEntry(status="R087", old_path="docs/skip.md", new_path="assets/incoming.txt")


def test_build_sync_plan_filters_and_translates_paths(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.chdir(tmp_path)
    write_file(tmp_path / "assets" / "added.txt", "a")
    write_file(tmp_path / "assets" / "renamed.txt", "b")
    write_file(tmp_path / "assets" / "incoming.txt", "c")

    plan = build.build_sync_plan(
        [
            build.DiffEntry(status="A", path="assets/added.txt"),
            build.DiffEntry(status="M", path="docs/ignored.txt"),
            build.DiffEntry(status="D", path="assets/removed.txt"),
            build.DiffEntry(status="R100", old_path="assets/old.txt", new_path="assets/renamed.txt"),
            build.DiffEntry(status="R090", old_path="assets/outgoing.txt", new_path="docs/outgoing.txt"),
            build.DiffEntry(status="R080", old_path="docs/incoming.txt", new_path="assets/incoming.txt"),
        ],
        "assets",
    )

    assert plan["counts"] == {"added": 1, "modified": 0, "deleted": 1, "renamed": 3}
    assert plan["uploads"] == [
        {"local_path": "assets/added.txt", "key": "added.txt", "reason": "added"},
        {"local_path": "assets/renamed.txt", "key": "renamed.txt", "reason": "renamed"},
        {"local_path": "assets/incoming.txt", "key": "incoming.txt", "reason": "renamed"},
    ]
    assert plan["deletes"] == [
        {"key": "removed.txt", "reason": "deleted"},
        {"key": "old.txt", "reason": "renamed"},
        {"key": "outgoing.txt", "reason": "renamed"},
    ]


def test_infer_refs_with_explicit_override() -> None:
    assert build.infer_current_ref("release-tag") == "release-tag"
    assert build.infer_previous_ref("current", "previous") == "previous"


def test_infer_previous_ref_uses_prior_tag(tagged_repo: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.chdir(tagged_repo)
    assert build.infer_previous_ref("v2.0.0", None) == "v1.0.0"


def test_infer_previous_ref_without_prior_tag_fails(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    git(tmp_path, "init")
    git(tmp_path, "config", "user.name", "Codex")
    git(tmp_path, "config", "user.email", "codex@example.com")
    git(tmp_path, "config", "commit.gpgsign", "false")
    write_file(tmp_path / "assets" / "one.txt", "v1\n")
    git(tmp_path, "add", ".")
    git(tmp_path, "commit", "-m", "initial")
    git(tmp_path, "tag", "v1.0.0")
    monkeypatch.chdir(tmp_path)

    with pytest.raises(build.BuildError, match="could not determine previous tag"):
        build.infer_previous_ref("v1.0.0", None)


def test_collect_all_asset_entries_returns_only_files(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.chdir(tmp_path)
    write_file(tmp_path / "assets" / "nested" / "one.txt", "1\n")
    write_file(tmp_path / "assets" / "two.txt", "2\n")
    (tmp_path / "assets" / "empty-dir").mkdir(parents=True)

    entries = build.collect_all_asset_entries("assets")

    assert entries == [
        build.DiffEntry(status="A", path="assets/nested/one.txt"),
        build.DiffEntry(status="A", path="assets/two.txt"),
    ]


def test_dry_run_cli_outputs_summary_without_r2(tagged_repo: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.chdir(tagged_repo)
    write_file(tagged_repo / "assets" / "three.txt", "third\n")
    git(tagged_repo, "add", ".")
    git(tagged_repo, "mv", "assets/one.txt", "assets/one-renamed.txt")
    git(tagged_repo, "commit", "-m", "third")
    git(tagged_repo, "tag", "v3.0.0")

    completed = subprocess.run(
        [sys.executable, str(BUILD_SCRIPT), "--current-ref", "v3.0.0", "--previous-ref", "v2.0.0", "--dry-run"],
        cwd=tagged_repo,
        check=True,
        capture_output=True,
        text=True,
    )

    summary = json.loads(completed.stdout)
    assert summary["dry_run"] is True
    assert summary["current_ref"] == "v3.0.0"
    assert summary["previous_ref"] == "v2.0.0"
    assert sorted(summary["uploaded_keys"]) == ["one-renamed.txt", "three.txt"]
    assert summary["deleted_keys"] == ["one.txt"]


def test_dry_run_cli_auto_handles_first_release(tmp_path: Path) -> None:
    git(tmp_path, "init")
    git(tmp_path, "config", "user.name", "Codex")
    git(tmp_path, "config", "user.email", "codex@example.com")
    git(tmp_path, "config", "commit.gpgsign", "false")
    write_file(tmp_path / "assets" / "first.txt", "first\n")
    write_file(tmp_path / "assets" / "nested" / "second.txt", "second\n")
    git(tmp_path, "add", ".")
    git(tmp_path, "commit", "-m", "initial")
    git(tmp_path, "tag", "v1.0.0")

    completed = subprocess.run(
        [sys.executable, str(BUILD_SCRIPT), "--current-ref", "v1.0.0", "--dry-run"],
        cwd=tmp_path,
        check=True,
        capture_output=True,
        text=True,
    )

    summary = json.loads(completed.stdout)
    assert summary["dry_run"] is True
    assert summary["first_release"] is True
    assert summary["previous_ref"] is None
    assert summary["deleted_keys"] == []
    assert sorted(summary["uploaded_keys"]) == ["first.txt", "nested/second.txt"]
