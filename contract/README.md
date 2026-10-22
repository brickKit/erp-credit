# erp-credit 的契约

`erp/credit` 发布的契约包：系统面的 protobuf、事件载荷的 JSON Schema、用户面的 OpenAPI。放在组件仓库的 `contract/` 子目录里单独发布（标签 `contract/v3.…`）。

依据：装配项目 [be-assembly-standard](https://github.com/brickKit/be-assembly-standard) 的协议 04（预占分支）、协议 05（应收占用的转换，以后）、协议 14（金额、列表）；组件的 `docs/design.md`。

## 内容

| 位置 | 是什么 |
| --- | --- |
| `proto/be/erp/credit/v3/credit.proto` | `ReservationService`（协议 04 的参与方命令，与 `erp/inventory` 同形：`Hold`、`Confirm`、`Cancel`、`Query`、`Consume`、`Adjust`）；`CreditService.Available` |
| `gen/go/be/erp/credit/v3/` | Go 代码，包 `creditv3`；模块 `github.com/brickKit/erp-credit/contract/v3` |
| `python/` | Python 包 `contract-erp-credit`，模块 `be.erp.credit.v3` |
| `events/` | 流 `erp.credit`：政策、额度、分支（完整状态），超限（增量事实） |
| `openapi.yaml` | 用户面；每个路由的操作写在 `x-operation` |

## 要点

- **金额**是十进制字符串，额度按（伙伴、法人、币种），订单只占同币种的额度，不换算。
- **不受信用控制的客户**是一条 `limit` 为空的额度行：预占照样成功并记占用，从不因额度拒绝。法人对“没有额度行”的处理是政策：`block` 拒绝（`no_limit`），`unlimited` 自动建一条空额度行；法人没有政策时按 `block`。
- **预占分支**与库存同形：分支身份由协调者生成，每个命令按它幂等；同身份不同内容冲突；取消先到留下墓碑；`Query` 不存在的分支答 `ABSENT`。拒绝原因：`over_limit`、`no_limit`、`limit_suspended`。
- **能力不支持**（D75）：`Adjust`、临时额度与例外放行，答 `CAPABILITY_UNSUPPORTED`。本版不跟踪应收占用（`receivables_tracked: false`）。
