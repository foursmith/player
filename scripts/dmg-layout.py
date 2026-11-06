"""Regenerate the blank Finder template; normal builds do not require Python.

Run from the app root: uv run --with ds-store==1.3.1 python scripts/dmg-layout.py
"""
import json
import pathlib
import shutil
import subprocess
import tempfile

from ds_store import DSStore
from mac_alias import Alias


def run(*args):
    subprocess.run(args, check=True)


root = pathlib.Path.cwd()
name = json.loads((root / "mygo.json").read_text())["name"]
target = root / "assets/dmg/layout.dmg"
target.parent.mkdir(parents=True, exist_ok=True)
(root / "build").mkdir(exist_ok=True)
work = pathlib.Path(tempfile.mkdtemp(prefix=".dmg-layout-", dir=root / "build"))
mounted = False
try:
    image, mount = work / "writable.dmg", work / "mount"
    mount.mkdir()
    run("hdiutil", "create", "-size", "32m", "-fs", "HFS+", "-volname", name, str(image))
    run("hdiutil", "attach", "-readwrite", "-noverify", "-noautoopen", "-nobrowse", "-mountpoint", str(mount), str(image))
    mounted = True
    # Keep this file's ID when replacing the artwork during packaging.
    run("sips", "-s", "format", "tiff", "-z", "1", "1", str(root / "assets/icon.png"), "--out", str(mount / ".background.tiff"))
    (mount / "Applications").symlink_to("/Applications")
    with DSStore.open(str(mount / ".DS_Store"), "w+") as store:
        store["."]["bwsp"] = dict(WindowBounds="{{200, 300}, {600, 400}}", ContainerShowSidebar=False,
            PreviewPaneVisibility=False, ShowPathbar=False, ShowSidebar=False, ShowStatusBar=False,
            ShowTabView=False, ShowToolbar=False, SidebarWidth=180)
        store["."]["icvp"] = dict(viewOptionsVersion=1, backgroundType=2,
            backgroundImageAlias=Alias.for_file(str(mount / ".background.tiff")).to_bytes(),
            backgroundColorRed=.012, backgroundColorGreen=.105, backgroundColorBlue=.165,
            gridOffsetX=0., gridOffsetY=0., gridSpacing=64., arrangeBy="none", showIconPreview=False,
            showItemInfo=False, labelOnBottom=True, textSize=13., iconSize=96., scrollPositionX=0., scrollPositionY=0.)
        store["."]["icvl"] = ("type", b"icnv")
        store["."]["vSrn"] = ("long", 1)
        store[f"{name}.app"]["Iloc"] = (172, 220)
        store["Applications"]["Iloc"] = (428, 220)
    run("hdiutil", "detach", str(mount))
    mounted = False
    run("hdiutil", "convert", str(image), "-format", "UDZO", "-ov", "-o", str(target))
finally:
    if mounted:
        run("hdiutil", "detach", "-force", str(mount))
    shutil.rmtree(work)
