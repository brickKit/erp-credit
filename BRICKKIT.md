# erp/credit

## 组件定位

信用：对客户做信用控制。需要信用控制的客户安装；不装时订单确认没有信用分支（“无信用控制”）。
- **额度**：按（伙伴、法人、币种），订单只占同币种的额度，不换算。额度可以改到低于已用（只影响之后的预占）；停用的额度不接受新的预占，已有分支照常确认、取消、消耗。
- **不受信用控制的客户**：一条金额为空的额度行，预占照样成功并记占用，从不因额度拒绝。
- **信用政策**：每个法人对“没有该币种额度的客户”的处理：`block` 拒绝（法人没有政策时也按它），`unlimited` 自动建一条空额度行。
- **预占分支**（协议 04 的参与方，与 `erp/inventory` 同形）：预占、确认、取消、查询、消耗；取消先到留下墓碑；暂存 5 分钟，已到期的不能确认复活；确认占用没有自动到期。拒绝原因：`over_limit`（同时发一条超限事实）、`no_limit`、`limit_suspended`。
- **可用额度查询**：系统面，供协调者显示。

应收与核销不在这里（`erp/finance`）。本版只按订单占用控制：订单出账后占用仍记在订单分支上，订单完成或关闭时销售释放剩余分支（额度事件带 `receivables_tracked: false`）。答“能力不支持”（`CAPABILITY_UNSUPPORTED`，D75）的：应收占用与它的转换、临时额度与例外放行、分支的调整（订单修订）、期初。

## 部署前准备

- 数据库：PostgreSQL 16 及以上；schema `erp_credit`，属主角色 `erp_credit_owner`（只供迁移）与运行角色 `erp_credit_rw`，名字来自装配项目的注册表，由部署方在第一次启动前建好；表由本组件的迁移建。
- 消息系统（`BUS_URL`）与实现实例（`IMPLEMENTATION_INSTANCE`）：政策、额度、分支以完整状态事件、超限以增量事实发布到流 `erp.credit`（4 个分区）。
- 部署的服务证书（`SERVICE_TLS_*`）；身份与授权（`IAM_*`、`AUTHZ_GRPC_URL`、`DEPLOYMENT_ID`）；审计（`AUDIT_GRPC_URL`）。
- 依赖 `mdm/org`（法人启用）、`mdm/partner`（客户资格）、`mdm/currency`（币种启用与小数位），都经系统面读取。

## 依赖说明

- 必需：数据库、消息系统、身份成员、授权成员、`mdm/org`、`mdm/partner`、`mdm/currency`。
- 可选：审计组件。
- 主数据不可达时：建额度、设政策答“依赖不可用”；预占、确认、取消、消耗不读主数据，照常。

## 配置指南

全部键见 `component.yaml` 的 `configSchema`（由 SDK 生成，测试核对）。

| 键 | 说明 |
| --- | --- |
| `DEPLOYMENT_ID`、`IAM_ISSUER`、`IAM_JWKS_URL`、`AUTHZ_GRPC_URL`、`SERVICE_TLS_CERT_FILE`、`SERVICE_TLS_KEY_FILE`、`SERVICE_TLS_CA_FILE`（必填） | 受保护的用户面与 mTLS 的系统面要用；在外壳里是进程级的键 |
| `PG_HOST`、`PG_DATABASE`、`PG_USER`、`PG_PASSWORD`、`PG_SCHEMA`（必填） | 自己的数据库，密码是密钥；迁移另用 `PG_OWNER_USER`、`PG_OWNER_PASSWORD` |
| `BUS_URL`（必填，密钥）、`IMPLEMENTATION_INSTANCE`（必填） | 消息系统；实现实例是安装时生成的随机身份 |
| `AUDIT_GRPC_URL` | 审计组件的系统面；缺时审计记录留在本地，按协议 03 处理 |

依赖的地址由 BrickKit 按依赖注入（`MDM_ORG_GRPC_ENDPOINT`、`MDM_PARTNER_GRPC_ENDPOINT`、`MDM_CURRENCY_GRPC_ENDPOINT`），不是配置键。

## 契约索引

- **系统面**（本仓库 `contract/proto`，模块 `github.com/brickKit/erp-credit/contract/v3`，标签 `contract/v3.…`）：
  - `be.erp.credit.v3.ReservationService/Hold`、`Confirm`、`Cancel`、`Query`、`Consume`（`Adjust` 本版不支持）：只对 `erp/sales` 开放，操作 `erp.credit.system.reserve`。
  - `be.erp.credit.v3.CreditService/Available`：任一组件，操作 `erp.credit.system.read`。
- **用户面**：`contract/openapi.yaml`。操作 `erp.credit.limit.read`、`erp.credit.limit.manage`、`erp.credit.policy.read`、`erp.credit.policy.manage`（`erp.credit.limit.request` 本版不支持）；资源带法人维度。
- **事件**（流 `erp.credit`，schema 在 `contract/events/`）：`erp.credit_policy.changed.v1`、`erp.credit_limit.changed.v1`、`erp.credit_branch.changed.v1`（完整状态），`erp.credit_limit.exceeded.v1`（增量事实）。

## 外壳声明

不是外壳。可以被 Go 的核心外壳（`be/go-core`）承载。
