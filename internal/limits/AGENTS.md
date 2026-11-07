# limits：政策与额度

法人的信用政策（没有额度的客户怎样处理：拒绝或不受控制）与客户按（法人、币种）的额度：额度、订单占用、可用、状态。

## 负责与不负责

- 负责：表 `credit_policy`、`credit_limit`；路由 `/api/credit/policies`、`/api/credit/limits` 的读取、设置、修改、列表；临时提额（本版不做，`Unsupported`）；事件 `erp.credit_policy.changed.v1`、`erp.credit_limit.changed.v1`。
- 不负责：预占怎样占用额度（`branches`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `limits.go` | `PolicyOf`、`GetPolicy`、`SetPolicy`；`CreateLimit`（客户资格、币种、小数位在事务外取证；一个客户、法人、币种一条）、`limit`、`GetLimitHTTP`、`UpdateLimit`、`equal`；`ListPolicies`、`ListLimits`；`Unsupported` |

## 依赖与被依赖

- 依赖：`core`、`masterdata`。
- 被依赖：`branches`（`PolicyOf`）；入口登记路由。

## 规则在哪

`docs/design.md` 的规则与“每个命令改什么”；安全 07（法人维度）。

## 测试

`itest/policies_integration_test.go`（`policies`）、`itest/limits_integration_test.go`（`limits`、`suspendedLimit`）。
