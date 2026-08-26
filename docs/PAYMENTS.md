# 支付与记账

支付模块负责充值订单、到账、退款和现金记账。支付渠道适配器只处理商户接口；用户余额、订单状态和账本由后端支付服务统一写入。

## 金额单位

系统同时使用两种单位：

- `amount_minor`：支付币种的最小单位。CNY、USD 等两位小数币种以分或美分计；零位小数币种以整数计。
- `credit_micro`：账户余额单位，`1,000,000` 表示 `1 USD` 额度。

`credit_micro_per_unit` 定义每 1 个支付币种单位换得多少微美元额度。以 CNY 为例：

```text
credit_micro = amount_minor × credit_micro_per_unit ÷ 100
```

若要按 `¥1 = $1` 额度充值，设置 `credit_micro_per_unit = 1000000`。启用支付前，该值必须大于 0。

## 全局配置

管理端支付配置包含：

| 字段 | 作用 | 约束 |
| --- | --- | --- |
| `enabled` | 开放用户充值 | 同时要求有效兑换率和 `site.public_url` |
| `currency` | 收款币种 | 所选渠道实例必须使用相同币种 |
| `credit_micro_per_unit` | 每币种单位兑换的余额 | 必须大于 0 |
| `min_amount_minor` | 单笔最小金额 | 必须大于 0 |
| `max_amount_minor` | 单笔最大金额 | 不得小于最小金额 |
| `daily_limit_minor` | 单用户每日创建与支付限额 | `0` 表示不限制；待支付订单也占用当日额度 |
| `order_expiry_minutes` | 订单有效期 | 5–1440 分钟 |
| `max_pending_orders` | 单用户最多待支付订单 | 1–50 |
| `load_balance_strategy` | 多商户实例选择方式 | `round_robin` 或 `least_amount` |
| `product_name` | 支付平台显示的商品名称 | 由渠道能力决定最终显示方式 |

`round_robin` 按优先级、上次选择时间和实例 ID 选择；`least_amount` 在可用实例中选择当日已到账金额较少者。

## 支付渠道

可配置多个同类商户实例。实例的币种、支付方式、单笔范围、日限额、优先级和启用状态参与下单筛选。渠道配置以加密密文保存，列表和详情接口不返回商户秘密。

| 渠道 | `provider_key` | 默认支付方式 | 必填配置 |
| --- | --- | --- | --- |
| 易支付 | `easypay` | `alipay`、`wxpay` | `pid`、`pkey`、`apiBase` |
| 支付宝开放平台 | `alipay` | `alipay` | `appId`、`privateKey`、`alipayPublicKey` 或 `publicKey` |
| 微信支付 API v3 | `wxpay` | `wxpay` | `appId`、`mchId`、`serialNo`、`privateKey`、`platformCert` 或 `platformPublicKey`、32 字节 `apiV3Key` |
| Stripe | `stripe` | `card`、`link` | `secretKey`、`publishableKey`、`webhookSecret` |
| Airwallex | `airwallex` | `card`、`link` | `clientId`、`apiKey`、`webhookSecret`、HTTPS `/api/v1` `apiBase` |

可选字段：

- 易支付：`currency`、`paymentMode`；`apiBase` 必须使用 HTTPS。
- 支付宝：`gateway`、`paymentMode`；默认使用正式网关，`paymentMode` 为 `redirect` 或 `wap` 时走网页支付，否则走预下单二维码。
- 微信支付：`apiBase`、`paymentMode`、`platformPublicKeyId`；也兼容 `publicKeyId` 和 `platformSerialNo`。`paymentMode=h5` 时必须能取得真实客户端 IP，否则使用 Native 二维码。
- Airwallex：`accountId`、`countryCode`；`countryCode` 默认为 `CN`。

`supported_methods` 留空时使用表中默认值；显式填写时以逗号、分号或空格分隔。实例币种必须与全局支付币种相同，才会出现在用户充值页。

## 回调与公开地址

支付平台回调地址为：

```text
https://relay.example.com/api/payment/webhook/{provider_key}
```

支付完成返回地址为：

```text
https://relay.example.com/wallet
```

两者都从 `site.public_url` 生成。生产环境应填写用户实际访问的 HTTPS Origin，不能包含额外路径。反向代理必须允许公网访问回调地址，并保持原始请求体和签名请求头不变。

回调请求体上限为 1 MiB。每个适配器先验证渠道签名，再由支付服务核对商户实例、订单号、金额和币种。任何一项不一致都不会增加用户余额。

## 订单状态

```text
PENDING ──支付成功──> PAID ──余额与账本提交──> COMPLETED
   │
   ├─到期──────────> EXPIRED
   ├─用户取消──────> CANCELLED
   └─渠道拒绝──────> FAILED

COMPLETED ──用户申请──> REFUND_REQUESTED
            ──管理员处理──> REFUNDING ──渠道完成──> REFUNDED
```

- 余额增加、订单完成和收入账本在同一数据库事务中提交。
- 重复回调通过订单状态条件和唯一账本事件键保持幂等。
- 已过期、已取消或曾失败的订单收到有效成功回调后仍可结算；已退款和已完成订单不会重复入账。
- 后台每 15 秒查询最多 100 个未结订单。单次渠道查询超时为 10 秒。
- 过期订单在 24 小时宽限期内仍会被自动核验；渠道查询失败保持当前状态，留待下一轮处理。

## 人工核验

用户订单页的“核验到账”会查询订单创建时使用的商户实例。它只处理 `PENDING`，以及过期不超过 24 小时的 `EXPIRED` 订单。

核验没有到账时依次检查：

1. 订单是否保存了渠道交易号；
2. 商户实例是否仍可解密并连接支付平台；
3. 渠道查询接口返回的订单号、金额和币种是否与本地订单一致；
4. `site.public_url` 与支付平台回调地址是否一致；
5. 反向代理和防火墙是否放行回调；
6. 订单审计记录中是否有签名失败、金额不符或渠道拒绝。

不要通过手工修改订单状态补账。确需人工调整用户余额时，应在用户余额账本中单独记录原因，不应伪造支付到账。

## 退款

只有 `COMPLETED` 订单可以提交退款申请。管理员执行退款时，系统先冻结该订单原先增加的额度，再调用支付平台：

- 渠道同步拒绝：解除冻结，订单恢复 `COMPLETED`；
- 渠道同步完成：订单改为 `REFUNDED`，写入支出账本；
- 渠道异步处理：保持 `REFUNDING`，管理员通过退款查询完成对账。

若用户当前余额低于该订单曾增加的额度，退款不会开始。部分退款当前未开放；`refunded_micro` 记录整笔退款对应的额度。

## 记账本与对账

订单表记录当前工作流状态，`payment_ledger_entries` 记录已经发生的现金事件。充值完成写入 `income/recharge`，退款完成写入 `expense/refund`；推广现金提现使用独立的 `expense/referral_payout` 分类。

对账时以不可变账本为累计口径，以订单和渠道流水核对状态。页面中的 7、30、90 天结果是滚动时间窗口，不等于历史累计收入。

每日建议核对：

- 渠道实收与收入账本；
- 渠道退款与支出账本；
- `COMPLETED` 订单是否均有收入记录；
- `REFUNDED` 订单是否均有退款支出记录；
- 长时间停留在 `PENDING`、`PAID` 或 `REFUNDING` 的订单。

## 上线检查

1. 固定并备份 `ENCRYPTION_KEY`，确认历史商户配置可解密。
2. 设置正确的 `site.public_url`，从公网验证 HTTPS 与回调路由。
3. 先保存停用状态的商户实例，检查币种、金额限制和支付方式。
4. 使用渠道沙箱或最小金额完成创建、扫码或收银台、回调、自动到账和人工核验。
5. 重放同一回调，确认余额和收入账本没有重复增加。
6. 验证取消、过期、延迟到账和退款路径。
7. 完成渠道后台与本地账本对账后再向用户开放。

支付配置包含真实商户凭证。不要把管理端导出、数据库、环境文件或故障日志提交到 Git；完整安全边界见 [安全说明](../SECURITY.md)。推广佣金和微信商家转账见 [推广佣金与现金提现](REFERRAL_CASH_PAYOUT.md)。
