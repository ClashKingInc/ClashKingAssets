import os
import aiohttp
import asyncio

# Replace with your Bunny.net credentials
api_key = 'YOUR_API_KEY'
storage_zone_name = 'YOUR_STORAGE_ZONE_NAME'
storage_zone_region = 'YOUR_STORAGE_ZONE_REGION'  # e.g., 'ny', 'sg', etc.
base_url = f'https://{storage_zone_region}.storage.bunnycdn.com/{storage_zone_name}'

# Set up the headers for authentication
headers = {
    'AccessKey': api_key
}


# Function to list all files in the storage zone
async def list_files(session, path=""):
    url = f'{base_url}/{path}'
    async with session.get(url, headers=headers) as response:
        response.raise_for_status()
        return await response.json()


# Function to download .html files
async def download_file(session, file_url, local_filename):
    async with session.get(file_url, headers=headers) as response:
        response.raise_for_status()
        os.makedirs(os.path.dirname(local_filename), exist_ok=True)
        with open(local_filename, 'wb') as f:
            async for chunk in response.content.iter_chunked(8192):
                f.write(chunk)


# List all files and download .html files
async def download_html_files():
    async with aiohttp.ClientSession() as session:
        files = await list_files(session)
        tasks = []
        for file in files:
            if file['IsDirectory']:
                continue  # Skip directories
            if file['ObjectName'].endswith('.html'):
                file_url = f"{base_url}/{file['Path']}"
                local_filename = os.path.join('downloaded_files', file['ObjectName'])
                print(f"Queuing download for {file['ObjectName']}...")
                tasks.append(download_file(session, file_url, local_filename))

        # Run all download tasks concurrently
        await asyncio.gather(*tasks)


# Run the asynchronous download
if __name__ == '__main__':
    asyncio.run(download_html_files())
