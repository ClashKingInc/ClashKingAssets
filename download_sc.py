import aiohttp
import asyncio
import json
import os
import zipfile

class SCDownloader:
    def __init__(self):
        self.FINGERPRINT = "475cb6a2d13043762034ddd6a198bad23e0782eb" # Using the one from update_static.py
        self.CLASH_VERSION = "latest"
        self.VERSION_PARAM = "version"
        self.APK_URL = f"https://d.apkpure.net/b/APK/com.supercell.clashofclans?{self.VERSION_PARAM}={self.CLASH_VERSION}"
        self.TARGET_FOLDER = "uptodatesc"

    async def download(self, url: str, as_json: bool = False):
        async with aiohttp.request('GET', url) as fp:
            if as_json:
                c = await fp.json()
            else:
                c = await fp.read()
        return c

    async def get_fingerprint(self):
        print("Downloading latest APK to find fingerprint...")
        data = await self.download(self.APK_URL)
        
        with open("apk.zip", "wb") as f:
            f.write(data)
        zf = zipfile.ZipFile("apk.zip")
        with zf.open('assets/fingerprint.json') as fp:
            fingerprint = json.loads(fp.read())['sha']

        os.remove("apk.zip")
        print(f"Found fingerprint: {fingerprint}")
        return fingerprint

    async def download_file(self, session, url, filepath):
        if os.path.exists(filepath):
            # print(f"Skipping {filepath}, already exists")
            return
        try:
            async with session.get(url) as response:
                if response.status == 200:
                    with open(filepath, 'wb') as f:
                        while True:
                            chunk = await response.content.read(8192)
                            if not chunk:
                                break
                            f.write(chunk)
                    print(f"Downloaded {filepath.split('/')[-1]}")
                else:
                    print(f"Failed to download {filepath}: HTTP {response.status}")
        except Exception as e:
            print(f"Error downloading {filepath}: {e}")

    async def run(self):
        os.makedirs(self.TARGET_FOLDER, exist_ok=True)
        
        if not self.FINGERPRINT:
            self.FINGERPRINT = await self.get_fingerprint()
        
        BASE_URL = f"https://game-assets.clashofclans.com/{self.FINGERPRINT}"
        print(f"Fetching fingerprint.json from {BASE_URL} ...")
        
        try:
            fingerprint_file = await self.download(url=f"{BASE_URL}/fingerprint.json", as_json=True)
        except Exception as e:
            print(f"Failed to fetch fingerprint.json from server, falling back to getting APK: {e}")
            self.FINGERPRINT = await self.get_fingerprint()
            BASE_URL = f"https://game-assets.clashofclans.com/{self.FINGERPRINT}"
            fingerprint_file = await self.download(url=f"{BASE_URL}/fingerprint.json", as_json=True)
        
        tasks = []
        async with aiohttp.ClientSession() as session:
            for file_data in fingerprint_file.get("files", []):
                file_path: str = file_data["file"]
                if not file_path.startswith("sc/"):
                    continue
                
                filename = file_path.split("/")[-1]
                download_url = f"{BASE_URL}/{file_path}"
                out_path = os.path.join(self.TARGET_FOLDER, filename)
                
                tasks.append(self.download_file(session, download_url, out_path))
            
            print(f"Found {len(tasks)} SC files. Starting download...")
            chunk_size = 20
            for i in range(0, len(tasks), chunk_size):
                 await asyncio.gather(*tasks[i:i+chunk_size])

        print("Finished downloading SC files.")

if __name__ == "__main__":
    asyncio.run(SCDownloader().run())
