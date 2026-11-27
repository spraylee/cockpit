# 运行契约

- `svc up` / `svc start NAME -- CMD` 是启动入口；安装、`svc list` 和 `svc ui` 不启动后台。
- 仅通过用户私有 Unix socket 管理本实例的服务，不扫描系统 PID 或端口，不依赖开机自启。
- 注册时保留 argv、当前目录、PATH 和备注。任务以当前用户前台进程的方式运行，不使用自动重启或 TTL。
- `svc stop NAME` 保留定义；`svc rm NAME` 移除定义；`svc down` 停止该实例并丢弃本次运行的登记。
- 验收：`SVC_INTEGRATION=1 go test -v -timeout 90s ./...`，使用独立状态目录。Process Compose v1.124.0 已在 macOS arm64 实测。
