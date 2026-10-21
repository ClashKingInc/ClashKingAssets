#!/usr/bin/env python3
"""Serve a public asset checkout for local and temporary development previews."""
import argparse
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit


class AssetsHandler(SimpleHTTPRequestHandler):
    def end_headers(self):
        self.send_header('Access-Control-Allow-Origin', '*')
        self.send_header('Access-Control-Expose-Headers', 'ETag, Last-Modified, Content-Length')
        self.send_header('Cache-Control', 'no-cache')
        self.send_header('X-Content-Type-Options', 'nosniff')
        super().end_headers()

    def do_OPTIONS(self):
        self.send_response(204)
        self.send_header('Access-Control-Allow-Methods', 'GET, HEAD, OPTIONS')
        self.send_header('Access-Control-Allow-Headers', 'If-None-Match, If-Modified-Since, Range, If-Range')
        self.end_headers()

    def translate_path(self, path):
        # The tunnel keeps the prefix. Strip it only at its exact path boundary,
        # and never follow asset-directory symlinks outside the public checkout.
        pathname = urlsplit(path).path
        prefix = self.server.asset_prefix
        if prefix and not pathname.startswith(prefix + '/'):
            return str(self.server.asset_root / '__not_found__')
        relative = pathname[len(prefix):] if prefix else pathname
        candidate = Path(super().translate_path(relative)).resolve()
        if not candidate.is_relative_to(self.server.asset_root):
            return str(self.server.asset_root / '__not_found__')
        return str(candidate)

    def list_directory(self, path):
        self.send_error(404, 'Asset not found')
        return None


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[1] / 'assets')
    parser.add_argument('--host', default='127.0.0.1')
    parser.add_argument('--port', type=int, default=8001)
    parser.add_argument('--prefix', default='')
    args = parser.parse_args()
    root = args.root.resolve(strict=True)
    prefix = '/' + args.prefix.strip('/') if args.prefix.strip('/') else ''
    server = ThreadingHTTPServer((args.host, args.port), partial(AssetsHandler, directory=str(root)))
    server.asset_root = root
    server.asset_prefix = prefix
    print(f'Serving public assets at http://{args.host}:{args.port}{prefix}/', flush=True)
    server.serve_forever()
