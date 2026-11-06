# Third-party notices

Foursmith Player is licensed under GNU GPL version 3 or later. See `LICENSE`
and `COPYRIGHT.txt`. Third-party software and assets retain their own licenses.

- MyGo (version pinned in `go.mod`): https://github.com/egoist/mygo — MIT.
- purego: https://github.com/ebitengine/purego — Apache-2.0.
- go-text/typesetting: https://github.com/go-text/typesetting — BSD-3-Clause,
  with HarfBuzz-derived code under its accompanying license.
- golang.org/x/net, x/sys, x/text, and x/image: https://go.googlesource.com/ —
  BSD-3-Clause. Exact versions are pinned in `go.mod`.
- FFmpeg: https://ffmpeg.org/ — the current native build enables `--enable-gpl`
  and `--enable-version3` and is GPL-3.0-or-later, including when dynamically
  loaded. See `LICENSE`, `licenses/FFmpeg-LICENSE.md`, and the FFmpeg GPL/LGPL
  texts in `licenses/`.
- SDL3: https://github.com/libsdl-org/SDL
- TensorFlow Lite C v2.17.1 runtime: https://github.com/tphakala/tflite_c/releases/tag/v2.17.1
- BlazeFace models and inference reference: https://github.com/google-ai-edge/mediapipe
- Phosphor icons: https://github.com/phosphor-icons/core — MIT.

License texts are in `licenses/`. The FFmpeg libraries and their native dependencies are copied from the build toolchain. Their version and configured components follow that toolchain, rather than being downloaded by the application at runtime.

The player is based in part on the work of the Independent JPEG Group, through
FFmpeg's JPEG implementation.

Project source: https://github.com/foursmith/player. Each binary release must
provide the exact corresponding player and dependency sources, patches, and
build instructions alongside its download; see `doc/DISTRIBUTION.md` in the
source tree. The project license does not replace this source-distribution
requirement or the notices required by native dependencies (including x264,
x265, LAME, VMAF, and other libraries actually copied into a package).
