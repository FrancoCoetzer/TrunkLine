#!/usr/bin/env python3
"""Draws TrunkLine's Windows pictures (make winres).

Writes:
  packaging/windows/trunkline.ico         the icon: the .exe's (make_syso.py
                                          puts it in the resources), the
                                          installer's and the uninstaller's
  packaging/windows/installer-side.bmp    the installer's welcome and finish
                                          page picture
  packaging/windows/installer-header.bmp  the installer's page header picture

The picture is drawn here from the same geometry as packaging/trunkline.svg (a
64x64 view box), at 1024 px and scaled down, so no SVG renderer is needed.
Change both together. Needs Python 3 and Pillow; the outputs are committed, so
building the app needs neither.
"""
import io
import os
import struct

from PIL import Image, ImageDraw, ImageFont

ROOT = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))
ICO = os.path.join(ROOT, "packaging", "windows", "trunkline.ico")
SIDE = os.path.join(ROOT, "packaging", "windows", "installer-side.bmp")
HEADER = os.path.join(ROOT, "packaging", "windows", "installer-header.bmp")

SIZES = (256, 64, 48, 40, 32, 24, 20, 16)  # the sizes Windows asks for at 100-200% scaling


def draw(px=1024):
    """The icon at px x px, from packaging/trunkline.svg's 64-unit geometry."""
    k = px / 64
    im = Image.new("RGBA", (px, px), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    # <rect width="64" height="64" rx="12" fill="#131922"/>
    d.rounded_rectangle((0, 0, px - 1, px - 1), radius=round(12 * k), fill="#131922")

    # <path d="M20 14v36 M20 22c0 8 24 6 24 16v12" stroke="#38bdf8" stroke-width="5" stroke-linecap="round"/>
    def bezier(p0, p1, p2, p3, n=96):
        pts = []
        for i in range(n + 1):
            t = i / n
            a, b, c, e = (1 - t) ** 3, 3 * (1 - t) ** 2 * t, 3 * (1 - t) * t ** 2, t ** 3
            pts.append((a * p0[0] + b * p1[0] + c * p2[0] + e * p3[0], a * p0[1] + b * p1[1] + c * p2[1] + e * p3[1]))
        return pts

    # The stroke is drawn as a round brush stamped along the path every ~1 px:
    # thick polylines (ImageDraw.line) leave notches where short segments join.
    r = 5 * k / 2
    lines = [
        [(20, 14), (20, 50)],
        bezier((20, 22), (20, 30), (44, 28), (44, 38)) + [(44, 50)],
    ]
    for line in lines:
        pts = [(x * k, y * k) for x, y in line]
        for (x0, y0), (x1, y1) in zip(pts, pts[1:]):
            steps = max(1, int(((x1 - x0) ** 2 + (y1 - y0) ** 2) ** 0.5))
            for i in range(steps + 1):
                x, y = x0 + (x1 - x0) * i / steps, y0 + (y1 - y0) * i / steps
                d.ellipse((x - r, y - r, x + r, y + r), fill="#38bdf8")

    # the three circles
    for cx, cy, colour in ((20, 14, "#f59e0b"), (20, 50, "#f59e0b"), (44, 50, "#10b981")):
        r = 5 * k
        d.ellipse((cx * k - r, cy * k - r, cx * k + r, cy * k + r), fill=colour)
    return im


def image_data(big, size):
    """One icon image: PNG at 256 px; at smaller sizes a 32-bit DIB, which
    every Windows API reads (rows bottom-up, BGRA, then a 1-bit AND mask that
    alpha makes unused)."""
    im = big.resize((size, size), Image.LANCZOS)
    if size >= 256:
        buf = io.BytesIO()
        im.save(buf, "PNG", optimize=True)
        return buf.getvalue()
    header = struct.pack("<IiiHHIIiiII", 40, size, size * 2, 1, 32, 0, 0, 0, 0, 0, 0)
    bgra = im.tobytes("raw", "BGRA")
    stride = size * 4
    pixels = b"".join(bgra[(size - 1 - y) * stride:(size - y) * stride] for y in range(size))
    mask = bytes(((size + 31) // 32) * 4 * size)
    return header + pixels + mask


def write_ico(images):
    """An .ico: ICONDIR, one ICONDIRENTRY per image, then the images."""
    head = struct.pack("<HHH", 0, 1, len(images))
    offset = len(head) + 16 * len(images)
    entries, blobs = b"", b""
    for size, data in images:
        dim = 0 if size >= 256 else size  # 0 means 256
        entries += struct.pack("<BBBBHHII", dim, dim, 0, 0, 1, 32, len(data), offset + len(blobs))
        blobs += data
    with open(ICO, "wb") as f:
        f.write(head + entries + blobs)


def font(size):
    """DejaVu Sans Mono Bold (the app's own look is bold monospace), or Pillow's own font."""
    for path in ("/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf",
                 "/usr/share/fonts/TTF/DejaVuSansMono-Bold.ttf"):
        if os.path.exists(path):
            return ImageFont.truetype(path, size)
    return ImageFont.load_default(size)


def write_side(big):
    """The installer's welcome and finish page picture (NSIS: 164 x 314,
    a BMP): the icon and the name on a dusk-violet fade, as the app looks."""
    w, h = 164, 314
    top, bottom = (0x2b, 0x27, 0x42), (0x16, 0x15, 0x1f)
    im = Image.new("RGB", (w, h))
    d = ImageDraw.Draw(im)
    for y in range(h):
        t = y / (h - 1)
        d.line((0, y, w, y), fill=tuple(round(a + (b - a) * t) for a, b in zip(top, bottom)))
    icon = big.resize((88, 88), Image.LANCZOS)
    im.paste(icon, ((w - 88) // 2, 64), icon)
    f = font(19)
    trunk, line = "Trunk", "Line"
    tw, lw = d.textlength(trunk, font=f), d.textlength(line, font=f)
    x = (w - tw - lw) / 2
    d.text((x, 172), trunk, font=f, fill="#e7e4f3")
    d.text((x + tw, 172), line, font=f, fill="#f6c39f")
    sub = font(11)
    text = "SVN client"
    d.text(((w - d.textlength(text, font=sub)) / 2, 202), text, font=sub, fill="#a5a1bf")
    im.save(SIDE, "BMP")


def write_header(big):
    """The installer's page header picture (NSIS: 150 x 57, a BMP, on the
    header's white): the icon at the right."""
    w, h = 150, 57
    im = Image.new("RGB", (w, h), "white")
    icon = big.resize((44, 44), Image.LANCZOS)
    im.paste(icon, (w - 44 - 6, (h - 44) // 2), icon)
    im.save(HEADER, "BMP")


def main():
    big = draw()
    images = [(size, image_data(big, size)) for size in SIZES]
    write_ico(images)
    write_side(big)
    write_header(big)
    for p in (ICO, SIDE, HEADER):
        print(f"wrote {os.path.relpath(p, ROOT)} ({os.path.getsize(p)} bytes)")


if __name__ == "__main__":
    main()
