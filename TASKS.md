# v0.1.0

- [x] 验证 Process Compose 的 Unix socket、动态新增、端口、日志和进程状态。
- [x] 实现按需后台、前端 CLI 和原生 TUI 入口；两次隔离集成测试通过。
- [x] 四平台原生二进制可编译，编写 GitHub Release 工作流和认证安装器。

交接：运行 `SVC_INTEGRATION=1 go test -v ./...` 回归；实际 TUI 交互可用运行中的 `svc ui` 手动验收。
