# branches：预占分支

装配项目协议 04 的参与方：`erp/sales` 的确认过程在这里预占信用分支。每个分支只涉及一个额度行。

## 负责与不负责

- 负责：表 `branch`、`consumption`；`ReservationService` 的 `Hold`（额度行锁下比较可用，超限拒绝并发 `erp.credit_limit.exceeded.v1`）、`Confirm`、`Cancel`（不认识的分支留下墓碑，拒绝迟到的预占，RS04）、`Query`、`Consume`（按消耗身份幂等）、`Adjust`（本版不做）；暂存到期的工作项；`CreditService.Available`；用户面读一个分支；事件 `erp.credit_branch.changed.v1`；订单占用（`credit_limit.order_exposure`）随分支增减。
- 不负责：额度与政策本身（`limits`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `branches.go` | `branchStates`（协议 04 的状态与线上值）、`wire`、`branchID`、`holdContent`；`Hold`；`release`（退回未消耗的部分）、`setState`、`lockBranch`（先锁额度行再锁分支行）、`onBranch`；`Confirm`、`Cancel`、`Query`、`Consume`、`Adjust`；`ExpireHolds`；`Available`；`GetBranchHTTP` |

## 依赖与被依赖

- 依赖：`core`、`limits`（`PolicyOf`）。
- 被依赖：入口 `credit/services.go` 与 `credit/module.go`。

## 规则在哪

`docs/design.md` 的“每个命令改什么”；装配项目协议 04（RS 编号）。

## 测试

`itest/branches_integration_test.go`（`racingHolds`、`branchLifecycle`、`lateAndExpired`）；`itest/policies_integration_test.go`、`itest/limits_integration_test.go`（`suspendedLimit` 的预占与可用额度）。
