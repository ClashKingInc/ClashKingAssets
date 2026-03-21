import subprocess
from pathlib import Path
import os

from update_assets import AssetsUpdater
from update_storage import StorageUpdater


class UpdateAll:
    def __init__(self):
        self.render_scale = os.getenv("SC_RENDER_SCALE", "2")
        self.workers = os.getenv("SC_WORKERS", "").strip()
        self.base_dir = Path(__file__).resolve().parent

    def build_go_command(self, root_flag: str, root: str, delete_source: bool = False, delete_sctx: bool = False) -> list[str]:
        command = [
            "go",
            "run",
            "main.go",
            root_flag,
            root,
            "--render-scale",
            self.render_scale,
        ]
        if self.workers:
            command.extend(["--workers", self.workers])
        if delete_source:
            command.append("--delete-source")
        if delete_sctx:
            command.append("--delete-sctx")
        return command

    def process_assets(self):
        image_root = self.base_dir / "image"
        if image_root.exists():
            print("\n[Phase] Process image assets", flush=True)
            subprocess.run(
                self.build_go_command("--process-image-root", "image", delete_source=True),
                check=True,
                cwd=self.base_dir,
            )

        for root in ("sc", "ui/sc"):
            local_root = self.base_dir / root
            if not local_root.exists():
                continue
            print(f"\n[Phase] Process SC root: {root}", flush=True)
            subprocess.run(
                self.build_go_command("--process-sc-root", root, delete_source=True, delete_sctx=True),
                check=True,
                cwd=self.base_dir,
            )

    def run(self):
        print("[Phase] Download assets", flush=True)
        AssetsUpdater().run()
        print("[Phase] Download complete", flush=True)
        self.process_assets()
        print("\n[Phase] Update storage", flush=True)
        StorageUpdater().run()
        print("[Phase] Done", flush=True)


if __name__ == "__main__":
    UpdateAll().run()
