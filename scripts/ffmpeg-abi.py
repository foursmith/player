#!/usr/bin/env python3
"""Regenerate the 64-bit FFmpeg 9 ABI from installed development headers.

This compiles only a temporary layout probe, never the player or an app bundle.
All selected fields have fixed-width or pointer types on amd64 and arm64,
including Windows LLP64. Runtime version checks reject other ABI majors.
"""
import argparse
import os
from pathlib import Path
import shlex
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--check', action='store_true', help='verify the checked-in ABI without rewriting it')
args = parser.parse_args()

STRUCTS = {
    'avFormat': ('AVFormatContext', {'nb_streams': 'uint32', 'streams': '**avStream', 'start_time': 'int64', 'duration': 'int64', 'bit_rate': 'int64', 'interrupt_callback': 'avInterrupt'}),
    'avStream': ('AVStream', {'index': 'int32', 'codecpar': '*avParameters', 'time_base': 'avRational', 'duration': 'int64', 'disposition': 'int32'}),
    'avCodecContext': ('AVCodecContext', {'codec': '*avCodec', 'codec_id': 'int32', 'opaque': 'uintptr', 'get_format': 'uintptr', 'width': 'int32', 'height': 'int32', 'pkt_timebase': 'avRational', 'hw_device_ctx': '*avBuffer', 'sample_rate': 'int32', 'sample_fmt': 'int32', 'ch_layout': 'avChannelLayout'}),
    'avFrame': ('AVFrame', {'data': '[8]*byte', 'linesize': '[8]int32', 'buf': '[8]*avBuffer', 'color_range': 'int32', 'color_primaries': 'int32', 'color_trc': 'int32', 'colorspace': 'int32', 'chroma_location': 'int32', 'width': 'int32', 'height': 'int32', 'nb_samples': 'int32', 'format': 'int32', 'pts': 'int64', 'best_effort_timestamp': 'int64', 'sample_rate': 'int32', 'hw_frames_ctx': '*avBuffer'}),
    'avPacket': ('AVPacket', {'data': '*byte', 'size': 'int32', 'stream_index': 'int32', 'pts': 'int64', 'dts': 'int64', 'duration': 'int64', 'flags': 'int32'}),
    'avParameters': ('AVCodecParameters', {'codec_type': 'int32', 'codec_id': 'int32', 'format': 'int32', 'extradata': '*byte', 'extradata_size': 'int32', 'color_range': 'int32', 'color_primaries': 'int32', 'color_trc': 'int32', 'color_space': 'int32', 'chroma_location': 'int32', 'width': 'int32', 'height': 'int32', 'coded_side_data': '*avSideData', 'nb_coded_side_data': 'int32'}),
    'avCodec': ('AVCodec', {'name': '*byte'}),
    'avHWConfig': ('AVCodecHWConfig', {'pix_fmt': 'int32', 'methods': 'int32', 'device_type': 'int32'}),
    'avPixelDesc': ('AVPixFmtDescriptor', {'flags': 'uint64', 'comp': '[4]avComponent'}),
    'avComponent': ('AVComponentDescriptor', {'depth': 'int32'}),
    'avHWDevice': ('AVHWDeviceContext', {'hwctx': 'unsafe.Pointer'}),
    'avHWFrames': ('AVHWFramesContext', {'device_ctx': '*avHWDevice', 'format': 'int32', 'sw_format': 'int32', 'width': 'int32', 'height': 'int32'}),
    'avBuffer': ('AVBufferRef', {'data': '*byte'}),
    'avFilterInOut': ('AVFilterInOut', {'name': '*byte', 'filter_ctx': 'unsafe.Pointer', 'pad_idx': 'int32', 'next': '*avFilterInOut'}),
    'avSpherical': ('AVSphericalMapping', {'projection': 'int32', 'bound_top': 'uint32', 'bound_bottom': 'uint32', 'bound_left': 'uint32', 'bound_right': 'uint32'}),
    'avStereo': ('AVStereo3D', {'type': 'int32'}),
    'avSideData': ('AVPacketSideData', {'data': '*byte', 'size': 'uintptr', 'type': 'int32'}),
    'avChannelLayout': ('AVChannelLayout', {'nb_channels': 'int32'}),
}
CONSTANTS = {
    'avEOF': 'AVERROR_EOF', 'avNoTimestamp': 'AV_NOPTS_VALUE',
    'avExternal': 'AVERROR_EXTERNAL', 'avNoMemory': 'AVERROR(ENOMEM)', 'avPacketKey': 'AV_PKT_FLAG_KEY', 'avColorFull': 'AVCOL_RANGE_JPEG', 'avExit': 'AVERROR_EXIT', 'avVideo': 'AVMEDIA_TYPE_VIDEO', 'avAudio': 'AVMEDIA_TYPE_AUDIO',
    'avH264': 'AV_CODEC_ID_H264', 'avHEVC': 'AV_CODEC_ID_HEVC',
    'avAttachedPic': 'AV_DISPOSITION_ATTACHED_PIC', 'avHWPixelFlag': 'AV_PIX_FMT_FLAG_HWACCEL',
    'avKeepRef': 'AV_BUFFERSRC_FLAG_KEEP_REF', 'avSphericalData': 'AV_PKT_DATA_SPHERICAL',
    'avStereoData': 'AV_PKT_DATA_STEREO3D', 'avEquirectangular': 'AV_SPHERICAL_EQUIRECTANGULAR',
    'avSphericalTile': 'AV_SPHERICAL_EQUIRECTANGULAR_TILE',
    'avHalfEquirectangular': 'AV_SPHERICAL_HALF_EQUIRECTANGULAR', 'avFisheye': 'AV_SPHERICAL_FISHEYE',
    'avMono': 'AV_STEREO3D_2D', 'avSBS': 'AV_STEREO3D_SIDEBYSIDE', 'avTB': 'AV_STEREO3D_TOPBOTTOM',
}
source = '''#include <stddef.h>
#include <stdio.h>
#include <errno.h>
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <libavutil/hwcontext.h>
#include <libavutil/pixdesc.h>
#include <libavutil/spherical.h>
#include <libavutil/stereo3d.h>
#include <libavfilter/avfilter.h>
#include <libavfilter/buffersrc.h>
int main(void) {
_Static_assert(sizeof(void*) == 8, "64-bit ABI required");
_Static_assert(LIBAVCODEC_VERSION_MAJOR == 63 && LIBAVFORMAT_VERSION_MAJOR == 63 && LIBAVUTIL_VERSION_MAJOR == 61, "FFmpeg 9 headers required");
'''
for go_name, (c_name, fields) in STRUCTS.items():
    source += f'printf("{go_name} size %zu\\n", sizeof({c_name}));\n'
    for field in fields:
        source += f'printf("{go_name} {field} %zu %zu\\n", offsetof({c_name}, {field}), sizeof((({c_name}*)0)->{field}));\n'
for name, expr in CONSTANTS.items():
    source += f'printf("const {name} %lld\\n", (long long)({expr}));\n'
source += '}\n'
flags = shlex.split(subprocess.check_output(['pkg-config', '--cflags', 'libavformat', 'libavcodec', 'libavfilter'], text=True))
with tempfile.TemporaryDirectory(prefix='foursmith-ffmpeg-abi-') as tmp:
    c_path, binary = Path(tmp) / 'abi.c', Path(tmp) / 'abi'
    c_path.write_text(source)
    subprocess.run([*shlex.split(os.environ.get('CC', 'cc')), *flags, str(c_path), '-o', str(binary)], check=True)
    lines = subprocess.check_output([str(binary)], text=True).splitlines()
layouts, constants = {}, {}
for line in lines:
    name, field, *values = line.split()
    if name == 'const':
        constants[field] = int(values[0])
    else:
        layouts.setdefault(name, {})[field] = list(map(int, values))
output = '// Code generated by scripts/ffmpeg-abi.py from FFmpeg 9 headers; DO NOT EDIT.\n\npackage media\n\nimport "unsafe"\n\n'
output += 'type avRational struct { Num, Den int32 }\ntype avInterrupt struct { Callback, Opaque uintptr }\n\n'
for name, (_, fields) in STRUCTS.items():
    output += f'type {name} struct {{\n'
    end = 0
    for field, (offset, size) in sorted(((field, layouts[name][field]) for field in fields), key=lambda entry: entry[1][0]):
        if offset > end:
            output += f'_ [{offset-end}]byte\n'
        go_field = ''.join(part.title() for part in field.split('_'))
        output += f'{go_field} {fields[field]} // offset {offset}\n'
        end = offset + size
    size = layouts[name]['size'][0]
    if size > end:
        output += f'_ [{size-end}]byte\n'
    output += '}\n\n'
output += 'const (\n' + ''.join(f'{name} = {value}\n' for name, value in constants.items()) + ')\n'
root = Path(__file__).resolve().parent.parent
formatted = subprocess.check_output(['gofmt'], input=output, text=True)
path = root / 'internal/media/abi_generated.go'
if args.check:
    if path.read_text() != formatted:
        raise SystemExit('FFmpeg ABI differs from installed headers; regenerate and review bindings')
else:
    path.write_text(formatted)
