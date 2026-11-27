# 决策

## 按需后台，登记只活在本次实例里
不使用 LaunchAgent、TTL 或自动恢复。`up` 启动的是空 Process Compose；动态注册已在 v1.124.0 上验证，因而不需要维护第二份 YAML 登记。

## 原生 Release，认证下载
沿用 binox 的平台归档命名；私有仓库的下载交给已登录的 `gh`。Go 标准库实现，无须 npm 发布或在目标机安装运行时。
