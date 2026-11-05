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

图标、模型和三语文案由原生项目的 `assets/` 独立维护，应用名称与版本读取 `mygo.json`。

已有原生应用在运行时，可用 `just build build/candidate` 输出到独立目录，避免改写正在使用的动态库。本次迁移验证产物使用该目录。

macOS 使用 VideoToolbox + CVPixelBuffer + Metal，视频帧不经过 Go 的 RGBA 拷贝；仅检测缩略图和导出画面读回 CPU。Windows/Linux 代码保留 FFmpeg 硬件解码与 MyGo GPU 投影路径，但当前输出需经过 RGBA 上传，尚未实现 D3D11/VAAPI 纹理直接共享。当前本机验证平台为 macOS arm64。

Windows/Linux 需要本平台的 FFmpeg、SDL3 开发 SDK 与 LiteRT C 库，设置 `FSVR_TFLITE_LIBRARY` 后在目标系统执行相同的 `just build`。Windows 打包从编码器、LiteRT 所在目录及可选的 `FSVR_NATIVE_LIB_DIR` 收集 DLL；Linux 需要 `patchelf`，按 `ldd` 收集非 glibc 依赖。非 macOS 的导出字幕使用 FFmpeg/libass，所用 FFmpeg 必须包含 `ass` 滤镜。跨平台代码与打包流程尚未做其他系统的实机验收。

`just doctor` 检查媒体依赖和 GPU。`FSVR_TFLITE_LIBRARY` 可指定兼容的 TensorFlow Lite C 运行库。开发时需要 FFmpeg 与 SDL3 头文件和动态库；macOS 自动读取 Homebrew 路径，其他平台使用 `CGO_CFLAGS` / `CGO_LDFLAGS` 指定工具链。

设置、最近文件和播放历史保存在系统用户数据目录中的 `Foursmith Player/state.json`，只读取当前应用的数据目录。`--data-dir` 可隔离开发配置；文件或文件夹可直接作为启动参数。本地播放历史按完整路径、大小和修改时间识别文件。

打开文件、文件夹或拖入视频会替换本地播放列表，并立即播放新选择；播放列表底部的“添加”只追加文件，保留当前播放或暂停状态。原生“文件”菜单提供打开、最近文件、添加、关闭视频和 DLNA 搜索。macOS 使用 ⌘O 打开文件、⇧⌘O 打开文件夹、⇧⌘A 添加文件、⌘, 打开设置；Windows/Linux 使用 Ctrl。续播设置适用于本地文件，关闭视频会保存进度并停止下次启动时自动打开该文件。

DLNA 搜索由原生端直接发送 IPv4 SSDP 请求，通过设备的 ContentDirectory 服务分页浏览目录，视频直接交给 FFmpeg 播放。无需运行 FSVR 服务，也不提供 FSVR 媒体库或密码登录。设备仅在点击搜索时发现，目录按需加载，字幕支持设备声明的 CaptionInfoEx 和目录中的独立字幕文件。真实设备兼容性仍需实机验收。

迁移范围及验收记录见 [doc/MIGRATION.md](doc/MIGRATION.md)，人像居中算法见 [doc/PORTRAIT_CENTERING.md](doc/PORTRAIT_CENTERING.md)。

代码审查、性能对照及验证范围见 [doc/CODE_REVIEW.md](doc/CODE_REVIEW.md)。

项目自有代码当前不提供开源授权。第三方组件保留各自的许可证，见 [resources/THIRD_PARTY_NOTICES.md](resources/THIRD_PARTY_NOTICES.md)。
