# core：全组件共用的名字与写法（共用）

每个模块都引用、自己什么业务也不做的东西。改这里会影响所有模块。

## 负责与不负责

- 负责：组件 ID、依赖的 ID、流与事件类型、资源与操作、法人维度、暂存期限 `HoldTTL`、资源定义 `Definitions()`（`names.go`）；本组件的错误原因、格式、授权检查、校验、金额的检查（`Amount`）、审计（`Change`）（`rules.go`）；记录的类型、读取、发件表（`Emit`、`EmitLimit`）（`records.go`）；`Base`（`base.go`）。
- 不负责：任何业务规则（在 `limits`、`branches`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `base.go` | `Base`：组件的运行时 |
| `names.go` | 常量；`fields`、`Definitions()` |
| `rules.go` | 错误原因、格式、`Check`、`Invalid`、`NotFound`、`Amount`、`Change` |
| `records.go` | `Policy`、`Limit`、`Reference`、`Branch` 与 `Scan…`、`Get…`、`One`；`Emit`、`EmitLimit` |

## 依赖与被依赖

- 依赖：只有 SDK 与契约。
- 被依赖：全部模块与入口。

## 规则在哪

`docs/design.md`；装配项目协议 04、安全 07。金额只在 SQL 里比较与加减；额度与占用的列是 `NUMERIC(19,4)`。

## 测试

由 `itest/` 的流程测试间接覆盖。
