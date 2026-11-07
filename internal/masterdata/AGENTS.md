# masterdata：向主数据取证（共用）

向 `mdm/org`、`mdm/partner`、`mdm/currency` 取证。每个调用都在事务之外：先取证，再开事务；不可达不当作“不存在”。

## 负责与不负责

- 负责：法人能不能用（`LegalEntityActive`）、客户在这个法人下的资格（`CustomerEligible`）、币种的小数位（`CurrencyScale`）。
- 不负责：额度与政策（`limits`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `masterdata.go` | `LegalEntityActive`、`CustomerEligible`、`CurrencyScale` |

## 依赖与被依赖

- 依赖：`core`。
- 被依赖：`limits`。

## 规则在哪

装配项目协议 07（资格取证）、协议 14（币种小数位）。

## 测试

由 `itest/limits_integration_test.go`（不是客户、币种不可用、日元没有小数）间接覆盖；替身在 `itest/endtoend_integration_test.go`。
