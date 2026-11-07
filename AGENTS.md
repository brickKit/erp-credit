# erp/credit

信用组件的代码。用法与边界在 `BRICKKIT.md`，为什么这样设计（术语、数据、规则、每个命令改什么、未决问题）在 [docs/design.md](docs/design.md)；跨组件的规则在装配项目 be-assembly-standard 的协议 04（预占分支，本组件是参与方，与 `erp/inventory` 同形）、协议 05（应收占用的转换，以后）、安全 07（法人维度）。

## 代码地图

代码按业务模块分层。**先读这一节，再进目录**：每个代码目录都有自己的 `AGENTS.md`（同目录的 `CLAUDE.md` 让 Claude Code 读这个目录的文件时自动带上它），写这一层负责什么、不负责什么、每个文件、依赖谁、测在哪。

| 目录 | 是什么 |
| --- | --- |
| [`credit/`](credit/AGENTS.md) | 入口：组件定义与组装（路由、系统面、后台工作、恢复核对都在这里登记，转给业务模块）；不写业务。`main.go` 与外壳只引用它 |
| [`internal/`](internal/AGENTS.md) | 业务：下面的模块各一个包；模块之间的依赖表在它的 `AGENTS.md` |
| [`itest/`](itest/AGENTS.md) | 集成测试，按业务流程分文件；它的 `AGENTS.md` 是测试索引 |
| `credit/migrations/` | 表 |
| `cmd/seed/` | 种子：角色与授予、三个法人的政策、客户的额度（大、中、小、不受控制、没有额度、暂停的反例）（`make seed`） |
| `contract/` | 本组件发布的契约，单独的模块：`contract/proto/`、`contract/events/`、`contract/openapi.yaml` |
| `docs/` | 设计 |

业务模块（`internal/` 下）：

| 模块 | 负责 | 拥有的表 |
| --- | --- | --- |
| [`limits`](internal/limits/AGENTS.md) | 法人的政策与客户的额度：读取、设置、修改、列表 | `credit_policy`、`credit_limit` |
| [`branches`](internal/branches/AGENTS.md) | 预占分支（协议 04，本组件是参与方；状态机在 SDK 的 `reserve`）：预占、确认、取消、查询、消耗、暂存到期；可用额度 | `branch`、`consumption`（分支的状态在 SDK 的 `be_reserve_branch`） |

共用的：[`masterdata`](internal/masterdata/AGENTS.md)（事务外向 `mdm/org`、`mdm/partner`、`mdm/currency` 取证）、[`core`](internal/core/AGENTS.md)（名字与资源定义、校验、审计与事件的写法、记录的类型与读取）。

| 想改 | 从这里开始 | 然后 |
| --- | --- | --- |
| 预占、拒绝原因、没有额度时的政策 | `docs/design.md` 的“每个命令改什么” | `internal/branches` 的 `Hold`；`itest/branches_integration_test.go`、`itest/policies_integration_test.go` |
| 应收占用的转换（协议 05） | `docs/design.md` 的实现状态 | `internal/branches` 的 `Consume` 与新的订阅（已出账） |
| 一个维度、一个操作 | `internal/core/names.go` | `BRICKKIT.md` 的契约索引；路由的登记在 `credit/module.go` |
| 加字段、加接口、改事件 | 装配项目 be-assembly-standard 规范里的“改动配方” | `docs/design.md` 的数据与命令 |

## 构建与测试

- `go test ./...`（`make test`）：清单的 `configSchema` 与 `events`、OpenAPI 与路由、事件载荷、合并安全、结构检查（`credit/structure_test.go`）。
- `make test-integration`（`go test -tags integration ./itest/`）：真实的 PostgreSQL 与消息系统；每个文件测哪条流程、要什么前提，在 [itest/AGENTS.md](itest/AGENTS.md)。
- `brickkit lint`

## 设计取舍

- **额度行是唯一的裁决点**：每个分支只涉及一个额度行，所有命令先锁额度行、再锁分支行（SDK 的 `reserve`，线协议 P16.11），新分支先写下分支再锁额度行（别人在它提交前看不到这个分支），所以不死锁。
- **分支的状态机在 SDK**：协议 04 参与方的规则只实现一次（be-sdk-go 的 `reserve`），本组件只写占用、退回与消耗；订单占用只经 `limits` 的三个函数写。
- **不受控制也参加**：装了信用，销售的确认总有信用分支；不受控制的客户用空额度表达，占用照记，以后设了额度立即生效。
- **超限是事实，不是状态**：每次因超限拒绝发一条 `erp.credit_limit.exceeded.v1`，事实身份是分支身份。

## 易错点

- 不要在事务里调主数据：先取（`internal/masterdata`），再开事务。
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
