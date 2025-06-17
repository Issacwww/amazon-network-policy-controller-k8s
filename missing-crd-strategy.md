
# ✅ Kubernetes Controller 健康状态上报设计文档

## 🎯 背景问题描述

在 AWS Network Policy Controller 中，当启用了某个 feature（如使用 `PolicyEndpoint` CRD）但集群中未部署该 CRD 时，会导致：

* `controller-runtime.NewManager()` 在解析 GVK 时报错，进而 `os.Exit` 无法启动 Controller。
* controller manager 无法暴露 `/healthz` 端点，导致 CloudWatch Metric 或其他监控误认为系统崩溃。
* 实际上，这种错误是**客户预期之外的使用方式（customer-induced error）**，不应被视为系统级错误。

---

## 🎯 设计目标

我们希望构建一个**独立于 controller manager 的健康探针服务**，具备以下特性：

* 支持健康状态查询，即使 controller manager 启动失败；
* 明确区分错误类型：正常、可恢复错误（如 CRD/config 缺失）、不可恢复错误；
* 对外暴露统一的健康检查端点 `/healthz/custom`；
* 可被 CloudWatch Metric Agent 或 Prometheus 等工具主动探测；
* 避免误报，降低报警噪声。

---

## 🧱 架构核心：自定义健康探针服务

### ✅ 独立的 health server

* 启动于主函数开头的 goroutine 中
* 不依赖 controller manager
* 监听如 `:8081` 端口，暴露 `/healthz/custom`

```go
health.StartHealthServer(":8081")
```

### ✅ 状态定义（HealthState）

状态保存在一个原子对象中，具有如下字段：

| 字段                | 说明                                 |
| ----------------- | ---------------------------------- |
| `ControllerReady` | controller 是否已成功注册                 |
| `DegradedReason`  | 表示是否进入 degraded 模式，如 CRD/config 缺失 |
| `FatalError`      | 程序致命失败（panic、初始化错误等）               |

---

## 🔁 状态响应逻辑

请求 `/healthz/custom` 时，根据当前状态返回不同的 HTTP 状态码和消息体：

| 状态     | 返回码                         | 示例消息                         | 触发条件                                     |
| ------ | --------------------------- | ---------------------------- | ---------------------------------------- |
| ✅ 正常   | `200 OK`                    | `ok`                         | Controller 启动并运行                         |
| ⚠️ 降级  | `424 Failed Dependency`     | `degraded: missing CRD`      | CRD/config 缺失等客户操作问题                     |
| ❌ 致命错误 | `500 Internal Server Error` | `fatal: panic in reconciler` | 初始化失败、panic                              |
| ⏳ 未就绪  | `503 Service Unavailable`   | `not ready`                  | Controller 尚未就绪或未调用 `SetControllerReady` |

---
