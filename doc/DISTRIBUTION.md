# Building and source distribution

Foursmith Player is licensed under **GPL-3.0-or-later**. The complete license
is in [`LICENSE`](../LICENSE); the grant and copyright notice are in
[`resources/COPYRIGHT.txt`](../resources/COPYRIGHT.txt).

The player calls FFmpeg shared libraries directly through purego. Dynamic
loading does not make a GPL-enabled FFmpeg build an LGPL build. The project
uses GPL version 3 or later to match the current native dependency configuration.
Third-party components retain their original licenses.

## Build from source

Use the Go version in `go.mod`, [Just](https://just.systems/), and the native
toolchain for the target OS. Go module versions are pinned in `go.mod` and
`go.sum`. Run commands from this repository's root.

- **macOS:** install the Xcode command-line tools and FFmpeg 9 / SDL3
  (`brew install ffmpeg sdl3`). `just deps` fetches the SHA-256-verified
  TensorFlow Lite C 2.17.1 runtime on Apple Silicon. For Intel macOS, provide
  a matching inference runtime using `FSVR_TFLITE_LIBRARY`.
- **Windows / Linux:** set `FSVR_NATIVE_LIB_DIR` to a local SDK containing
  FFmpeg 9 shared libraries, SDL3, TensorFlow Lite C, and their non-system
  dependencies for the target architecture. The filenames and architecture
  checks are defined in `internal/native/target.go`. Put SDK license notices
  in its `notices/` directory. Linux packaging also requires `patchelf` and
  the tools required by MyGo to create its Linux packages.

`just check` and `just test` run checks and tests. `just dev` runs the native
development app. `just build` creates the application and installers in the
standard `build/` directory. macOS packaging rewrites dynamic library install
names and applies ad-hoc signatures; a recipient can rebuild and sign their
modified application without the publisher's signing identity.

The projection model's training and export scripts are in
`scripts/projection-model/`. The source tree also includes the models, shaders,
icons, localization, resource files, packaging scripts, and `justfile`.

## Native dependency baseline

The development toolchain inspected on 2026-11-07 uses Homebrew FFmpeg
**9.0.2_1** (FFmpeg 9.0.2). Its `ffmpeg -L` output identifies it as GNU GPL
version 3 or later. Its configuration is:

```text
--prefix=/opt/homebrew/Cellar/ffmpeg/9.0.2_1 --enable-shared --enable-pthreads
--enable-version3 --cc=clang --host-cflags= --host-ldflags= --enable-ffplay
--enable-gpl --enable-libsvtav1 --enable-libopus --enable-libx264
--enable-libmp3lame --enable-libdav1d --enable-libvmaf --enable-libvpx
--enable-libx265 --enable-openssl --enable-videotoolbox --enable-audiotoolbox
--enable-neon
```

The upstream source archive for this baseline is
[`ffmpeg-9.0.2.tar.xz`](https://ffmpeg.org/releases/ffmpeg-9.0.2.tar.xz), with
SHA-256 `8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e`.
The installed Homebrew recipe is `ffmpeg/9.0.2_1/.brew/ffmpeg.rb` inside its
Cellar. This snapshot describes the inspected development toolchain; each
release must record its actual libraries and recipes rather than assume this
baseline still applies. Windows and Linux SDKs require their own records.

## Publishing binaries

Use GPL section 6(d): offer each binary and its complete, machine-readable
corresponding source from the same release page, at no additional charge.
Keep the source accessible for as long as the binary is offered. Before
publishing a release:

1. Record the exact player commit, platform, architecture, Go version, module
   versions, and native library versions. Publish a source archive of that
   commit including the resources and build scripts. Include the pinned Go
   dependency sources (for example with `go mod vendor` in the release source
   tree), preserving their license and copyright notices.
2. Include the **exact corresponding source** of the FFmpeg libraries and all
   other required non-system libraries shipped in that binary. This includes
   enabled external components such as x264, x265, LAME, and VMAF, as well as
   SDL3 and the inference runtime. Preserve their notices. A generic upstream
   homepage, the latest branch, or a different version is insufficient.
3. Include each native component's build recipe, compiler/toolchain details,
   FFmpeg configure arguments, and applied patches with modification dates.
   Explicitly record when there are no source patches. For Homebrew libraries,
   retain the installed recipes, receipts, and source archives, including
   sources and patches for their bundled dependencies. For an external SDK,
   obtain the same materials from its provider. Record the packaging changes
   to install names, RPATHs, and signatures made by `cmd/package`.
4. Include the GPL text, project copyright notice, third-party notices, and
   component license texts in the app. `mygo.json` includes the root `LICENSE`;
   `resources/` supplies the remaining notices on all platforms. Check the
   final package, including the notices of transitive native dependencies.
5. Put clearly labeled source downloads next to the binary download, including
   on any website that links to it. Test access without a publisher account
   and verify that the supplied source and instructions can build a modified
   version. Do not publish a binary whose corresponding source is unavailable.

Changing the repository license alone does not supply the corresponding source
of existing app bundles. This document is the release procedure, not a claim
that source archives for existing local builds have already been published.
Never distribute an FFmpeg build configured with `--enable-nonfree`.

References: [FFmpeg licensing](https://ffmpeg.org/legal.html),
[FFmpeg source licensing](https://github.com/FFmpeg/FFmpeg/blob/master/LICENSE.md),
[GNU GPL version 3](https://www.gnu.org/licenses/gpl-3.0.html).
