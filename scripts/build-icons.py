#!/usr/bin/env python3
"""Derive the 128 px and 256 px launcher icons from the approved 48 px artwork.

Why the 48 px file is the master, measured rather than assumed:

  * `favicon-16x16.png`, `favicon-32x32.png` and `favicon-48x48.png` are one
    composition — the "R" mark on the opaque navy square, corner pixel
    `(1, 23, 72, 255)` — and the 32 -> 48 resample differs from the committed 48
    by a mean of 3.8/255, i.e. they are the same drawing at three sizes.
  * `apple-touch-icon.png` is 180x180 but a *different* composition: corner
    `(255, 255, 255, 255)`, the full emblem (mark plus orbit ring) on white. It
    differs from the 48 px launcher icon by a mean of 125.7/255, so rendering the
    launcher icons from it would change the product's identity rather than enlarge
    it. It is therefore deliberately not the source here.

The consequence, recorded honestly: the largest file carrying the launcher
composition is 48 px, so these two files are upscales (48 -> 128 is 2.67x, 48 ->
256 is 5.33x). They are dimensionally valid, transparent-free in the same way the
approved icon is, and compositionally identical to it, but they are **not** an
approved high-resolution master. A human visual check, or a 128/256 px (or SVG)
master from the artwork owner, is still required before they are treated as final
release artwork. See docs/deployment/P5_LINUX_RELEASE_READINESS.md.

Usage:  python3 scripts/build-icons.py [--check]
        --check  regenerate in memory and compare with what is committed, so CI
                 can prove the committed files are the script's output.
"""
import argparse
import hashlib
import sys
from pathlib import Path

try:
    from PIL import Image
except ImportError:  # pragma: no cover - depends on the host
    sys.exit("Pillow is required: python3 -m pip install Pillow")

REPO = Path(__file__).resolve().parent.parent
MASTER = REPO / "web" / "public" / "favicon-48x48.png"
COMPARISON = REPO / "web" / "public" / "apple-touch-icon.png"
OUTPUTS = {
    "favicon-128x128.png": 128,
    "favicon-256x256.png": 256,
}
MASTER_SIZE = 48


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def render(size: int) -> Image.Image:
    with Image.open(MASTER) as source:
        if source.width != source.height:
            raise SystemExit(f"{MASTER} is {source.width}x{source.height}, not square")
        if source.width < MASTER_SIZE:
            sys.exit(f"master artwork is smaller than expected: {source.width}px")
        # LANCZOS is the documented filter: the highest-quality resampler Pillow
        # offers in both directions, stable across the 9.x series.
        return source.convert("RGBA").resize((size, size), Image.LANCZOS)


def mean_difference(a: Image.Image, b: Image.Image) -> float:
    b = b.convert("RGBA")
    if a.size != b.size:
        b = b.resize(a.size, Image.LANCZOS)
    pa, pb = list(a.convert("RGBA").getdata()), list(b.getdata())
    return sum(abs(x - y) for ta, tb in zip(pa, pb) for x, y in zip(ta, tb)) / (
        a.width * a.height * 4
    )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true", help="fail if the committed icons differ")
    args = parser.parse_args()

    if not MASTER.exists():
        sys.exit(f"missing master artwork: {MASTER}")

    print(f"master:  {MASTER.relative_to(REPO)} ({Image.open(MASTER).size[0]}px)")
    if COMPARISON.exists():
        # Guard the reasoning above: if the 180 px file ever becomes the launcher
        # composition, the note in this script and in the release documents is out
        # of date and the master should be reconsidered.
        master_at_48 = render(48).convert("RGB")
        with Image.open(COMPARISON) as other:
            d = mean_difference(master_at_48, other.convert("RGB").resize((48, 48), Image.LANCZOS))
        print(f"         {COMPARISON.name} at 48px differs by {d:.1f}/255 "
              f"({'same composition' if d <= 6 else 'different composition, not an icon master'})")

    status = 0
    for name, size in OUTPUTS.items():
        target = REPO / "web" / "public" / name
        image = render(size)
        if args.check:
            if not target.exists():
                print(f"MISSING  {name}")
                status = 1
                continue
            with Image.open(target) as existing:
                same = existing.size == image.size and list(existing.convert("RGBA").getdata()) == list(image.convert("RGBA").getdata())
            print(f"{'MATCH   ' if same else 'DIFFERS '} {name} ({size}x{size})")
            if not same:
                status = 1
            continue
        image.save(target, format="PNG", optimize=True)
        print(f"wrote {target.relative_to(REPO)} {size}x{size} "
              f"({size / MASTER_SIZE:.2f}x of the master) sha256 {sha256(target)}")

    if args.check:
        print("committed icons match the script" if status == 0 else "committed icons do NOT match the script")
    return status


if __name__ == "__main__":
    raise SystemExit(main())
