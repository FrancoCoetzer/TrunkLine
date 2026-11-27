#!/usr/bin/env python3
"""Writes TrunkLine's Windows resources for a build (make windows, make dist).

    make_syso.py VERSION ARCH...      e.g. make_syso.py 0.6.0 amd64 arm64

writes rsrc_windows_<arch>.syso in the repo root for each ARCH: a COFF object
that Go links into Windows builds of the package there. It holds

  - the icon (from packaging/windows/trunkline.ico): RT_ICON images and an
    RT_GROUP_ICON listing them, with ID 3. Wails loads group 3 for the window
    (its winc.AppIconID), and the lowest group ID is also the one Explorer and
    the taskbar show.
  - the version information (Properties, Details): VERSION as the file and
    product version, the name, description and copyright.
  - the application manifest, as Wails' own builds have it: runs as the user
    who started it, draws sharply at any display scaling (per-monitor DPI
    aware), modern common controls, long paths.

The .syso files are build output (git ignores them). Python 3's standard
library is all this needs. The icon itself is drawn by make_icon.py.
"""
import os
import re
import struct
import sys

ROOT = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))
ICO = os.path.join(ROOT, "packaging", "windows", "trunkline.ico")

GROUP_ID = 3
LANG_EN_US = 0x0409
RT_ICON, RT_GROUP_ICON, RT_VERSION, RT_MANIFEST = 3, 14, 16, 24

# COFF machine and the relocation that makes an address image-relative.
MACHINES = {
    "amd64": (0x8664, 0x0003),  # IMAGE_FILE_MACHINE_AMD64, IMAGE_REL_AMD64_ADDR32NB
    "arm64": (0xAA64, 0x0002),  # IMAGE_FILE_MACHINE_ARM64, IMAGE_REL_ARM64_ADDR32NB
}

MANIFEST = """<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0" xmlns:asmv3="urn:schemas-microsoft-com:asm.v3">
  <assemblyIdentity type="win32" name="TrunkLine" version="{version}.0" processorArchitecture="*"/>
  <description>TrunkLine SVN client</description>
  <dependency>
    <dependentAssembly>
      <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
    </dependentAssembly>
  </dependency>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}}"/>
      <supportedOS Id="{{1f676c76-80e1-4239-95bb-83d0f6d0da78}}"/>
      <supportedOS Id="{{4a2f28e3-53b9-4441-ba9c-d69d4a4a6e38}}"/>
      <supportedOS Id="{{35138b9a-5d96-4fbd-8e2d-a2440225f93a}}"/>
    </application>
  </compatibility>
  <asmv3:application>
    <asmv3:windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">permonitorv2,permonitor</dpiAwareness>
      <longPathAware xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">true</longPathAware>
    </asmv3:windowsSettings>
  </asmv3:application>
</assembly>
"""


def read_ico(path):
    """The images in an .ico, as (ICONDIRENTRY fields, data)."""
    with open(path, "rb") as f:
        b = f.read()
    _, kind, n = struct.unpack_from("<HHH", b, 0)
    if kind != 1:
        sys.exit(f"{path} is not an icon")
    images = []
    for i in range(n):
        w, h, colors, _, planes, bits, size, offset = struct.unpack_from("<BBBBHHII", b, 6 + 16 * i)
        images.append(((w, h, colors, planes, bits), b[offset:offset + size]))
    return images


def icon_resources(images):
    """RT_ICON entries 1..n and the RT_GROUP_ICON (a GRPICONDIR) for them."""
    icons = [(i + 1, data) for i, (_, data) in enumerate(images)]
    group = struct.pack("<HHH", 0, 1, len(images))
    for ((w, h, colors, planes, bits), data), (icon_id, _) in zip(images, icons):
        group += struct.pack("<BBBBHHIH", w, h, colors, 0, planes, bits, len(data), icon_id)
    return icons, group


def pad4(b):
    return b + b"\0" * (-len(b) % 4)


def vnode(key, value=b"", text=False, children=()):
    """One node of a VS_VERSIONINFO tree: wLength, wValueLength, wType, the
    key (UTF-16, NUL-ended), padding, the value, padding, then the children,
    each starting on a 4-byte boundary. Text values count WCHARs."""
    k = (key + "\0").encode("utf-16-le")
    if text:
        v = (value + "\0").encode("utf-16-le")
        vlen = len(value) + 1
    else:
        v, vlen = value, len(value)
    out = pad4(struct.pack("<HHH", 0, vlen, 1 if text else 0) + k) + v
    for c in children:
        out = pad4(out) + c
    return struct.pack("<H", len(out)) + out[2:]


def version_resource(version):
    nums = [int(x) for x in version.split(".")] + [0]
    ms, ls = (nums[0] << 16) | nums[1], (nums[2] << 16) | nums[3]
    VS_FF_MASK, VOS_NT_WINDOWS32, VFT_APP = 0x3F, 0x00040004, 1
    fixed = struct.pack("<13I", 0xFEEF04BD, 0x00010000, ms, ls, ms, ls, VS_FF_MASK, 0, VOS_NT_WINDOWS32, VFT_APP, 0, 0, 0)
    strings = [
        ("CompanyName", "Franco Coetzer"),
        ("FileDescription", "TrunkLine SVN client"),
        ("FileVersion", version),
        ("InternalName", "trunkline"),
        ("LegalCopyright", "Franco Coetzer, GNU GPL v3"),
        ("OriginalFilename", "trunkline.exe"),
        ("ProductName", "TrunkLine"),
        ("ProductVersion", version),
        ("Comments", "https://github.com/FrancoCoetzer/TrunkLine"),
    ]
    table = vnode("040904B0", children=[vnode(k, v, text=True) for k, v in strings])  # US English, UTF-16
    sfi = vnode("StringFileInfo", children=[table])
    vfi = vnode("VarFileInfo", children=[vnode("Translation", struct.pack("<HH", LANG_EN_US, 1200))])
    return vnode("VS_VERSION_INFO", fixed, children=[sfi, vfi])


def coff(tree, machine, reloc_type):
    """A COFF object with one .rsrc section holding the resource tree:

        root -> type -> ID -> language -> data entry

    Each data entry points at its data with an image-relative address, which
    the object marks with a relocation against the section so the linker
    fills it in. Types and IDs must be in ascending order."""
    size_dir = lambda n: 16 + 8 * n
    directory = lambda n: struct.pack("<IIHHHH", 0, 0, 0, 0, 0, n)
    off = size_dir(len(tree))
    type_off, lang_off, entry_off, data_off = [], [], [], []
    for _, items in tree:
        type_off.append(off)
        off += size_dir(len(items))
    for _, items in tree:
        lang_off.append([])
        for _ in items:
            lang_off[-1].append(off)
            off += size_dir(1)
    for _, items in tree:
        entry_off.append([])
        for _ in items:
            entry_off[-1].append(off)
            off += 16
    for _, items in tree:
        data_off.append([])
        for _, data in items:
            off = (off + 7) & ~7
            data_off[-1].append(off)
            off += len(data)
    sec = bytearray((off + 7) & ~7)

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

    SCN = 0x00000040 | 0x40000000  # initialized data, readable
    raw_at = 20 + 40
    reloc_at = raw_at + len(sec)
    symtab_at = reloc_at + 10 * len(relocs)
    header = struct.pack("<HHIIIHH", machine, 1, 0, symtab_at, 1, 0, 0)
    section = struct.pack("<8sIIIIIIHHI", b".rsrc", 0, 0, len(sec), raw_at, reloc_at, 0, len(relocs), 0, SCN)
    relocations = b"".join(struct.pack("<IIH", r, 0, reloc_type) for r in relocs)
    symbol = struct.pack("<8sIhHBB", b".rsrc", 0, 1, 0, 3, 0)  # the section, static
    strings = struct.pack("<I", 4)  # an empty string table
    return header + section + bytes(sec) + relocations + symbol + strings


def main():
    if len(sys.argv) < 3 or not re.fullmatch(r"\d+\.\d+\.\d+", sys.argv[1]) or any(a not in MACHINES for a in sys.argv[2:]):
        sys.exit("usage: make_syso.py X.Y.Z amd64|arm64...")
    version = sys.argv[1]
    icons, group = icon_resources(read_ico(ICO))
    tree = [
        (RT_ICON, icons),
        (RT_GROUP_ICON, [(GROUP_ID, group)]),
        (RT_VERSION, [(1, version_resource(version))]),
        (RT_MANIFEST, [(1, MANIFEST.format(version=version).encode("utf-8"))]),
    ]
    for arch in sys.argv[2:]:
        path = os.path.join(ROOT, f"rsrc_windows_{arch}.syso")
        with open(path, "wb") as f:
            f.write(coff(tree, *MACHINES[arch]))
        print(f"wrote {os.path.relpath(path, ROOT)} ({os.path.getsize(path)} bytes, {version})")


if __name__ == "__main__":
    main()
