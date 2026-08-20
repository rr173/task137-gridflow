# task137-gridflow — Benzhi 评测说明

## 项目解决的业务问题

电力系统**潮流计算与机组调度引擎**：为省级电力调度中心完成“建网 → 负荷预测 → 机组开停与出力排布 → 交流牛顿-拉夫逊潮流求解 → 线路热稳定/电压/无功越限校核 → 备用充裕度 → 放行/拒绝 → 周期推进 → 线路停运重解 → 重启重算一致性”闭环。主要输入是母线/支路/发电机拓扑与各周期负荷预测；主要输出是各周期机组出力、母线电压（幅值+相角）、线路潮流与负载率、越限清单与放行结论。持久化使用 SQLite，进程重启后可从库中重建拓扑与机组状态并对未放行周期重算潮流。

## 标准本地命令

```bash
go build ./...            # 编译
go run . --addr=:8080 --db=gridflow.db   # 启动服务（前端在 http://localhost:8080/）
go test ./...             # 运行单元测试
go run . --smoke-test     # 自检（内存库，执行后退出）
go run . --migrate-only   # 仅迁移后退出
```

入口为根目录 `main.go`（`go run .`）。无 `cmd/app` 子目录。

## 前端

- 目录：`internal/webfs/web/`（原生 HTML/CSS/JS，无构建工具、无 Node、无 npm）。
- 经 `//go:embed web/*` 由 Go 服务在 `/`（index.html）、`/style.css`、`/app.js` 提供，打入同一二进制。
- 页面 URL：`http://localhost:8080/`，覆盖建母线/支路/发电机 → 录入负荷 → 初始化/推进周期 → 排布+求解潮流 → 查看电压/潮流/越限/放行结论 → 机组投运/停运 → 线路停运/复役 → 重启重算 → 审计事件的完整读写流程。
- 无前端构建步骤；`go build ./...` 即把页面打进二进制（无独立产物目录、无 `node_modules`）。

## smoke-test（同时验证前端与业务 API）

`go run . --smoke-test` 在内存库上启动 httptest 服务，执行端到端场景：建网→录负荷→排布→求解潮流→越限→放行/拒绝→min_up 守卫→线路停运重解→重启重算一致性→前端页面可达，校验页面 HTML 与 `/summary` 业务 API 联动，执行后自行退出（退出码 0）。

## benzhi 构建脚本

`build_benzhi_docker.sh` 接受两个参数：镜像名、目标平台。

```bash
bash ./build_benzhi_docker.sh go-task-benzhi:amd64 linux/amd64
docker run -it go-task-benzhi:amd64          # 进入容器 shell
bash ./build_benzhi_docker.sh go-task-benzhi:arm64 linux/arm64
docker run -it go-task-benzhi:arm64
```

镜像内已 `go build ./...` 预编译；`go run . --smoke-test` 可在镜像内运行验证。amd64 与 arm64 均通过根目录 `benzhi.Dockerfile` 构建。`.dockerignore` 排除 `vendor/`、`node_modules`（本项目无）、`*.db` 与二进制，使两架构均在镜像内从源码重建。

## 组件版本

Go 1.26.3（`go.mod` 指令 `go 1.26.3`，`GOTOOLCHAIN=local`，`CGO_ENABLED=0`）；SQLite 经纯 Go 驱动 `modernc.org/sqlite v1.52.0`（对应 SQLite 3.46.1）；与 `env/component-versions.json` 及 `docs/component_versions.lock.json` 一致。Docker 构建器 `docker.m.daocloud.io/library/golang:1.26.3-bookworm`，运行时 `docker.m.daocloud.io/library/alpine:3.20`，benzhi 评测镜像 `golang:1.26.3`。
