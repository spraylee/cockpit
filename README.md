# svc

人和 coding agent 共用的本机服务管理命令。按需启动，跨终端和会话查看；不安装 LaunchAgent、不修改开机启动项。底层用 [Process Compose](https://github.com/F1bonacc1/process-compose) 管进程和 TUI，`svc` 负责提供统一入口。

## 安装

支持 macOS / Linux 的 arm64 和 x86_64。需要 GitHub CLI，并在 `gh auth status` 中确认登录的是能访问本私有仓库的账号。

```sh
gh release download --repo spraylee/svc --pattern install.sh
sh install.sh
svc --version
```

安装器从已认证的 GitHub Release 获取当前平台的原生二进制，校验 SHA-256 后安装到 `~/.local/bin`。它不需要 Go/Node/npm，不改 shell 配置，不启动服务。可用 `sh install.sh v0.1.0` 固定版本；确认现有 `svc` 属于本项目后，用 `SVC_REPLACE=1 sh install.sh` 更新。不想下载新脚本时，可以直接用仓库中同版本的 `scripts/install.sh`。

运行服务需要 **Process Compose v1.124.0 或更新版本**（v1.124.0 已进行集成测试）。如未安装，macOS 使用作者的 Homebrew tap：

```sh
brew tap f1bonacc1/tap
brew trust --formula f1bonacc1/tap/process-compose
brew install f1bonacc1/tap/process-compose
```

Linux 或不使用 Homebrew 时，从 [官方安装页](https://f1bonacc1.github.io/process-compose/installation/) 安装，并确认 `process-compose version` 可用。

## 使用

在项目目录运行：

```sh
svc start docs-preview --note '临时看文档' -- python3 -m http.server 8901 --bind 127.0.0.1
svc list
svc ui
```

- `svc up` 也可显式拉起一个空后台；新建服务的 `svc start … -- CMD` 会在需要时自动执行这一步。
- `svc list --json` 供 agent 读取；`svc list` 显示名称、状态、时长、端口、命令和备注。`svc logs NAME` 查看最近 100 行。
- `svc ui` 连接现有后台；选中服务按 `F3` 查看备注、命令和工作目录。`F10` 或 `Ctrl+C` 只退出远程 TUI，确认提示为 **Detach from the remote project?**。增删登记请用 `svc`，不要与 TUI 命令面板的新增/删除并行操作。
- `svc stop NAME` 停止并保留登记及日志，`svc start NAME` 可再启动，`svc restart NAME` 可重启，`svc rm NAME` 停止并移除登记。
- `svc down` 停止本工具的全部受管服务并退出后台。随后 `svc up` 是全新的空列表，不会自动恢复旧命令。

标题用服务名称（如 `docs-preview`），长描述用 `--note`。新建成功代表任务已提交给管理器，**不代表应用已就绪**；用 `svc list` 或应用自身的健康检查确认。HTTP 示例只监听回环地址，服务实际监听哪里由你传入的命令决定。

## 给 agent 的约定

> 仅在用户或项目规则允许启动服务时使用 `svc`。从项目目录执行 `svc start NAME --note '用途' -- CMD ARGS…`；不要使用裸 `&` / `nohup`。先用 `svc list --json` 查看受管服务，再按名称检查日志、启停。只停止当前任务中明确授权的服务，不按系统端口或 PID 猜测归属；`svc down` 会结束所有受管任务，不能代替单服务的 `svc stop`。

`--` 后的内容是原样传入的 argv，不做第二次 shell 展开。需要管道等 Shell 语法时显式用 `sh -c`。服务使用注册时的当前目录和 `PATH`，不自动复制 agent 的整个环境；保密参数请由项目自己的环境文件或密钥机制加载，不要放进命令行或备注。

## 边界

- 限本机当前用户；管理 API 使用私有目录的 Unix socket，不监听 TCP。默认状态目录 `~/.spraylee-svc`，可用 `SVC_HOME` 指定独立的绝对路径。
- 不扫描或收养已运行的进程，不按会话或时间自动关闭。退出 agent 或 TUI 后后台继续运行，直到你停止。服务登记和应用日志只留在这次后台会话里；后台退出后不保留历史。
- 被托管命令必须以前台方式运行。不要在命令内再次 `nohup` / 自行 daemonize；强杀后台、重启机器或服务自行脱离进程组不属于正常 `stop` / `down` 保证。

## 开发

```sh
go build -o /tmp/svc-dev .
go test ./...
SVC_INTEGRATION=1 go test -v -timeout 90s ./...
```

集成测试使用独立状态目录和随机本地端口，结束时关闭该目录的实例，不会连接日常使用的管理器。推送 `vX.Y.Z` tag 会先在 macOS 和 Linux 上测试，再发布各平台资产与校验和。包名和发布资产命名沿用 [binox](https://github.com/spraylee/binox) 的规范；下载私有资产则交给 `gh`，不要求公开仓库或发布 npm 包。
