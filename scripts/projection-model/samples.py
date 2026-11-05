"""Generate numbered video fixtures with independent expected-format labels.

Uses cached, held-out Poly Haven panoramas. Does not train or alter the model.
"""

import argparse
import hashlib
import json
import math
import shutil
import struct
import subprocess
from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw, ImageFont, ImageOps

from dataset import LABELS, directions


NAMES = dict(zip(LABELS, ["左右 180° EQR", "左右 180° 鱼眼", "单目 360° EQR", "单目 180° EQR", "单目 180° 鱼眼", "上下 360° EQR", "普通 2D"]))
SIZE = {mode: ((1280, 720) if mode == "flat_2d" else (1280, 1280) if mode == "tb_360_eqr" else (640, 640) if mode.startswith("m_") else (1280, 640)) for mode in LABELS}
VERSION = 2


def run(args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def cases():
    result = []

    def add(mode, description, scene="entrance_hall", group="stress", **extra):
        number = len(result) + 1
        result.append(dict(id=number, file=f"sample-{number:02}.mp4", expected_mode=mode, description=description, scene=scene, group=group, duration=12, fps=12, **extra))

    for scene, description in [("horn-koppe_spring", "室外山地"), ("entrance_hall", "室内大厅")]:
        for mode in LABELS:
            add(mode, description + " / 标准投影 / 无空间标签", scene, "baseline")
    add("sbs_180_fe", "双鱼眼 / 水印、轻微椭圆、部分裁边", radius=.60, scale_y=1.04, watermark=True)
    add("m_180_fe", "单鱼眼 / 偏心、裁边、水印", center=[.43, .56], radius=.47, watermark=True)
    add("sbs_180_eqr", "前 4.2 秒全黑 / 需跳过黑屏", intro=4.2)
    add("flat_2d", "竖屏 2D / 720×1280", size=[720, 1280])
    add("flat_2d", "2D 宽银幕 / 上下黑边", letterbox=True)
    add("flat_2d", "2D 2:1 宽画面 / 与 SBS 相同宽高比", size=[1280, 640])
    add("sbs_180_fe", "40 秒长 GOP / 黑色关键帧 / 需精确定位替换", duration_override=40, size=[640, 320], intro=4.2, long_gop=True)
    add("mono_360_eqr", "可信单目 360 空间标签", group="metadata", copy=3, spatial=["equirectangular", "mono"])
    add("tb_360_eqr", "可信上下 360 空间标签", group="metadata", copy=6, spatial=["equirectangular", "tb"])
    add("sbs_180_eqr", "可信左右 180 半球标签", group="metadata", copy=1, spatial=["half-equirectangular", "sbs"])
    add("sbs_180_eqr", "2:1 + 完整全景/SBS 歧义标签 / 应抽帧确认", group="metadata", copy=1, spatial=["equirectangular", "sbs"])
    add("flat_2d", "不支持的左右 360 / 4:1 / 保留为 2D", group="unsupported", render_mode="sbs_360_eqr", size=[2048, 512], spatial=["equirectangular", "sbs"])
    add("flat_2d", "全程黑屏 / 无有效帧 / 回退 2D", group="unusable", black=True)
    add("sbs_180_eqr", "7680×3840 AVC Main / 歧义标签 / 硬解回退", group="codec", transcode=1, size=[7680, 3840], fps_override=3, spatial=["equirectangular", "sbs"])
    add("mono_360_eqr", "3840×1920 HEVC / 无空间标签", group="codec", transcode=3, size=[3840, 1920], fps_override=6, codec="hevc")
    # The player has one fisheye FOV preset. These lens variants deliberately
    # map to that existing 180-degree mode, rather than introducing new modes.
    variants = [
        ("完整镜头 140° / 窄视场圆形", dict(fov=140)),
        ("完整镜头 160° / 非 180° 圆形", dict(fov=160)),
        ("完整镜头 200° / 超过半球", dict(fov=200)),
        ("完整镜头 220° / 超广视场", dict(fov=220)),
        ("180° / 横向椭圆镜头边界", dict(fov=180, scale_y=1.30)),
        ("180° / 纵向椭圆、上下裁边", dict(fov=180, scale_y=.77)),
        ("190° / 偏心、大幅单侧裁切", dict(fov=190, center=[.38, .57], radius=.56)),
        ("180° / 重度裁切、仅余角部暗边", dict(fov=180, radius=.68)),
        ("200° / 满幅矩形、没有黑色圆边", dict(fov=200, radius=.80)),
        ("190° / 圆角矩形裁幅", dict(fov=190, radius=.75, mask_shape="rounded_rectangle")),
        ("185° / 椭圆、柔和渐暗及灰色外缘", dict(fov=185, radius=.54, scale_y=1.12, rim_softness=.12, outside_luma=.09)),
        ("190° / 不规则暗边、水印", dict(fov=190, radius=.54, mask_shape="irregular", watermark=True)),
    ]
    for description, parameters in variants:
        for mode in ["sbs_180_fe", "m_180_fe"]:
            add(mode, description + " / 按 180° 鱼眼归类", group="nonstandard_fisheye", **parameters)
    for description, parameters in variants:
        add("sbs_180_fe", "室外 / " + description + " / 按 180° 鱼眼归类", scene="horn-koppe_spring", group="nonstandard_fisheye", **parameters)
    for mode in ["sbs_180_eqr", "sbs_180_fe", "tb_360_eqr"]:
        add(mode, "双目 / 右眼偏暗、局部遮挡", group="binocular_stress", other_exposure=.60, other_occlusion=True)
        add(mode, "双目 / 右眼纵向视差 3.5%", group="binocular_stress", other_shift_y=.035, other_exposure=.85)
    for item in result:
        item["duration"] = item.pop("duration_override", item["duration"])
        item["fps"] = item.pop("fps_override", item["fps"])
        item.setdefault("size", list(SIZE[item["expected_mode"]]))
        item.setdefault("codec", "h264")
    return result


class Renderer:
    def __init__(self, panorama, item):
        self.panorama = np.asarray(panorama, dtype=np.float32)
        self.item = item
        mode = item.get("render_mode", item["expected_mode"])
        width, height = item["size"]
        self.stereo = "tb" if mode.startswith("tb") else "sbs" if mode.startswith("sbs") else ""
        self.width = width // 2 if self.stereo == "sbs" else width
        self.height = height // 2 if self.stereo == "tb" else height
        if item.get("letterbox"):
            self.height = int(self.width / 2.4) // 2 * 2
        projection = "flat" if mode == "flat_2d" else "fisheye" if mode.endswith("_fe") else "360" if "360" in mode else "180"
        if projection == "fisheye":
            x, y = np.meshgrid((np.arange(self.width, dtype=np.float32) + .5) / self.width, (np.arange(self.height, dtype=np.float32) + .5) / self.height)
            cx, cy = item.get("center", [.5, .5])
            radius = item.get("radius", .5)
            u, v = (x - cx) / radius, (y - cy) * item.get("scale_y", 1) / radius
            r = np.hypot(u, v)
            theta = r * np.deg2rad(item.get("fov", 180) / 2)
            sine = np.sin(theta) / np.maximum(r, 1e-8)
            vx, vy, vz, mask = u * sine, -v * sine, -np.cos(theta), r <= 1
            if item.get("mask_shape") == "rounded_rectangle":
                rounded = .13
                qx, qy = np.maximum(np.abs(x - .5) - (.49 - rounded), 0), np.maximum(np.abs(y - .5) - (.49 - rounded), 0)
                mask = np.hypot(qx, qy) <= rounded
            elif item.get("mask_shape") == "irregular":
                angle = np.arctan2(v, u)
                mask = r <= np.minimum(1, .93 + .045 * np.sin(angle * 3) + .025 * np.cos(angle * 5))
            self.alpha = np.clip((1 - r) / item["rim_softness"], 0, 1) if item.get("rim_softness") else None
            if self.alpha is not None:
                self.alpha[~mask] = 0
        else:
            vx, vy, vz, mask = directions(self.width, self.height, projection, 0, 0)
            self.alpha = None
        length = np.sqrt(vx * vx + vy * vy + vz * vz)
        u = (np.arctan2(vx, -vz) / (2 * np.pi) + .5) % 1
        v = .5 - np.arcsin(np.clip(vy / length, -1, 1)) / np.pi
        ph, pw = self.panorama.shape[:2]
        self.sx = (u * pw - .5).astype(np.float32)
        sy = np.clip(v * ph - .5, 0, ph - 1).astype(np.float32)
        self.y0 = np.floor(sy).astype(np.int32)
        self.y1 = np.minimum(self.y0 + 1, ph - 1)
        self.fy = (sy - self.y0).astype(np.float32)[..., None]
        self.mask = mask

    def eye(self, seconds, other=False):
        # A common slow pan, with a small eye-angle difference. This is an
        # approximate stereo fixture, not physically reconstructed depth.
        angle = 22 + seconds + (.8 if other else 0)
        sx = self.sx + angle / 360 * self.panorama.shape[1]
        x0 = np.floor(sx).astype(np.int32)
        fx = (sx - x0).astype(np.float32)[..., None]
        x1 = (x0 + 1) % self.panorama.shape[1]
        x0 %= self.panorama.shape[1]
        top = self.panorama[self.y0, x0] * (1 - fx) + self.panorama[self.y0, x1] * fx
        bottom = self.panorama[self.y1, x0] * (1 - fx) + self.panorama[self.y1, x1] * fx
        pixels = np.clip(top * (1 - self.fy) + bottom * self.fy, 0, 255).astype(np.uint8)
        outside = round(self.item.get("outside_luma", 0) * 255)
        if self.alpha is not None:
            pixels = np.clip(pixels * self.alpha[..., None] + outside * (1 - self.alpha[..., None]), 0, 255).astype(np.uint8)
        else:
            pixels[~self.mask] = outside
        if other:
            if self.item.get("other_shift_y"):
                rows = np.clip(np.arange(self.height) + round(self.height * self.item["other_shift_y"]), 0, self.height - 1)
                pixels = pixels[rows]
            if self.item.get("other_exposure"):
                pixels = np.clip(pixels.astype(np.float32) * self.item["other_exposure"], 0, 255).astype(np.uint8)
            if self.item.get("other_occlusion"):
                pixels = pixels.copy()
                pixels[self.height // 3:self.height * 2 // 3, self.width // 3:self.width // 2] = 0
        if self.item.get("watermark"):
            image = Image.fromarray(pixels)
            draw = ImageDraw.Draw(image)
            font = ImageFont.load_default(size=max(12, round(self.width / 26)))
            draw.text((4, 4), "TEST", font=font, fill="white")
            draw.text((self.width - 4, self.height - 4), "TEST", font=font, fill="white", anchor="rs")
            pixels = np.asarray(image)
        return pixels

    def frame(self, seconds):
        width, height = self.item["size"]
        if self.item.get("black") or seconds < self.item.get("intro", 0):
            return np.zeros((height, width, 3), dtype=np.uint8)
        left = self.eye(seconds)
        if self.stereo:
            return np.concatenate([left, self.eye(seconds, True)], axis=0 if self.stereo == "tb" else 1)
        if self.item.get("letterbox"):
            output = np.zeros((height, width, 3), dtype=np.uint8)
            top = (height - self.height) // 2
            output[top:top + self.height] = left
            return output
        return left


def encode(renderer, item, output, preview):
    width, height = item["size"]
    rate = 6
    gop = 1000 if item.get("long_gop") else item["fps"]
    args = ["ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "rawvideo", "-pix_fmt", "rgb24", "-s", f"{width}x{height}", "-r", str(rate), "-i", "pipe:0", "-an", "-vf", f"fps={item['fps']}", "-c:v", "libx264", "-threads", "2", "-preset", "veryfast", "-crf", "24", "-profile:v", "main", "-pix_fmt", "yuv420p", "-g", str(gop), "-keyint_min", str(gop), "-sc_threshold", "0", "-bf", "0", "-map_metadata", "-1", "-y", str(output)]
    count = round(item["duration"] * rate)
    preview_at = max(round(count / 2), round((item.get("intro", 0) + 1) * rate))
    with subprocess.Popen(args, stdin=subprocess.PIPE) as process:
        try:
            for frame in range(count):
                pixels = renderer.frame(frame / rate)
                if frame == preview_at:
                    Image.fromarray(pixels).save(preview, quality=88)
                process.stdin.write(pixels.tobytes())
        finally:
            process.stdin.close()
        if process.wait() != 0:
            raise RuntimeError(f"Video encoding failed: {output}")


def atom(kind, payload):
    return struct.pack(">I4s", 8 + len(payload), kind.encode()) + payload


def spatial_tags(file, spherical, stereo):
    # FFmpeg's default MP4 puts moov last. Growing it leaves mdat/chunk offsets
    # untouched. Refuse arbitrary fast-start inputs rather than corrupt them.
    raw = file.read_bytes()
    at, moov, mdat = 0, -1, -1
    while at + 8 <= len(raw):
        size, kind = struct.unpack_from(">I4s", raw, at)
        if size < 8:
            raise ValueError("Unsupported MP4 box size")
        if kind == b"moov":
            moov = at
        if kind == b"mdat":
            mdat = at
        at += size
    if moov < mdat or moov < 0 or at != len(raw):
        raise ValueError("Spatial fixture requires a final moov box")
    bounds = bytearray(20)
    if spherical == "half-equirectangular":
        struct.pack_into(">II", bounds, 12, 0x40000000, 0x40000000)
    projection = atom("proj", atom("prhd", bytes(16)) + atom("equi", bounds))
    mapping = atom("sv3d", atom("svhd", bytes(4) + b"Foursmith sample\0") + projection)
    extra = atom("st3d", bytes([0, 0, 0, 0, {"mono": 0, "tb": 1, "sbs": 2}[stereo]])) + mapping
    inserted = 0

    def rewrite(data):
        nonlocal inserted
        result = bytearray()
        while len(data) >= 8:
            size, kind = struct.unpack_from(">I4s", data)
            if size < 8 or size > len(data):
                raise ValueError("Invalid MP4 box")
            payload = data[8:size]
            if kind in [b"moov", b"trak", b"mdia", b"minf", b"stbl"]:
                payload = rewrite(payload)
            elif kind == b"stsd":
                payload = payload[:8] + rewrite(payload[8:])
            elif kind in [b"avc1", b"hvc1", b"hev1"]:
                payload += extra
                inserted += 1
            result += atom(kind.decode(), payload)
            data = data[size:]
        return bytes(result) + data

    replacement = rewrite(raw[moov:])
    if inserted != 1:
        raise ValueError("Expected exactly one video sample entry")
    file.write_bytes(raw[:moov] + replacement)


def transcode(item, source, output):
    width, height = item["size"]
    args = ["ffmpeg", "-hide_banner", "-loglevel", "error", "-i", str(source), "-an", "-vf", f"fps={item['fps']},scale={width}:{height}"]
    if item["codec"] == "hevc":
        args += ["-c:v", "libx265", "-preset", "ultrafast", "-crf", "30", "-x265-params", "log-level=error:pools=2:frame-threads=1:bframes=2", "-tag:v", "hvc1"]
    else:
        args += ["-c:v", "libx264", "-threads", "2", "-preset", "ultrafast", "-crf", "30", "-profile:v", "main", "-coder", "1", "-bf", "0"]
    args += ["-pix_fmt", "yuv420p", "-g", str(item["fps"] * 2), "-map_metadata", "-1", "-y", str(output)]
    run(args)


def verify_media(item, path):
    raw = subprocess.check_output(["ffprobe", "-v", "error", "-count_frames", "-select_streams", "v:0", "-show_entries", "stream=codec_name,width,height,nb_read_frames,duration:stream_side_data", "-of", "json", str(path)])
    stream = json.loads(raw)["streams"][0]
    if [stream["width"], stream["height"]] != item["size"] or stream["codec_name"] != item["codec"] or int(stream["nb_read_frames"]) != item["duration"] * item["fps"]:
        raise ValueError(f"Unexpected media properties: {item['file']} {stream}")
    spherical = next((s for s in stream.get("side_data_list", []) if s["side_data_type"] == "Spherical Mapping"), None)
    stereo = next((s for s in stream.get("side_data_list", []) if s["side_data_type"] == "Stereo 3D"), None)
    if item.get("spatial"):
        if not spherical or not stereo:
            raise ValueError("Missing spatial fixture tags: " + item["file"])
        expected = {"mono": "2D", "tb": "top and bottom", "sbs": "side by side"}[item["spatial"][1]]
        if stereo["type"] != expected:
            raise ValueError("Wrong stereo packing: " + item["file"])
    elif spherical or stereo:
        raise ValueError("Blind fixture unexpectedly contains spatial tags: " + item["file"])
    return stream


def font(size):
    for name in ["/System/Library/Fonts/PingFang.ttc", "/System/Library/Fonts/Supplemental/Arial Unicode.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"]:
        if Path(name).exists():
            return ImageFont.truetype(name, size)
    return ImageFont.load_default(size=size)


def matches(item, result):
    return not result.get("error") and result.get("resolved_mode") == item["expected_mode"] and result["samples"] == (0 if item.get("black") else 3)


def validation_role(item):
    mode = item["expected_mode"]
    return "primary_binocular" if mode.startswith(("sbs", "tb")) else "flat_control" if mode == "flat_2d" else "auxiliary_mono"


def write_index(root, manifest):
    role_order = {"primary_binocular": 0, "flat_control": 1, "auxiliary_mono": 2}
    items = sorted(manifest["clips"], key=lambda c: (role_order[validation_role(c)], c["id"]))
    sheet = Image.new("RGB", (1440, math.ceil(len(items) / 4) * 256), "#111318")
    draw = ImageDraw.Draw(sheet)
    lines = ["# 视频验证样本", "", f"共 {len(items)} 段，静音，文件以中性编号命名。直接在播放器打开本目录即可加入播放列表。", "", "以双目为主验证，2D 为对照，单目为辅助；下表和总览先排双目，再排 2D，最后排单目，三个组分别统计。预期类型来自生成时的投影，不以当前模型预测作为真值。先看播放器自动结果，再与下表对照；已手动选过类型时，可点“视频类型 → 重新识别”。", "", "标准样本来自 Poly Haven CC0 缓存中的两个测试场景（未用于模型训练，但仍与合成测试集共享场景），用慢速转动产生运动。左右眼采用小角度差异，属于近似立体，不能验证真实双目深度。偏心/裁边样本用于检测容错，不代表播放器会自动校准镜头内参。12 秒片段居多，长 GOP 片段为 40 秒；8K AVC 是放大素材，用于验证解码器而非画质。", "", "30–65 是非标准鱼眼：完整镜头视场为 140°、160°、180°、185°、190°、200° 或 220°，含椭圆、偏心、重度裁切、满幅矩形、圆角矩形、柔和暗边和不规则边界。按照播放器当前分类统一预期为对应的左右/单目 180° 鱼眼；这代表类型兼容，不能据此认定实际视场为 180° 或边缘几何已精确校准。视场参数描述完整镜头圆，裁幅后的有效视场会变化。66–71 对双目增加眼间曝光差、局部遮挡和纵向视差。", ""]
    if any(item.get("results") for item in items):
        for role, name in [("primary_binocular", "双目主验证"), ("flat_control", "2D 对照"), ("auxiliary_mono", "单目辅助")]:
            selected = [item for item in items if validation_role(item) == role]
            correct = sum(all(matches(item, r) for r in item["results"]) for item in selected)
            lines += [f"{name}：{correct}/{len(selected)} 段在全部随机检测轮次中匹配预期。", ""]
        failures = [item for item in items if not all(matches(item, r) for r in item.get("results", []))]
        for item in failures:
            observed = "/".join(dict.fromkeys(NAMES.get(r.get("resolved_mode"), "失败") for r in item["results"]))
            lines += [f"待复现：[{item['file']}]({item['file']}) 预期 **{NAMES[item['expected_mode']]}**，实测为 **{observed}**。样本保持原样，可用于后续修复回归。", ""]
    lines += ["![样本总览](contact-sheet.jpg)", "", "| 文件 | 预期类型 | 分辨率 / 编码 | 场景 | 自动识别（多轮） |", "| --- | --- | --- | --- | --- |"]
    for index, item in enumerate(items):
        x, y = index % 4 * 360, index // 4 * 256
        thumb = ImageOps.contain(Image.open(root / "previews" / item["file"].replace(".mp4", ".jpg")), (344, 180))
        sheet.paste(thumb, (x + (360 - thumb.width) // 2, y + (180 - thumb.height) // 2))
        draw.text((x + 10, y + 186), f"{item['id']:02}  {NAMES[item['expected_mode']]}", font=font(19), fill="white")
        draw.text((x + 10, y + 216), item["description"][:25], font=font(13), fill="#abb4c6")
        results = item.get("results", [])
        if results:
            correct = sum(matches(item, r) for r in results)
            names = "/".join(dict.fromkeys(NAMES.get(r.get("resolved_mode"), r.get("resolved_mode", "失败")) for r in results))
            status = f"{correct}/{len(results)} · {names}"
        else:
            status = "尚未运行"
        width, height = item["size"]
        lines.append(f"| [{item['file']}]({item['file']}) | {NAMES[item['expected_mode']]} | {width}×{height} / {item['codec']} | {item['description']} | {status} |")
    sheet.save(root / "contact-sheet.jpg", quality=90)
    lines += ["", "检测使用随机抽帧，多轮结果可能不同。`manifest.json` 含各帧投票、模型分数、最终类型和耗时；`validation-round-*.jsonl` 保存原始诊断输出。元数据组会按播放器规则优先使用可信标签，不能与无标签组混算成模型准确率；全黑样本应无有效帧并回退 2D。", "", "素材来源（Powered by Poly Haven）："]
    lines += [f"- [{r['id']}]({r['source']}) · {r['license']}" for r in manifest["sources"]]
    (root / "README.md").write_text("\n".join(lines) + "\n")


def classify(root, manifest, rounds):
    for number in range(1, rounds + 1):
        output = root / f"validation-round-{number}.jsonl"
        with output.open("w") as stream:
            run(["./scripts/native-env", "go", "run", "./cmd/classify", "--timeout", "12s", *[str(root / c["file"]) for c in manifest["clips"]]], stdout=stream)
        results = [json.loads(line) for line in output.read_text().splitlines()]
        by_file = {r["file"]: r for r in results}
        if len(by_file) != len(manifest["clips"]):
            raise ValueError("Incomplete classifier output")
        for item in manifest["clips"]:
            item.setdefault("results", []).append(by_file[item["file"]])
        correct = sum(matches(item, r) for r, item in zip(results, manifest["clips"]))
        print(f"validation {number}: {correct}/{len(results)} expected formats (mismatches retained)", flush=True)
    manifest["model_sha256"] = hashlib.sha256(Path("assets/models/projection.tflite").read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=Path("build/video-samples"))
    parser.add_argument("--data", type=Path, default=Path(".cache/projection-model/data"))
    parser.add_argument("--rounds", type=int, default=3)
    parser.add_argument("--verify-only", action="store_true", help="validate existing generated media without rendering again")
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    (args.out / "previews").mkdir(exist_ok=True)
    items = cases()
    records = json.loads((args.data / "sources.json").read_text())
    sources = [r for r in records if r["id"] in {c["scene"] for c in items}]
    if any(r["split"] != "test" for r in sources) or len(sources) != 2:
        raise ValueError("Sample sources must be the two held-out test panoramas")
    config = dict(version=VERSION, clips=items, sources=sources)
    config_path = args.out / "generation.json"
    if config_path.exists():
        previous = json.loads(config_path.read_text())
        # Append new numbered cases without regenerating existing clips or
        # changing the files the user may already have loaded in the player.
        if previous.get("version") not in [1, VERSION] or previous["sources"] != sources or previous["clips"] != items[:len(previous["clips"])]:
            raise ValueError("Use a new output directory for changed fixture settings")
    config_path.write_text(json.dumps(config, ensure_ascii=False, indent=2) + "\n")
    panoramas = {r["id"]: Image.open(args.data / "panoramas" / (r["id"] + ".jpg")).convert("RGB") for r in sources}
    for item in items:
        output = args.out / item["file"]
        preview = args.out / "previews" / item["file"].replace(".mp4", ".jpg")
        print(f"{item['id']:02}/{len(items)} {item['description']}", flush=True)
        if not args.verify_only and not output.exists():
            temporary = output.with_suffix(".partial.mp4")
            if "copy" in item or "transcode" in item:
                original = items[item.get("copy", item.get("transcode")) - 1]
                source = args.out / original["file"]
                if "copy" in item:
                    shutil.copyfile(source, temporary)
                else:
                    transcode(item, source, temporary)
                shutil.copyfile(args.out / "previews" / original["file"].replace(".mp4", ".jpg"), preview)
            else:
                encode(Renderer(panoramas[item["scene"]], item), item, temporary, preview)
            if item.get("spatial"):
                spatial_tags(temporary, *item["spatial"])
            temporary.replace(output)
        item["probe"] = verify_media(item, output)
        item["sha256"] = hashlib.sha256(output.read_bytes()).hexdigest()
        item["validation_role"] = validation_role(item)
    manifest = dict(version=VERSION, sources=sources, clips=items)
    write_index(args.out, manifest)
    (args.out / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    if args.rounds:
        classify(args.out, manifest, args.rounds)
        (args.out / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
        write_index(args.out, manifest)
    print(f"Ready: {args.out.resolve()}", flush=True)


if __name__ == "__main__":
    main()
