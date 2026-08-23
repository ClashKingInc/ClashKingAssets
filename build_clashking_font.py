from __future__ import annotations

import argparse
import copy
import hashlib
import tempfile
import urllib.request
from pathlib import Path

from fontTools.ttLib import TTFont
from fontTools.varLib.instancer import instantiateVariableFont

INTER_URL = "https://rsms.me/inter/font-files/InterVariable.woff2?v=4.1"
INTER_SHA256 = "693b77d4f32ee9b8bfc995589b5fad5e99adf2832738661f5402f9978429a8e3"
CLASHKING_SOURCE_SHA256 = "9395422e95b616103e53c80671899aeef80c2b8e5343e87a1c8aa9fecade303c"


class FontBuildError(RuntimeError):
    pass


def download_inter(destination: Path) -> None:
    with urllib.request.urlopen(INTER_URL) as response:  # noqa: S310 - pinned HTTPS URL and digest
        destination.write_bytes(response.read())

    digest = hashlib.sha256(destination.read_bytes()).hexdigest()
    if digest != INTER_SHA256:
        raise FontBuildError(f"Unexpected Inter source digest: {digest}")


def set_font_names(font: TTFont) -> None:
    names = font["name"]
    values = {
        1: "ClashKing",
        2: "SemiBold",
        3: "4.001;CK;ClashKing-SemiBold",
        4: "ClashKing SemiBold",
        6: "ClashKing-SemiBold",
        16: "ClashKing",
        17: "SemiBold",
    }
    for name_id, value in values.items():
        names.setName(value, name_id, 3, 1, 0x409)
        names.setName(value, name_id, 1, 0, 0)


def build_font(clashking_source: Path, inter_source: Path, ttf_output: Path, woff2_output: Path) -> None:
    clashking_digest = hashlib.sha256(clashking_source.read_bytes()).hexdigest()
    if clashking_digest != CLASHKING_SOURCE_SHA256:
        raise FontBuildError(f"Unexpected ClashKing source digest: {clashking_digest}")

    clashking = TTFont(clashking_source)
    inter_variable = TTFont(inter_source)
    output = instantiateVariableFont(inter_variable, {"opsz": 14, "wght": 600}, inplace=False)

    clashking_cmap = clashking.getBestCmap()
    output_cmap = output.getBestCmap()
    for codepoint, clashking_glyph_name in clashking_cmap.items():
        output_glyph_name = output_cmap.get(codepoint)
        if output_glyph_name is None:
            continue

        glyph = clashking["glyf"][clashking_glyph_name]
        if glyph.isComposite() and any(component.glyphName not in output["glyf"] for component in glyph.components):
            continue

        output["glyf"][output_glyph_name] = copy.deepcopy(glyph)
        output["hmtx"][output_glyph_name] = clashking["hmtx"][clashking_glyph_name]

    set_font_names(output)
    output["OS/2"].usWeightClass = 600
    output["post"].italicAngle = 0
    output.recalcTimestamp = False

    ttf_output.parent.mkdir(parents=True, exist_ok=True)
    output.flavor = None
    output.save(ttf_output)
    output.flavor = "woff2"
    output.save(woff2_output)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Build the international ClashKing UI font.")
    parser.add_argument(
        "--clashking-source",
        type=Path,
        default=Path("internal/fonts/clashking-basic.ttf"),
        help="Original ClashKing Basic Latin source font.",
    )
    parser.add_argument("--ttf-output", type=Path, default=Path("assets/fonts/clashking.ttf"))
    parser.add_argument("--woff2-output", type=Path, default=Path("assets/fonts/clashking.woff2"))
    parser.add_argument("--inter-source", type=Path, help="Use an existing InterVariable.woff2 instead of downloading it.")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if args.inter_source:
        digest = hashlib.sha256(args.inter_source.read_bytes()).hexdigest()
        if digest != INTER_SHA256:
            raise FontBuildError(f"Unexpected Inter source digest: {digest}")
        build_font(args.clashking_source, args.inter_source, args.ttf_output, args.woff2_output)
    else:
        with tempfile.TemporaryDirectory(prefix="clashking-font-") as temporary_directory:
            inter_source = Path(temporary_directory) / "InterVariable.woff2"
            download_inter(inter_source)
            build_font(args.clashking_source, inter_source, args.ttf_output, args.woff2_output)

    print(f"Built {args.ttf_output} and {args.woff2_output}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except FontBuildError as exc:
        raise SystemExit(str(exc)) from exc
