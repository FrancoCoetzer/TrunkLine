#!/usr/bin/env python3
"""Makes TrunkLine's Windows icon files (make winres).

Writes:
  packaging/windows/trunkline.ico   the icon file, for the installer
  rsrc_windows_amd64.syso           the icon as Windows resources, in a COFF
                                    object Go links into Windows builds of the
                                    package in the repo root

Windows takes a program's icon from resources inside the .exe: RT_ICON images
and an RT_GROUP_ICON that lists them. Wails loads group 3 for the window (its
winc.AppIconID), and the lowest group ID is also the one Explorer and the
taskbar show, so there is one group, with ID 3.

The picture is drawn here from the same geometry as packaging/trunkline.svg (a
64x64 view box), at 1024 px and scaled down, so no SVG renderer is needed.
Change both together. Needs Python 3 and Pillow; the outputs are committed, so
building the app needs neither.
"""
import io
import os
import struct

from PIL import Image, ImageDraw

ROOT = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))
ICO = os.path.join(ROOT, "packaging", "windows", "trunkline.ico")
SYSO = os.path.join(ROOT, "rsrc_windows_amd64.syso")

SIZES = (256, 64, 48, 40, 32, 24, 20, 16)  # the sizes Windows asks for at 100-200% scaling
GROUP_ID = 3
LANG_EN_US = 0x0409
RT_ICON, RT_GROUP_ICON = 3, 14


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


def write_syso(images):
    """A COFF object with one .rsrc section holding the resource tree:

        root -> type (RT_ICON, RT_GROUP_ICON) -> ID -> language -> data entry

    Each data entry points at its data with an image-relative address, which
    the object marks with an IMAGE_REL_AMD64_ADDR32NB relocation against the
    section so the linker fills it in."""
    icons = [(i + 1, data) for i, (_, data) in enumerate(images)]  # RT_ICON IDs 1..n
    group = struct.pack("<HHH", 0, 1, len(images))
    for (size, data), (icon_id, _) in zip(images, icons):
        dim = 0 if size >= 256 else size
        group += struct.pack("<BBBBHHIH", dim, dim, 0, 0, 1, 32, len(data), icon_id)

    # (type, [(id, data)]), types and IDs in ascending order as Windows requires
    tree = [(RT_ICON, icons), (RT_GROUP_ICON, [(GROUP_ID, group)])]

    def directory(n_entries):
        return struct.pack("<IIHHHH", 0, 0, 0, 0, 0, n_entries)

    # Lay out: root dir, type dirs, ID dirs (one language each), data entries, data.
    size_dir = lambda n: 16 + 8 * n
    off = size_dir(len(tree))
    type_off = []
    for _, items in tree:
        type_off.append(off)
        off += size_dir(len(items))
    lang_off = []
    for _, items in tree:
        lang_off.append([])
        for _ in items:
            lang_off[-1].append(off)
            off += size_dir(1)
    entry_off = []
    for _, items in tree:
        entry_off.append([])
        for _ in items:
            entry_off[-1].append(off)
            off += 16
    data_off = []
    for _, items in tree:
        data_off.append([])
        for _, data in items:
            off = (off + 7) & ~7
            data_off[-1].append(off)
            off += len(data)
    total = (off + 7) & ~7

    sec = bytearray(total)
    sub = 0x80000000  # "this entry points at a subdirectory"
    root = directory(len(tree))
    for (rtype, _), o in zip(tree, type_off):
        root += struct.pack("<II", rtype, sub | o)
    sec[0:len(root)] = root
    relocs = []
    for t, (_, items) in enumerate(tree):
        d = directory(len(items))
        for j, (rid, _) in enumerate(items):
            d += struct.pack("<II", rid, sub | lang_off[t][j])
        sec[type_off[t]:type_off[t] + len(d)] = d
        for j, (rid, data) in enumerate(items):
            ld = directory(1) + struct.pack("<II", LANG_EN_US, entry_off[t][j])
            sec[lang_off[t][j]:lang_off[t][j] + len(ld)] = ld
            # IMAGE_RESOURCE_DATA_ENTRY: the data's address (relocated), size, code page, reserved
            sec[entry_off[t][j]:entry_off[t][j] + 16] = struct.pack("<IIII", data_off[t][j], len(data), 0, 0)
            relocs.append(entry_off[t][j])
            sec[data_off[t][j]:data_off[t][j] + len(data)] = data

    IMAGE_FILE_MACHINE_AMD64 = 0x8664
    IMAGE_REL_AMD64_ADDR32NB = 0x0003
    SCN = 0x00000040 | 0x40000000  # initialized data, readable
    raw_at = 20 + 40
    reloc_at = raw_at + len(sec)
    symtab_at = reloc_at + 10 * len(relocs)
    header = struct.pack("<HHIIIHH", IMAGE_FILE_MACHINE_AMD64, 1, 0, symtab_at, 1, 0, 0)
    section = struct.pack("<8sIIIIIIHHI", b".rsrc", 0, 0, len(sec), raw_at, reloc_at, 0, len(relocs), 0, SCN)
    relocations = b"".join(struct.pack("<IIH", r, 0, IMAGE_REL_AMD64_ADDR32NB) for r in relocs)
    symbol = struct.pack("<8sIhHBB", b".rsrc", 0, 1, 0, 3, 0)  # the section, static
    strings = struct.pack("<I", 4)  # an empty string table
    with open(SYSO, "wb") as f:
        f.write(header + section + bytes(sec) + relocations + symbol + strings)


def main():
    big = draw()
    images = [(size, image_data(big, size)) for size in SIZES]
    write_ico(images)
    write_syso(images)
    for p in (ICO, SYSO):
        print(f"wrote {os.path.relpath(p, ROOT)} ({os.path.getsize(p)} bytes)")


if __name__ == "__main__":
    main()
