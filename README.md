# erp-credit

组件 `erp/credit`：信用政策、额度与占用、信用预占分支。使用说明见 `BRICKKIT.md`，修改说明见 `AGENTS.md`，设计见 `docs/design.md`。

新身份（D03），没有旧版本；按终局设计实现，版本从 `3.0.0` 起（A110）。

## 在项目里使用

在装配项目里 `brickkit add erp/credit@<版本>`，再按 `BRICKKIT.md` 的“部署前准备”配置。

## 文档

| 问题 | 看哪里 |
| --- | --- |
| 它做什么、怎样部署与配置 | `BRICKKIT.md` |
| 为什么这样设计：术语、数据、规则、未决问题 | `docs/design.md` |
| 怎样改它 | `AGENTS.md` |
| 契约 | `contract/README.md` |

## 开发

见 `AGENTS.md` 的“构建与测试”。
