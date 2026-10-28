# 开发与维护

本文件适用于整个仓库。根目录 `README.md` 面向普通用户，保持简洁；开发命令、架构和维护约束写在这里。功能或行为变化时同步更新相关文档，避免描述尚未实现的能力。

## 环境和验证

- 目标平台为 Windows 10/11 x64；项目使用 Go 1.27.3、mygo 原生界面和 Go 模块。模块名为 `android-tools`。
- 在仓库根目录运行：

  ```powershell
  go test -count=1 ./...
  go vet ./...
  go run ./cmd/package -out .\android-tools-windows-amd64.zip
  ```

- 打包器要求从仓库根目录运行，不覆盖已有输出；如果示例输出文件已存在，请指定新路径。它构建 Windows x64 GUI，并下载、校验和打包工具及许可证。发布工作流为 `.github/workflows/android-tools-release.yml`：手动从 `main` 运行，要求 `v主版本.次版本.修订号` 标签，测试、构建后创建标签和 Release。
- 常规测试不依赖手机。真实设备、官方组件下载和驱动测试通过测试文件中的 `ANDROID_TOOLS_TEST_*` 环境变量显式启用。`TestRealADBFileOperations` 需要 `ANDROID_TOOLS_TEST_ADB`、`ANDROID_TOOLS_TEST_ADB_SERIAL` 和 `ANDROID_TOOLS_TEST_ADB_SOCKET`，会在指定手机的 `/sdcard` 创建随机测试目录、传输和删除测试文件；使用隔离 ADB 服务，确认目标设备，并检查测试清理结果。不要对用户现有文件执行测试。
- 界面修改应检查 640×480 和 960×680，覆盖正常、加载、错误及设备断开状态；可使用测试中的 `ANDROID_TOOLS_*_PREVIEW` 输出预览。无头测试不代表 Windows 实际滚动或 GPU 性能，性能结论需要真实窗口测量。

## 代码结构

- `cmd/android-tools`：程序入口；`internal/app/app.go`：窗口、导航、后台刷新和生命周期。
- `internal/app/engine.go`、`tracker.go`、`process_windows.go`：外部工具、ADB 服务、设备事件和 Windows 进程管理。
- `internal/app/manager.go`、`pair.go`：设备记录、身份核验、无线连接和配对。
- `internal/app/adb_view.go`、`fastboot.go`、`files.go`、`terminal.go`：设备操作、文件管理和终端。
- `internal/app/fastboot_devices_windows.go`、`driver_install_windows.go`：Windows Fastboot 设备发现和驱动处理。
- `internal/app/settings.go`、`preferences.go`：应用设置；`internal/tooldeps`：官方工具准备与校验；`internal/appicon`：应用图标；`cmd/package`：发行包。

## 设备和数据约束

- 应用使用独立 ADB 服务 `tcp:127.0.0.1:50387`，退出时停止；外部本机命令不经过应用内部协调。调用 ADB、Fastboot 或 scrcpy 时必须绑定明确的目标序列号，不能假定只有一台设备。
- 保留无线 GUID、硬件身份和已保存地址的核验；地址复用、断线重连及新传输出现时不能仅依赖缓存。配对密钥不写入设备记录或错误信息。
- 设备记录和投屏设置共存于 `%APPDATA%\android-tools\devices.json`，由设备记录锁协调写入，使用同目录临时文件、同步和重命名。不要在设置与设备操作中分别覆盖整个文件。
- 界面状态只在 mygo 窗口线程更新；ADB、网络、磁盘和驱动工作放在后台。异步结果须检查页面、设备及请求代次，避免旧结果覆盖新状态。长操作应保持明确的取消、退出和错误处理。
- 文件管理从 `/sdcard` 开始，单目录最多读取 2000 项；支持上传普通文件、下载文件、重命名及删除文件或文件夹，不支持向资源管理器直接拖出。手机文件名和路径是非可信输入：校验名称、正确转义 shell 参数，并处理包含空格、引号、换行及中文的名称。`adb exec-out` 的远端退出码不可单独作为成功依据，保留明确结果标记和完整性检查。
- 上传先使用临时文件并检查同名目标；下载先暂存并避免覆盖本机已有文件。重命名不得有意覆盖同名目标；删除前必须向用户展示目标并确认，文件夹删除须说明包含其内容。不要将这些保护仅放在视觉层而绕过实际执行路径。
- 内置 Fastboot 刷写需保留设备、分区、镜像和确认步骤，刷写期间阻止应用退出。驱动安装必须保留下载摘要、签名及 Windows 设备匹配检查，并在安装前重新核验目标；不能承诺驱动只影响一台设备。
- ADB 设备 Shell 为逐行输入，不支持需要伪终端的全屏交互程序；终端输出保留最近 1 MiB。命令输入支持本机文件拖入、上下键浏览会话历史、Ctrl+L 清空输出和 Esc 停止命令。

## 组件和界面维护

- 当前固定组件为 Platform Tools 37.0.1 和 scrcpy 5.0.1。`internal/tooldeps/tooldeps.go` 中的归档摘要、清单摘要、必需文件和解压规则共同构成校验；更新时核对官方来源，并一起更新、测试和同步文档。应用与发行打包器共用这套准备逻辑；不要覆盖与发行版不一致的现有组件。
- 应用的组件和驱动下载共用网络配置与进度，默认直连；手动代理只接受 IP 和端口，系统代理按目标地址解析 Windows 用户配置。代理失败不得静默改为直连；取消下载不得留下未校验的组件。发行打包器不读取用户应用设置。
- 延迟测试先建立连接，再使用同一客户端测量后续 Google 204 响应；测速和下载结束后关闭各自空闲连接。Windows 设备通知共用一个进程级回调，注销时释放对应订阅。
- 投屏设置在下次启动投屏时生效；默认视频码率为 8 Mbps。声音不可用时依赖当前 scrcpy 的默认回退行为，不能把进程启动成功等同于声音转发成功。
- 保持单实例使用 mygo 的 `RequestSingleInstanceLock` 和 `OnSecondInstance`，并在第二次启动时恢复已有窗口。统一相关页面的按钮尺寸、图标和间距；图标按钮保留可访问标签和悬停提示。
- 依赖升级应核对官方发布说明，并运行适用测试、构建和 Windows 界面回归；避免为了迁移而保留无用的旧实现。不要提交生成的验收目录、外部工具、密钥或用户设备数据。
