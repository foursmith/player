# Native media components

- MyGo v0.7.4: https://github.com/egoist/mygo
- FFmpeg: https://ffmpeg.org/ (runtime libraries follow the build toolchain's configuration).
- SDL3: https://github.com/libsdl-org/SDL
- TensorFlow Lite C v2.17.1 runtime: https://github.com/tphakala/tflite_c/releases/tag/v2.17.1
- BlazeFace models and inference reference: https://github.com/google-ai-edge/mediapipe
- Phosphor icons: https://github.com/phosphor-icons/core

License texts are in `licenses/`. The FFmpeg libraries and their native dependencies are copied from the build toolchain. Their version and configured components follow that toolchain, rather than being downloaded by the application at runtime.
