# erp/credit

信用组件的代码。用法与边界在 `BRICKKIT.md`，为什么这样设计（术语、数据、规则、每个命令改什么、未决问题）在 [docs/design.md](docs/design.md)；跨组件的规则在装配项目 be-assembly-standard 的协议 04（预占分支，本组件是参与方，与 `erp/inventory` 同形）、协议 05（应收占用的转换，以后）、安全 07（法人维度）。

## 代码地图

| 位置 | 内容 |
| --- | --- |
| `credit/definition.go` | 组件定义：身份、依赖、流与事件类型、资源与操作、法人维度、暂存期限 |
| `credit/module.go` | 路由与系统面的注册、本组件的错误原因、金额的校验、取法人、客户资格与币种（事务外） |
| `credit/limits.go` | 政策与额度的读取、设置、修改，用户面的列表 |
| `credit/branches.go` | 预占分支：`Hold`、`Confirm`、`Cancel`、`Query`、`Consume`，释放（`release`），加锁顺序（`lockBranch`），暂存到期的工作项，`Available` |
| `credit/records.go` | 记录的类型、读取与事件 |
| `credit/migrations/` | 表 |
| `contract/` | 本组件发布的契约，单独的模块：`contract/proto/`、`contract/events/`、`contract/openapi.yaml` |
| `docs/` | 设计 |

| 想改 | 从这里开始 | 然后 |
| --- | --- | --- |
| 预占、拒绝原因、没有额度时的政策 | `docs/design.md` 的“每个命令改什么” | `branches.go` 的 `Hold`、集成测试 |
| 应收占用的转换（协议 05） | `docs/design.md` 的实现状态 | `Consume` 与新的订阅（已出账） |
| 一个维度、一个操作 | `definition.go` | `BRICKKIT.md` 的契约索引 |
| 加字段、加接口、改事件 | 装配项目 be-assembly-standard 规范里的“改动配方” | `docs/design.md` 的数据与命令 |

## 构建与测试

- `go test ./...`（`make test`）：清单的 `configSchema` 与 `events`、OpenAPI 与路由、事件载荷、合并安全。
- `go test -tags integration ./credit/`（`make test-integration`）：真实的 PostgreSQL 与消息系统（`BE_SDK_TEST_PG_ADMIN`、`BE_SDK_TEST_NATS_URL`，同 `erp/inventory`）；`mdm/org`、`mdm/partner`、`mdm/currency` 是按契约写的替身，测试以 `erp/sales` 的身份调用系统面。
- 集成测试的前提：`make up` 起了 `be-postgres` 与 `be-nats`，库 `brickkit_test_db` 存在；schema `erp_credit_it` 每次重建；端口 28088、9088 与替身的 19099、19100、19101 要空着。
- `brickkit lint`

## 设计取舍

- **额度行是唯一的裁决点**：每个分支只涉及一个额度行，所有命令先锁额度行、再锁分支行（`lockBranch`），新分支先插入分支行再锁额度行（别人在它提交前看不到这个分支），所以不死锁。
- **不受控制也参加**：装了信用，销售的确认总有信用分支；不受控制的客户用空额度表达，占用照记，以后设了额度立即生效。
- **超限是事实，不是状态**：每次因超限拒绝发一条 `erp.credit_limit.exceeded.v1`，事实身份是分支身份。

## 易错点

- 不要在事务里调主数据：先取（`currencyScale`、`customerEligible`、`legalEntityActive`），再开事务。
- 金额只在 SQL 里比较与加减；额度、占用的列是 `NUMERIC(19,4)`，金额按币种的小数位校验。

## 改代码前自查

- 改了契约（`contract/`）：proto 改后 `make -C contract gen`，发布 `contract/v…`，再改这里的依赖；OpenAPI 与路由由测试核对。
- 改了版本号吗？每次改动都升版本。

<!-- brickkit:managed:begin lang=zh -->
## BrickKit

这是一个 BrickKit 组件：平台只读 `component.yaml`。它依赖的规则：

- `configSchema` 的键就是代码读的环境变量名。不能用保留名：`COMPONENT_ID`、`COMPONENT_VERSION`、`PORT`、`BRICKKIT_SERVED_MEMBERS`、`BRICKKIT_SERVED_MEMBERS_CONFIG`，以及任何 `*_ENDPOINT`。
- 依赖写精确版本。依赖的地址以 `<ID>_ENDPOINT` 注入；缺席的可选依赖根本没有这个变量，读的时候要带兜底。
- `/healthz` 只查本进程，不查依赖。迁移命令用同一个镜像跑，遇到不认识的参数必须直接失败；`local.migrationCommand` 从源码跑同一条迁移，给 `mode: local` 用。
- `BRICKKIT.md` 会随版本进入每个使用它的项目，在那里是脱离仓库单独读的：跟代码一起改，不放相对链接。
- 发版：改 `metadata.version`，提交、推送，`brickkit release`。`brickkit lint` 会检查清单和这些文档——在项目里、在这个目录下跑，只查这个组件（`--all` 查整个项目）。
- 完整规则在 `brickkit-component` 技能里（装了技能的项目或仓库根目录下的 `.claude/skills/brickkit-component/SKILL.md`；`brickkit skills update` 会装上）；参数问 `brickkit <命令> --help`；BrickKit 自己的文档用 `brickkit docs` 看。
<!-- brickkit:managed:end -->
