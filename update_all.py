from update_assets import AssetsUpdater


class UpdateAll:
    def run(self):
        print("[Phase] Sync assets", flush=True)
        AssetsUpdater().run()
        print("[Phase] Done", flush=True)


if __name__ == "__main__":
    UpdateAll().run()
