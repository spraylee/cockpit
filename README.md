# cockpit

人和 coding agent 共用的本机服务管理命令，别名 `ck`。按需启动，跨终端和会话查看；不安装 LaunchAgent、不修改开机启动项。底层用 [Process Compose](https://github.com/F1bonacc1/process-compose) 管进程和 TUI，`cockpit` 负责提供统一入口。

## 安装

```sh
curl -fsSL https://github.com/spraylee/cockpit/releases/latest/download/install.sh | sh
```

装到 `~/.local/bin`（`cockpit` 和指向它的 `ck` 软链），校验 SHA-256 后替换，不改 shell 配置，也不启动任何服务。

```sh
# 固定版本
curl -fsSL https://github.com/spraylee/cockpit/releases/latest/download/install.sh | sh -s v0.2.0

# 换安装目录
curl -fsSL https://github.com/spraylee/cockpit/releases/latest/download/install.sh | COCKPIT_BIN_DIR="$HOME/bin" sh
```

支持 macOS 和 Linux 的 arm64 / x86_64。

## 更新

```sh
cockpit update
```

下载最新 release、校验 SHA-256、原地替换二进制。`ck` 是指向 `cockpit` 的软链，所以两个名字一起生效，不用重装。

## 依赖

运行服务需要 **Process Compose v1.124.0 或更新版本**（v1.124.0 已集成测试）。macOS 用作者的 Homebrew tap：

```sh
brew tap f1bonacc1/tap
brew trust --formula f1bonacc1/tap/process-compose
brew install f1bonacc1/tap/process-compose
```

其他平台见 [官方安装页](https://f1bonacc1.github.io/process-compose/installation/)，装完确认 `process-compose version` 可用。

## 使用

在项目目录运行：

```sh
cockpit start docs-preview --note '临时看文档' -- python3 -m http.server 8901 --bind 127.0.0.1
cockpit list
cockpit ui
```

- `cockpit up` 也可显式拉起一个空后台；新建服务的 `cockpit start … -- CMD` 会在需要时自动执行这一步。
- `cockpit list --json` 供 agent 读取；`cockpit list` 显示名称、状态、时长、端口、**目录**、命令和备注。`DIR` 列把 `$HOME` 缩成 `~`、过长路径缩成 `…/最后两段`，完整绝对路径只在 `--json` 里。`cockpit logs NAME` 查看最近 100 行。
- `stop` / `restart` / `rm` 成功后也会回显所属目录，用来确认动的是预期的那个项目。
- `cockpit ui` 连接现有后台；选中服务按 `F3` 查看备注、命令和工作目录。`F10` 或 `Ctrl+C` 只退出远程 TUI，确认提示为 **Detach from the remote project?**。增删登记请用 `cockpit`，不要与 TUI 命令面板的新增/删除并行操作。
- `cockpit stop NAME` 停止并保留登记及日志，`cockpit start NAME` 可再启动，`cockpit restart NAME` 可重启，`cockpit rm NAME` 停止并移除登记。
- `cockpit down` 停止本工具的全部受管服务并退出后台。随后 `cockpit up` 是全新的空列表，不会自动恢复旧命令。

标题用服务名称（如 `docs-preview`），长描述用 `--note`。新建成功代表任务已提交给管理器，**不代表应用已就绪**；用 `cockpit list` 或应用自身的健康检查确认。**停服务前先看 `DIR`**：服务名不会跨项目冲突，但记错服务名仍可能停错项目。HTTP 示例只监听回环地址，服务实际监听哪里由你传入的命令决定。

## 给 agent 的约定

> 仅在用户或项目规则允许启动服务时使用 `cockpit`。从项目目录执行 `cockpit start NAME --note '用途' -- CMD ARGS…`；不要使用裸 `&` / `nohup`。先用 `cockpit list --json` 查看受管服务，**并核对 `cwd` 确认它属于当前项目**，再按名称检查日志、启停。只停止当前任务中明确授权的服务，不按系统端口或 PID 猜测归属；`cockpit down` 会结束所有受管任务，不能代替单服务的 `cockpit stop`。

`--` 后的内容是原样传入的 argv，不做第二次 shell 展开。需要管道等 Shell 语法时显式用 `sh -c`。服务使用注册时的当前目录和 `PATH`，不自动复制 agent 的整个环境；保密参数请由项目自己的环境文件或密钥机制加载，不要放进命令行或备注。

## 边界

- 限本机当前用户；管理 API 使用私有目录的 Unix socket，不监听 TCP。默认状态目录 `~/.cockpit`，可用 `COCKPIT_HOME` 指定独立的绝对路径。
- 不扫描或收养已运行的进程，不按会话或时间自动关闭。退出 agent 或 TUI 后后台继续运行，直到你停止。服务登记和应用日志只留在这次后台会话里；后台退出后不保留历史。
- 被托管命令必须以前台方式运行。不要在命令内再次 `nohup` / 自行 daemonize；强杀后台、重启机器或服务自行脱离进程组不属于正常 `stop` / `down` 保证。
- `ck` 同时是 Homebrew `concurrencykit` 的别名。若你装了那个 C 库且 `/opt/homebrew/bin` 在 PATH 中优先，它会遮住本工具的 `ck`；安装脚本检测到这种情况会警告，`cockpit` 全名始终可用。

## 开发

```sh
go build -o /tmp/cockpit-dev .
go test ./...
COCKPIT_INTEGRATION=1 go test -v -timeout 90s ./...
```

集成测试使用独立状态目录和随机本地端口，结束时关闭该目录的实例，不会连接日常使用的管理器。推送 `vX.Y.Z` tag 会先在 macOS 和 Linux 上测试，再发布各平台资产与校验和。
