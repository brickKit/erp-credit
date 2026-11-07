# itest/：集成测试与测试索引

信用组件对着真实的 PostgreSQL 与消息系统、经它真实的接口（用户面带 be-sdk-go `sectest` 的令牌；系统面走 mTLS，测试以 `erp/sales` 的身份调用）的测试（L2）。`mdm/org`、`mdm/partner`、`mdm/currency` 是按契约写的替身。构建标签 `integration`；一条业务流程一个文件。

## 怎样跑

- `make test-integration`（`go test -count=1 -tags integration ./itest/`），约一秒。
- 环境变量：`BE_SDK_TEST_PG_ADMIN=postgres://postgres:<口令>@localhost:5432/brickkit_test_db`（口令是装配项目 `.env` 的 `POSTGRES_PASSWORD`）、`BE_SDK_TEST_NATS_URL=nats://localhost:4222`。
- 前提：装配项目里 `make up` 起了 `be-postgres` 与 `be-nats`；库 `brickkit_test_db` 存在；schema `erp_credit_it` 每次重建；端口 28088、9088 与替身的 19099、19100、19101 要空着。
- **只有一个测试** `TestCreditEndToEnd`：按下表的顺序跑各步（普通的方法调用，不是子测试），后面的步用前面建的额度，所以一步不能单独跑。

## 文件

按运行顺序：

| 文件 | 步 | 测的流程 | 被测的模块 |
| --- | --- | --- | --- |
| `policies_integration_test.go` | `policies` | 没有政策时没有额度的客户被拒；不受控制的政策下照常预占、占用照记、出现一条没有额度的额度行 | `branches`、`limits` |
| `limits_integration_test.go` | `limits` | 建额度：必须是这个法人的客户、币种可用、金额合乎小数位；一个客户、法人、币种一条 | `limits`、`masterdata` |
| `branches_integration_test.go` | `racingHolds`、`branchLifecycle`、`lateAndExpired` | 两张订单同时确认只有额度内的成功、十张抢剩下的恰好八张成功、每次超限一条事实、额度按币种；同样的预占答第一次、内容不同被拒（RS17），确认、消耗（幂等）、取消退回其余；先取消后预占留墓碑（RS04）、不认识的分支答不存在、过期的暂存不复活 | `branches` |
| `participant_integration_test.go` | `participant` | 协议 04 的分支迁移里业务流程走不到的格：已结束的分支再确认、再取消不变；只有已确认的能消耗、不超出剩余、消耗完成为已消耗；消耗身份换金额是冲突；查询不留墓碑；到期作业让过期的暂存到期；调整答“不支持”；可用额度前后相同 | `branches` |
| `limits_integration_test.go` | `suspendedLimit` | 暂停的额度不接新的预占；可用额度 | `branches`、`limits` |
| `recoverycheck_integration_test.go` | `recoveryChecks` | 整套恢复的核对：额度与政策按版本答“还在不在”（`checksStory`） | 入口 `credit/checks.go` |

共用的：

| 文件 | 内容 |
| --- | --- |
| `endtoend_integration_test.go` | `TestCreditEndToEnd`（各步的顺序）；三个替身；`fixture`（运行中的成员、系统面的连接、法人与客户、步与步之间传的额度）；`hold`；`start`；`client.do`、`serve`、`portFree` |

## 加测试

新流程加一个文件与一步，在 `TestCreditEndToEnd` 里按依赖排进顺序，在上表加一行；测试不为了通过而改。
