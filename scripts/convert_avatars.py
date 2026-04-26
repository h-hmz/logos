#!/usr/bin/env python3
"""
Convert jpg/png images to web-optimised WebP avatars.

Usage:
    python convert_avatars.py                          # converts frontend/public/avatars/
    python convert_avatars.py path/to/dir              # converts a directory
    python convert_avatars.py img1.jpg img2.png ...    # converts individual files
    python convert_avatars.py --dry-run                # preview only, no writes
"""

import argparse
import sys
from pathlib import Path
from PIL import Image

MAX_W = 400
MAX_H = 480
QUALITY = 82


def convert(src: Path, dry_run: bool, max_w: int, max_h: int, quality: int) -> None:
    img = Image.open(src).convert("RGB")
    orig_w, orig_h = img.size

    # Scale down so the image fits within max_w x max_h, never upscale
    scale = min(max_w / orig_w, max_h / orig_h, 1.0)
    new_w = round(orig_w * scale)
    new_h = round(orig_h * scale)
    if scale < 1.0:
        img = img.resize((new_w, new_h), Image.LANCZOS)

    dst = src.with_suffix(".webp")
    orig_kb = src.stat().st_size / 1024

    if dry_run:
        print(f"  [dry-run] {src.name} -> {dst.name}  ({orig_w}x{orig_h} -> {new_w}x{new_h})")
        return

    img.save(dst, "WEBP", quality=quality, method=6)
    new_kb = dst.stat().st_size / 1024
    saving = 100 * (1 - new_kb / orig_kb)
    src.unlink()
    print(f"  {src.name} -> {dst.name}  {orig_w}x{orig_h} -> {new_w}x{new_h}  "
          f"{orig_kb:.0f}KB -> {new_kb:.0f}KB  ({saving:.0f}% smaller)")


def collect_files(paths: list[str]) -> list[Path]:
    files = []
    for p in paths:
        path = Path(p)
        if path.is_dir():
            candidates = sorted(
                f for ext in ("*.jpg", "*.jpeg", "*.png") for f in path.glob(ext)
            )
            for f in candidates:
                if f.with_suffix(".webp").exists():
                    print(f"  skipping {f.name} (webp already exists)")
                else:
                    files.append(f)
        elif path.suffix.lower() in {".jpg", ".jpeg", ".png"}:
            if path.with_suffix(".webp").exists():
                print(f"  skipping {path.name} (webp already exists)")
            else:
                files.append(path)
        else:
            print(f"  skipping {path} (not jpg/png)", file=sys.stderr)
    return files


def main() -> None:
    default_dir = Path(__file__).parent.parent / "frontend" / "public" / "avatars"

    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("paths", nargs="*", default=[str(default_dir)], help="files or directories to convert")
    parser.add_argument("--dry-run", action="store_true", help="preview without writing")
    parser.add_argument("--quality", type=int, default=QUALITY, help=f"WebP quality 1-100 (default {QUALITY})")
    parser.add_argument("--max-w", type=int, default=MAX_W, help=f"max width in px (default {MAX_W})")
    parser.add_argument("--max-h", type=int, default=MAX_H, help=f"max height in px (default {MAX_H})")
    args = parser.parse_args()

    max_w, max_h, quality = args.max_w, args.max_h, args.quality

    files = collect_files(args.paths)
    if not files:
        print("No jpg/png files found.")
        return

    print(f"Converting {len(files)} file(s) -> WebP  (max {max_w}x{max_h}, quality {quality})"
          + ("  [dry-run]" if args.dry_run else ""))

    total_before = total_after = 0
    for f in files:
        try:
            before = f.stat().st_size
            convert(f, args.dry_run, max_w, max_h, quality)
            if not args.dry_run:
                after = f.with_suffix(".webp").stat().st_size
                total_before += before
                total_after += after
        except Exception as e:
            print(f"  ERROR {f.name}: {e}", file=sys.stderr)

    if not args.dry_run and total_before:
        saving = 100 * (1 - total_after / total_before)
        print(f"\nTotal: {total_before/1024:.0f}KB -> {total_after/1024:.0f}KB  ({saving:.0f}% smaller)")


if __name__ == "__main__":
    main()
