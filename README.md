# android-tools

Windows 本机 Android 设备管理工具，使用 Go 和 mygo 原生界面，支持 ADB、Fastboot 和 scrcpy。发行包无需安装：解压后运行 `android-tools.exe`，工具及其依赖位于同级 `android-tools` 文件夹。

## 使用

- 通过 USB 授权或无线调试连接手机；无线连接支持二维码、配对码及已配对设备地址。扫码配对成功后若未自动连接，可在同一页面填写手机无线调试首页的连接地址；已保存的手机可在“已配对”中选择并核验后连接，无需重复扫码。
- 设备列表展示连接状态；ADB 设备可读取信息、安装 APK、截图、重启或启动 scrcpy。Fastboot 支持读取信息、重启和镜像刷写。
- 列表顶部的下载图标可检查组件并下载缺失文件。已有文件与发行版不一致时不会覆盖；请退出应用并移走旧组件目录后重试。
- 设备记录保存在 `%APPDATA%\android-tools\devices.json`；配对密钥不写入记录。刷写前请自行核对目标设备、分区和镜像。

本机终端或有本机命令执行权限的 AI 可以使用 `android-tools\adb.exe` 连接应用运行期间的 `tcp:127.0.0.1:50387` ADB 服务，并使用 `android-tools\fastboot.exe` 操作 Fastboot 设备。调用时应显式指定目标序列号；外部命令不经过应用内的操作协调，应用退出后自有 ADB 服务停止。

## 开发与发布

需要 Windows 10/11 x64 和 Go 1.27.1，或支持自动下载工具链的 Go。项目自身的本地模块名为 `android-tools`；mygo 等第三方库通过 Go 模块管理。

```powershell
go test ./...
go vet ./...
go run ./cmd/package -out <新文件名>.zip
```

构建器和应用按钮共用组件准备逻辑，从官方来源下载并校验固定摘要的 Platform Tools 37.0.1 与 scrcpy 5.0.1；上游包变化时应核对来源并同步更新归档及清单摘要。压缩包包含应用 EXE、独立工具及其许可证。

应用图标改编自 [Lucide smartphone](https://lucide.dev/icons/smartphone)，遵循 [Lucide ISC 与 Feather MIT 许可证](https://github.com/lucide-icons/lucide/blob/main/LICENSE)。

将此工程推送到独立 GitHub 仓库后，可在 Actions 中手动运行“发布 android-tools”，输入未使用的 `v1.2.3` 格式标签。流程测试、构建并发布单个 ZIP；不会自动上传到其他仓库。

目前尚未完成真实手机、多设备和 Windows 10 实机验收。mygo 与当前 Go 工具链要求 Windows 10 或以上；Windows 10 使用普通背景回退，Windows 10 以下不受支持。

设备操作页可打开 ADB 或 Fastboot 命令面板，命令绑定当前设备。ADB 的设备 Shell 使用逐行输入，暂不支持需要伪终端的全屏交互程序；输出仅保留最近 1 MiB。命令输入框支持拖入本机文件；上下方向键浏览本次会话历史，Ctrl+L 清空输出，Esc 停止当前命令。
