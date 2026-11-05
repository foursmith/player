"""Generate a scene-disjoint projection experiment. Powered by Poly Haven.

Source HDRIs are CC0: https://polyhaven.com/license
Synthetic stereo is an approximation, not a replacement for real stereo data.
"""

import argparse
import concurrent.futures
import hashlib
import io
import json
import random
import re
import subprocess
from pathlib import Path

import numpy as np
import requests
from PIL import Image, ImageEnhance, ImageOps

LABELS = ["sbs_180_eqr", "sbs_180_fe", "mono_360_eqr", "m_180_eqr", "m_180_fe", "tb_360_eqr", "flat_2d"]
SIZE = 224
AGENT = {"User-Agent": "Foursmith-Projection-Experiment/0.1 (Powered by Poly Haven)"}


def scene_group(name):
    return re.sub(r"_\d+$", "", name)


def fetch(url):
    response = requests.get(url, headers=AGENT, timeout=60)
    response.raise_for_status()
    return response


def prepare(root, scenes, seed):
    root.mkdir(parents=True, exist_ok=True)
    manifest = root / "sources.json"
    if manifest.exists():
        records = json.loads(manifest.read_text())
        if len(records) != scenes:
            raise ValueError("Use a different output directory when changing scene count")
    else:
        fixed_sources = Path(__file__).with_name("sources.json")
        if scenes == 40 and seed == 20261105 and fixed_sources.exists():
            records = json.loads(fixed_sources.read_text())
        else:
            assets = fetch("https://api.polyhaven.com/assets?t=hdris").json()
            names = sorted(assets)
            random.Random(seed).shuffle(names)
            selected = names[:scenes]
            count = max(2, scenes // 6)
            records = [{"id": name, "split": "test" if i < count else "validation" if i < count * 2 else "train", "license": "CC0", "source": f"https://polyhaven.com/a/{name}"} for i, name in enumerate(selected)]
        assigned = {}
        for record in records:
            record["split"] = assigned.setdefault(scene_group(record["id"]), record["split"])
        manifest.write_text(json.dumps(records, indent=2) + "\n")

    def download(record):
        output = root / "panoramas" / (record["id"] + ".jpg")
        if output.exists():
            return
        output.parent.mkdir(exist_ok=True)
        files = fetch("https://api.polyhaven.com/files/" + record["id"]).json()
        resource = files["hdri"]["1k"]["hdr"]
        data = fetch(resource["url"]).content
        if hashlib.md5(data).hexdigest() != resource["md5"]:
            raise ValueError("Source checksum failed: " + record["id"])
        # FFmpeg reads Radiance HDR and converts the float image to RGB for the
        # experiment; no optional OpenCV/OpenEXR Python runtime is needed.
        converted = subprocess.run(["ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "image2pipe", "-i", "pipe:0", "-frames:v", "1", "-vf", "scale=1024:512", "-f", "image2pipe", "-c:v", "png", "pipe:1"], input=data, capture_output=True, check=True)
        Image.open(io.BytesIO(converted.stdout)).convert("RGB").save(output, quality=95)
        print("downloaded", record["id"], flush=True)

    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        list(pool.map(download, records))
    return records


def directions(width, height, projection, yaw, pitch):
    x, y = np.meshgrid((np.arange(width) + .5) / width, (np.arange(height) + .5) / height)
    mask = np.ones((height, width), dtype=bool)
    if projection == "flat":
        tangent = np.tan(np.deg2rad(70) / 2)
        vx, vy, vz = (2 * x - 1) * tangent * width / height, (1 - 2 * y) * tangent, -np.ones_like(x)
    elif projection == "fisheye":
        u, v = (x - .5) * 2, (y - .5) * 2
        radius = np.hypot(u, v)
        theta = radius * np.pi / 2
        sine = np.sin(theta) / np.maximum(radius, 1e-8)
        vx, vy, vz = u * sine, -v * sine, -np.cos(theta)
        mask = radius <= 1
    else:
        longitude = (x - .5) * (2 * np.pi if projection == "360" else np.pi)
        latitude = (.5 - y) * np.pi
        vx, vy, vz = np.sin(longitude) * np.cos(latitude), np.sin(latitude), -np.cos(longitude) * np.cos(latitude)
    # Apply a scene orientation before looking up the source panorama.
    p, a = np.deg2rad(pitch), np.deg2rad(yaw)
    vy, vz = np.cos(p) * vy - np.sin(p) * vz, np.sin(p) * vy + np.cos(p) * vz
    vx, vz = np.cos(a) * vx + np.sin(a) * vz, -np.sin(a) * vx + np.cos(a) * vz
    return vx, vy, vz, mask


def render(panorama, width, height, projection, yaw, pitch):
    vx, vy, vz, mask = directions(width, height, projection, yaw, pitch)
    length = np.sqrt(vx * vx + vy * vy + vz * vz)
    u = (np.arctan2(vx, -vz) / (2 * np.pi) + .5) % 1
    v = .5 - np.arcsin(np.clip(vy / length, -1, 1)) / np.pi
    ph, pw = panorama.shape[:2]
    sx, sy = u * pw - .5, np.clip(v * ph - .5, 0, ph - 1)
    x0, y0 = np.floor(sx).astype(int), np.floor(sy).astype(int)
    fx, fy = (sx - x0)[..., None], (sy - y0)[..., None]
    top = panorama[y0, x0 % pw] * (1 - fx) + panorama[y0, (x0 + 1) % pw] * fx
    bottom = panorama[np.minimum(y0 + 1, ph - 1), x0 % pw] * (1 - fx) + panorama[np.minimum(y0 + 1, ph - 1), (x0 + 1) % pw] * fx
    pixels = np.clip(top * (1 - fy) + bottom * fy, 0, 255).astype(np.uint8)
    pixels[~mask] = 0
    return Image.fromarray(pixels)


def stereo_eye(eye, rng):
    pixels = np.asarray(eye)
    h, w = pixels.shape[:2]
    y, x = np.indices((h, w))
    shift = rng.uniform(-.05, .05) * w * (1 + .5 * np.sin(y / h * 2 * np.pi))
    other = pixels[np.clip(y + rng.randint(-1, 1), 0, h - 1), np.clip((x + shift).astype(int), 0, w - 1)]
    return ImageEnhance.Brightness(Image.fromarray(other)).enhance(rng.uniform(.9, 1.1))


def example(panorama, mode, rng):
    yaw, pitch = rng.uniform(-180, 180), rng.uniform(-12, 12)
    if mode == "flat_2d":
        aspect = rng.choice([.5625, .75, 1, 1.33, 1.78, 2, 2.4])
        width, height = (320, round(320 / aspect)) if aspect >= 1 else (round(320 * aspect), 320)
        img = render(panorama, width, height, "flat", yaw, pitch)
        # Ordinary videos include real letterboxes and sidebars.
        if rng.random() < .35:
            border = rng.randint(4, 24)
            img = ImageOps.expand(img, (0, border, 0, border), fill="black")
    else:
        full = mode in ("mono_360_eqr", "tb_360_eqr")
        fish = mode.endswith("_fe")
        width, height = (320, 160) if full else (160, 160)
        eye = render(panorama, width, height, "fisheye" if fish else "360" if full else "180", yaw, pitch)
        if mode.startswith("sbs") or mode.startswith("tb"):
            other = stereo_eye(eye, rng)
            tb = mode.startswith("tb")
            img = Image.new("RGB", (width if tb else width * 2, height * 2 if tb else height))
            img.paste(eye, (0, 0))
            img.paste(other, (0, height) if tb else (width, 0))
        else:
            img = eye
    img = ImageEnhance.Color(img).enhance(rng.uniform(.6, 1.35))
    img = ImageEnhance.Brightness(img).enhance(rng.uniform(.65, 1.3))
    img = ImageEnhance.Contrast(img).enhance(rng.uniform(.8, 1.2))
    # Training and the Go adapter use this same complete-frame letterbox.
    canvas = ImageOps.pad(img, (SIZE, SIZE), method=Image.Resampling.BILINEAR, color="black", centering=(.5, .5))
    if rng.random() < .6:
        encoded = io.BytesIO()
        canvas.save(encoded, format="JPEG", quality=rng.randint(45, 95))
        canvas = Image.open(io.BytesIO(encoded.getvalue())).convert("RGB")
    return np.asarray(canvas)


def generate(root, records, views, seed):
    config = {"views": views, "seed": seed, "labels": LABELS, "render_version": 1}
    config_file = root / "config.json"
    if config_file.exists() and json.loads(config_file.read_text()) != config:
        raise ValueError("Use a different output directory when changing generation settings")
    config_file.write_text(json.dumps(config, indent=2) + "\n")
    for split in ("train", "validation", "test"):
        destination = root / (split + ".npz")
        if destination.exists():
            continue
        images, labels, groups = [], [], []
        for record in records:
            if record["split"] != split:
                continue
            panorama = np.asarray(Image.open(root / "panoramas" / (record["id"] + ".jpg")).convert("RGB"), dtype=np.float32)
            rng = random.Random(f"{seed}:{record['id']}")
            for label, mode in enumerate(LABELS):
                for _ in range(views):
                    images.append(example(panorama, mode, rng))
                    labels.append(label)
                    groups.append(scene_group(record["id"]))
        np.savez_compressed(destination, images=np.stack(images), labels=np.asarray(labels), groups=np.asarray(groups))
        print(split, len(images), "images from", len(set(groups)), "scenes", flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=Path(".cache/projection-model/data"))
    parser.add_argument("--scenes", type=int, default=40)
    parser.add_argument("--views", type=int, default=12)
    parser.add_argument("--seed", type=int, default=20261105)
    args = parser.parse_args()
    records = prepare(args.out, args.scenes, args.seed)
    generate(args.out, records, args.views, args.seed)
