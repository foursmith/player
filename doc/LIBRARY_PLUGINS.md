# 媒体库插件与播放接口

媒体库继续使用现有文件夹和列表展示。本地文件、DLNA、可安装的 JS/TS 插件共用公开的 `library.Plugin` 接口、队列和历史。注册入口在 `internal/providers.NewRegistry`；内置的 Jellyfin/Emby 连接器同样是脚本插件。

## 使用

在视频库的“远程媒体库”分组点击添加按钮，选择 Jellyfin / Emby，填写服务器地址及用户名/密码，或现有用户访问令牌，点击“连接并保存”。一个插件可以建立多个服务器或账号连接。服务器地址可包含部署前缀，例如 `https://video.example/emby`。

连接的设置按钮可以重新登录。敏感字段留空时沿用当前登录；更改服务器地址或账号时需重新提供凭据，旧令牌不会传给新服务器。更换服务器或账号时会分配新的连接身份，已有队列和历史保留原连接，避免相同媒体 ID 串用进度。密码与访问令牌通过 `mygo.Secrets` 存入系统凭据存储，不写入应用状态或插件配置。不支持系统凭据存储的环境会显示错误，不降级为明文保存。

添加菜单中的“安装插件…”导入本地插件目录，校验后复制到用户数据目录的 `plugins/<id>/`，安装时不执行脚本。也可在应用未运行时手动放入该目录。相同 ID 的插件不能覆盖当前插件；本阶段不包含 GitHub/ZIP 安装、自动更新或卸载界面。

## 数据边界

| 模型 | 内容 | 持久化 |
| --- | --- | --- |
| `Source` | 连接 ID、名称、插件 ID、非敏感 JSON 配置、凭据引用 | 是 |
| `Reference` | 来源 ID、媒体 ID、父目录提示 | 是 |
| `Entry` / `Item` | 当前条目、文件夹、元数据和错误 | 不保存整个对象 |
| `Playback` | URL、HTTP 请求头、字幕、过期时间、直播标记、插件会话数据 | 否 |
| `PlaybackEvent` | 开始、进度和停止，以及位置、暂停、结束和错误状态 | 否 |

连接 ID 与插件 ID 分开。相同媒体 ID 在不同连接下互不冲突，父目录不参与媒体身份计算。本地文件继续使用路径、大小和修改时间识别；本地独立文件的 `Reference.ItemID` 是绝对路径。

`Source.Config` 只保存公开配置。系统凭据引用与连接配置分开保存；修改连接不会覆盖已有播放会话使用的旧凭据，历史也可以继续恢复已保存的来源。删除视频库不会自动清除最近播放所需的凭据。

## 脚本插件

目录结构：

```text
my-plugin/
  plugin.json
  index.ts
  locales/zh-CN.json
  locales/ja.json
```

清单示例：

```json
{
  "id": "my-drive",
  "name": "My Drive",
  "version": "1.0.0",
  "apiVersion": 1,
  "main": "index.ts",
  "network": ["$server"],
  "settings": [
    {"key": "server", "label": "Server URL", "kind": "url", "required": true},
    {"key": "apiKey", "label": "API Key", "kind": "password", "secret": true}
  ]
}
```

设置支持 `text`、`url` 和 `password`。密码字段必须标记 `secret`；URL 必须是无内嵌凭据、无查询参数的 HTTP(S) 地址。标签可以通过 `locales/<locale>.json` 的原文到译文映射翻译。访问范围显示在连接表单中。

脚本由 moejs 执行，支持 JS 与可擦除类型的 TS，不是 Node 环境。入口必须是插件目录内的单个 `.js`/`.ts` 文件；需要依赖时先打包成单入口。没有文件系统、子进程或 Node 内置模块接口。`host` 只提供 `platform`、`arch`、`sourceId`；标准 Web API 提供 `fetch`、`URL`、计时器及 AbortSignal。

每个操作独占运行时，模块编译结果复用，运行时上限 64 MiB，单次结果上限 8 MiB，调用上限 30 秒。宿主更短的 `context` 期限优先。取消操作会终止运行时、计时器和 HTTP 请求，包括不合作的 JS 死循环。一个连接的预览取消不会中断其他播放操作。

### 导出函数

每个函数接收单个参数对象，可以同步返回或返回 Promise。通用参数为 `config`（非敏感配置）、`credentials`（该连接的凭据快照）、`signal`（请求取消信号）。凭据不应进入日志、URL 查询串、浏览数据或非敏感配置。

| 函数 | 其他参数 | 返回值 |
| --- | --- | --- |
| `authenticate`，可选 | 无 | 可写入系统凭据存储的字符串映射；成功后可仅返回令牌，丢弃密码；可增加稳定 `accountId`，使宿主识别同一服务器上的账号切换 |
| `browse`，必需 | `parentId`、`cursor`、`limit` | `{items, nextCursor}` |
| `resolve`，必需 | `id`、`parentId`、`position` | 播放资源，供预览、探测和恢复队列使用，不应创建活跃播放会话 |
| `prepare`，可选 | `id`、`parentId`、`position` | 真正播放前重新获取资源，并可创建服务端会话 |
| `report`，可选 | `id`、`session`、`event` | 无 |

浏览条目最少有 `{itemId, name}`，文件夹增加 `folder: true`，可提供以秒计的 `duration`、字节数 `size` 和毫秒时间戳 `modified`。来源 ID 和当前父目录由宿主补齐。游标是不透明字符串；宿主去重并拒绝循环游标。当前界面会读取所选目录的所有分页，不递归扫描整个远程库。

播放资源示例：

```js
return {
  url: "https://video.example/video.mp4",
  headers: {"Authorization": "Bearer ..."},
  subtitle: {url: "https://video.example/video.vtt", headers: {}},
  seekable: true,
  live: false,
  expiresAt: "2026-12-01T00:00:00Z",
  session: {playSessionId: "opaque-session-id"}
};
```

`seekable` 应显式提供。`expiresAt` 和 `subtitle` 均可省略。`session` 是该插件自己的临时 JSON 数据，只在后续上报时返回，不保存到历史。`resolve` 返回的资源必须在操作会话关闭后仍能读取；服务端活跃播放资源应在 `prepare` 中创建。

抛出带 `code` 的 Error 可返回 `signIn`、`setup`、`unavailable` 或 `unsupported`。不要将凭据放进错误消息。

### 网络访问

`network` 可以声明固定域名、`*.example.com` 或 `$server` 这类配置字段。动态 URL 字段按协议、主机和端口限制到同一 origin。没有声明则脚本不能访问网络；不接受任意主机的 `*`。重定向也会重新检查权限。返回给原生播放器的资源初始地址同样校验，不接受本地文件协议。

这是宿主 API 的访问控制，原生解码器读取流中的子资源不是操作系统网络沙箱。

## 播放生命周期

`Resolve` 用于探测和预览，`Preparer.Prepare` 只对真正选中的媒体调用。播放器首次成功输出画面后上报 `start`，暂停/恢复及定期进度上报 `progress`，切换、关闭、退出和解码失败上报 `stop`。这些请求在后台按会话顺序执行，不依赖控制栏刷新。

事件包含 `type`、`position`、`duration`（均以秒计）、`paused`、`ended` 和 `failed`。`prepare` 完成后若用户已取消选择，宿主会释放已知的服务端会话。旧解码器即使 URL 相同，也不能写入新连接的进度。

播放地址每次打开时重新获取。出现播放错误时对当前条目自动重新解析一次，按当前位置恢复；后续失败保留错误，避免重试循环。切换媒体会取消尚未完成的解析。直播跳过续播定位、拖动、额外抽帧和观看进度记录。

播放、缩略图、投影识别、拖动预览及章节图片传递对应请求头，外部字幕使用自己的请求头。普通文件保留双 HTTP 游标优化，HLS/DASH 清单保留原生 HTTP 上下文，以便子资源继承鉴权。

## 内置 Jellyfin / Emby

实现位于 `internal/scriptplugin/builtin/mediaserver/`。目前支持用户名/密码或用户令牌认证、文件夹浏览、服务端原始流、可用的外部文本字幕，以及播放开始、进度和停止上报。

这版优先保留原始视频的格式、码率及播放器本地解码能力。尚未实现服务端转码、需要主动开启的 Live TV 流、版本/音轨选择界面、EPG 或其他服务的连接器。`prepare` 和 `report` 为后续扩展这些播放能力提供会话边界。

## Go 插件

本地文件和 DLNA 保持 Go 内置实现，通过公开的 `library.Plugin` / `Session` 接口接入。可选接口包括 `Preparer`、`PlaybackObserver`、`Authenticator`、`Configurable`、`Discoverer` 和 `SourceKeyer`。`Session.Close` 释放操作资源；活跃播放通过 `PlaybackObserver` 的停止事件清理。DLNA 客户端和服务端继续共用原生协议支持，发布仍只允许显式共享的本地库。

产品尚未发布，直接使用当前模型，不为历史开发版提供迁移层。
