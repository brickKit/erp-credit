# branches：预占分支

装配项目协议 04 的参与方：`erp/sales` 的确认过程在这里预占信用分支。每个分支只涉及一个额度行。**分支的状态机在 SDK**（be-sdk-go 的 `reserve`，线协议第 16 章）：状态与迁移、按分支身份与内容摘要幂等、墓碑、暂存期限与到期、按消耗身份幂等、先锁额度行再锁分支都在那里；这里只写占什么、还什么、消耗什么。

## 负责与不负责

- 负责：参与的种类 `Kind`（`erp.credit.order`）与它的三个函数（锁额度行、退回未消耗的部分、发 `erp.credit_branch.changed.v1`）；表 `branch`（分支占用的额度行、客户、法人、币种、金额、已消耗、引用、预占时的可用额度）、`consumption`（消耗的金额）；`ReservationService` 的 `Hold`（`take`：额度行锁下比较可用，超限拒绝并发 `erp.credit_limit.exceeded.v1`）、`Confirm`、`Cancel`、`Query`、`Consume`、`Adjust`（本版不做）；暂存到期的工作项（`reserve.ExpireDue`）；`CreditService.Available`；用户面读一个分支。
- 不负责：分支的状态与它的迁移（SDK 的 `reserve`，组件不写 `be_reserve_branch`，`credit/structure_test.go` 检查）；订单占用的写法（`limits` 的 `TakeOrder`、`ConsumeOrder`、`GiveBackOrder`）；额度与政策本身（`limits`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `branches.go` | `branchStates`（协议 04 的状态与线上值）、`wire`、`branchID`、`holdContent`；`kind`、`Kind`、`answered`；`Hold` 与 `take`；`onBranch`；`Confirm`、`Cancel`、`Query`、`Consume`（拒绝按本组件的契约答：冲突 `CONTENT_CONFLICT`，没确认或超出剩余 `STATE_CONFLICT`，没有分支答 `ABSENT`）、`Adjust`；`ExpireHolds`；`Available`；`GetBranchHTTP` |

## 依赖与被依赖

- 依赖：`core`、`limits`（`PolicyOf`、订单占用的三个函数、`Lock`）；SDK 的 `reserve`。
- 被依赖：入口 `credit/services.go` 与 `credit/module.go`。

## 规则在哪

线协议第 16 章（P16.1–P16.13）；`docs/design.md` 的“每个命令改什么”；装配项目协议 04（RS 编号）。

## 测试

`itest/branches_integration_test.go`（`racingHolds`、`branchLifecycle`、`lateAndExpired`）、`itest/participant_integration_test.go`（`participant`：业务流程走不到的格）；`itest/policies_integration_test.go`、`itest/limits_integration_test.go`（`suspendedLimit` 的预占与可用额度）。状态机本身的测试在 SDK（`reserve` 的向量与集成测试）。
