# Foursmith Player 原生版

Go + MyGo v0.7.4 原生 UI，FFmpeg 解封装、解码及导出，SDL3 音频输出。原生程序不嵌入浏览器，也不启动 Bun 服务。迁移工作区中的 `../web/` 保存原实现，作为界面、交互及算法对照；构建所需资源已收录本仓库，无需该目录。

```sh
brew install go just ffmpeg sdl3
just deps
just dev
just check
just test
just build
```

以上命令在本仓库根目录执行。构建产物位于 `build/darwin-arm64/Foursmith Player.app`。`just build` 收集本机 FFmpeg/SDL 的动态依赖和编码器，加入 LiteRT C 运行库，再重新执行本机临时签名。正式分发仍需开发者证书签名和公证。

可选的 `just assets` 用于从迁移工作区的 `../web/` 重新同步资源，需要 Bun 和该 Web 项目的依赖；日常开发与构建不需要执行。

已有原生应用在运行时，可用 `just build build/candidate` 输出到独立目录，避免改写正在使用的动态库。本次迁移验证产物使用该目录。

macOS 使用 VideoToolbox + CVPixelBuffer + Metal，视频帧不经过 Go 的 RGBA 拷贝；仅检测缩略图和导出画面读回 CPU。Windows/Linux 代码保留 FFmpeg 硬件解码与 MyGo GPU 投影路径，但当前输出需经过 RGBA 上传，尚未实现 D3D11/VAAPI 纹理直接共享。当前本机验证平台为 macOS arm64。

Windows/Linux 需要本平台的 FFmpeg、SDL3 开发 SDK 与 LiteRT C 库，设置 `FSVR_TFLITE_LIBRARY` 后在目标系统执行相同的 `just build`。Windows 打包从编码器、LiteRT 所在目录及可选的 `FSVR_NATIVE_LIB_DIR` 收集 DLL；Linux 需要 `patchelf`，按 `ldd` 收集非 glibc 依赖。非 macOS 的导出字幕使用 FFmpeg/libass，所用 FFmpeg 必须包含 `ass` 滤镜。跨平台代码与打包流程尚未做其他系统的实机验收。

`just doctor` 检查媒体依赖和 GPU。`FSVR_TFLITE_LIBRARY` 可指定兼容的 TensorFlow Lite C 运行库。开发时需要 FFmpeg 与 SDL3 头文件和动态库；macOS 自动读取 Homebrew 路径，其他平台使用 `CGO_CFLAGS` / `CGO_LDFLAGS` 指定工具链。

设置和播放历史保存在系统用户数据目录中的 `Foursmith Player/state.json`。首次使用新名称且尚无配置时会读取旧应用的设置和播放历史，随后保存在新目录。`--data-dir` 可隔离开发配置，不读取旧应用数据；文件或文件夹可直接作为启动参数。远程 FSVR 连接使用 `--server http://host:port`，密码和会话只保存在当前进程内。

迁移范围及验收记录见 [doc/MIGRATION.md](doc/MIGRATION.md)，人像居中算法见 [doc/PORTRAIT_CENTERING.md](doc/PORTRAIT_CENTERING.md)。
