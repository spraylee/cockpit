# 已知陷阱

## 列表接口不带备注
症状：`process list -o json` 返回状态，但没有备注或命令。
根因：元数据在 `/process/info/{name}`，而不是 `/processes`。
修法：`cockpit list` 按受管名称合并两路数据；原生 TUI 直接用 `F3` 查详情。
检测：`COCKPIT_INTEGRATION=1 go test -run TestProcessComposeLifecycle` 会核对备注、原样 argv 与实际监听端口。
