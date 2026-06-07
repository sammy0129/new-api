# 虎皮椒支付 XunhuPay 对接 new-api 开发 PRD

## 1. 背景与目标

为 `new-api` 增加虎皮椒支付能力，支持用户在钱包充值页通过虎皮椒创建支付订单，支付成功后由虎皮椒异步回调完成额度充值；同时预留订阅套餐购买、查单、退款能力。

参考资料：

- 本地 skill：`xunhupay/SKILL.md`
- API 参数参考：`xunhupay/reference.md`
- 现有普通充值：`new-api/controller/topup.go`
- 现有订阅支付：`new-api/controller/subscription_payment_epay.go`
- 支付模型：`new-api/model/topup.go`
- 路由：`new-api/router/api-router.go`
- 默认前端钱包页：`new-api/web/default/src/features/wallet`
- 默认系统支付设置页：`new-api/web/default/src/features/system-settings/integrations/payment-settings-section.tsx`

## 2. 对接范围

### P0 必做

- 后台支付设置新增虎皮椒配置。
- 钱包充值页展示虎皮椒支付入口。
- 用户创建虎皮椒充值订单。
- 后端调用虎皮椒 `/payment/do.html` 获取支付链接和二维码链接。
- 虎皮椒异步回调验签、校验订单、幂等入账。
- 用户充值记录、管理员充值记录正常展示。
- 支付合规确认未完成时不可启用虎皮椒支付。

### P1 可选

- 订阅套餐通过虎皮椒购买。
- 管理后台查单。
- 管理后台全额退款。
- 支付二维码弹窗展示，而不是只打开跳转链接。

## 3. 产品规则

- 虎皮椒支付渠道由 `APPID` 绑定决定，前端不再区分微信/支付宝。
- P0 展示名称固定为“虎皮椒支付”。如需同时展示微信、支付宝等多个入口，P1 扩展为多 APPID 配置数组。
- `notify_url` 必须公网可访问，不允许使用 localhost。
- 支付成功以异步回调为准，前端跳转返回只作为用户体验，不直接入账。
- 回调必须先验签，再校验订单号、金额、支付 provider、订单状态。
- 同一订单多次回调只允许入账一次。
- 退款仅支持全额退款。
- 虎皮椒网关域名以运营方后台显示为准，不固定假设 `api.xunhupay.com`。

## 4. 后端需求

### 4.1 配置项

新增配置文件：

- `new-api/setting/payment_xunhupay.go`

新增配置项：

| 配置项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `XunhuPayEnabled` | bool | `false` | 是否启用虎皮椒支付 |
| `XunhuPayGateway` | string | `https://api.xunhupay.com` | 虎皮椒网关地址，可由后台覆盖 |
| `XunhuPayAppID` | string | 空 | 虎皮椒 APPID |
| `XunhuPaySecret` | string | 空 | 虎皮椒 SECRET |
| `XunhuPayMinTopUp` | int | `1` | 最低充值数量 |
| `XunhuPayNotifyUrl` | string | 空 | 可选自定义回调地址 |
| `XunhuPayReturnUrl` | string | 空 | 可选自定义同步跳转地址 |

配置需要接入：

- `model/option.go` 的默认值加载与更新分支。
- `web/default/src/features/system-settings/types.ts` 的类型定义。
- `web/default/src/features/system-settings/billing/index.tsx` 的默认值。
- `web/default/src/features/system-settings/billing/section-registry.tsx` 的传参。

### 4.2 模型常量

在 `new-api/model/topup.go` 增加：

```go
const (
    PaymentMethodXunhuPay = "xunhupay"
)

const (
    PaymentProviderXunhuPay = "xunhupay"
)
```

说明：

- `PaymentMethod` 用于充值记录展示和日志。
- `PaymentProvider` 用于回调防串单校验。
- 回调入账必须校验 `topUp.PaymentProvider == model.PaymentProviderXunhuPay`。

### 4.3 路由

新增 P0 路由：

| Method | Path | Auth | Controller | 说明 |
| --- | --- | --- | --- | --- |
| `POST` | `/api/user/xunhupay/pay` | UserAuth | `RequestXunhuPay` | 创建虎皮椒充值订单 |
| `POST` | `/api/xunhupay/notify` | None | `XunhuPayNotify` | 虎皮椒异步回调 |

新增 P1 路由：

| Method | Path | Auth | Controller | 说明 |
| --- | --- | --- | --- | --- |
| `POST` | `/api/subscription/xunhupay/pay` | UserAuth | `SubscriptionRequestXunhuPay` | 创建虎皮椒订阅订单 |
| `POST` | `/api/subscription/xunhupay/notify` | None | `SubscriptionXunhuPayNotify` | 虎皮椒订阅回调 |

### 4.4 支付启用判断

在 `controller/payment_webhook_availability.go` 增加：

```go
func isXunhuPayTopUpEnabled() bool {
    if !isPaymentComplianceConfirmed() {
        return false
    }
    return setting.XunhuPayEnabled &&
        strings.TrimSpace(setting.XunhuPayGateway) != "" &&
        strings.TrimSpace(setting.XunhuPayAppID) != "" &&
        strings.TrimSpace(setting.XunhuPaySecret) != ""
}

func isXunhuPayWebhookEnabled() bool {
    return isXunhuPayTopUpEnabled()
}
```

### 4.5 `GetTopUpInfo` 扩展

在 `controller/topup.go` 的 `GetTopUpInfo` 中：

- 增加 `enable_xunhupay_topup`
- 增加 `xunhupay_min_topup`
- 在 `pay_methods` 中追加虎皮椒支付方式：

```json
{
  "name": "虎皮椒支付",
  "type": "xunhupay",
  "color": "rgba(var(--semi-green-5), 1)",
  "min_topup": "1"
}
```

### 4.6 签名算法

新增 helper，建议放在 `controller/topup_xunhupay.go` 或独立 service 文件。

规则：

1. 过滤空字符串、nil，并排除 `hash` 字段。
2. 按参数名 ASCII 字典序升序排列。
3. 拼接 `key=value&key=value`。
4. 字符串末尾直接追加 `secret`，不加分隔符。
5. MD5，输出小写 32 位十六进制。

Go 示例：

```go
func makeXunhuPaySign(params map[string]string, secret string) string {
    keys := make([]string, 0, len(params))
    for k, v := range params {
        if k == "hash" || v == "" {
            continue
        }
        keys = append(keys, k)
    }
    sort.Strings(keys)

    parts := make([]string, 0, len(keys))
    for _, k := range keys {
        parts = append(parts, k+"="+params[k])
    }

    raw := strings.Join(parts, "&") + secret
    sum := md5.Sum([]byte(raw))
    return fmt.Sprintf("%x", sum)
}
```

### 4.7 创建充值订单

接口：

```http
POST /api/user/xunhupay/pay
```

请求：

```json
{
  "amount": 100
}
```

成功响应：

```json
{
  "message": "success",
  "data": {
    "payment_url": "https://...",
    "qrcode_url": "https://...",
    "trade_no": "USR1NOxxxx",
    "open_order_id": "HPJ..."
  }
}
```

流程：

1. 校验 `isXunhuPayTopUpEnabled()`。
2. 校验 `amount >= XunhuPayMinTopUp`。
3. 获取用户分组，复用 `getPayMoney(amount, group)` 计算实际支付金额。
4. 生成 `tradeNo`，建议格式：`XHPUSR{id}NO{random}{timestamp}`。
5. 创建 `model.TopUp`：
   - `UserId`: 当前用户
   - `Amount`: 充值数量
   - `Money`: 实际支付金额
   - `TradeNo`: `tradeNo`
   - `PaymentMethod`: `model.PaymentMethodXunhuPay`
   - `PaymentProvider`: `model.PaymentProviderXunhuPay`
   - `Status`: `common.TopUpStatusPending`
6. 组装虎皮椒请求参数：
   - `version`: `1.1`
   - `appid`: `XunhuPayAppID`
   - `trade_order_id`: `tradeNo`
   - `total_fee`: `payMoney`，单位元，保留两位小数
   - `title`: `TUC{amount}`
   - `notify_url`: 自定义地址或 `{callback}/api/xunhupay/notify`
   - `return_url`: 自定义地址或 `/console/topup?show_history=true`
   - `time`: 当前 Unix 秒
   - `nonce_str`: 32 位随机字符串
   - `hash`: 签名
7. `POST JSON` 到 `{gateway}/payment/do.html`。
8. `errcode != 0` 时记录日志并将订单标记为 `failed`。
9. 成功时返回 `data.url`、`data.url_qrcode`、`data.open_order_id`。

### 4.8 回调处理

接口：

```http
POST /api/xunhupay/notify
Content-Type: application/x-www-form-urlencoded
```

虎皮椒回调关键参数：

| 参数 | 说明 |
| --- | --- |
| `trade_order_id` | 商户订单号 |
| `total_fee` | 实际支付金额，单位元 |
| `transaction_id` | 交易单号 |
| `open_order_id` | 虎皮椒平台订单号 |
| `status` | `OD` 表示支付成功 |
| `appid` | 支付渠道 ID |
| `time` | 时间戳 |
| `nonce_str` | 随机字符串 |
| `hash` | 签名 |

处理流程：

1. 校验 `isXunhuPayWebhookEnabled()`，未启用返回 `fail`。
2. 调用 `ParseForm()`，读取 `PostForm` 为 `map[string]string`。
3. 使用 `XunhuPaySecret` 验签，失败返回 `fail`。
4. 若 `status != "OD"`，记录日志并返回 `success`。
5. 读取 `trade_order_id`，按订单号调用 `LockOrder/UnlockOrder`。
6. 查询 `TopUp`。
7. 校验：
   - 订单存在。
   - `PaymentProvider == xunhupay`。
   - `Status == pending`；若已成功，直接返回 `success`。
   - `total_fee` 与 `TopUp.Money` 一致，允许最大 `0.01` 误差。
8. 更新订单：
   - `Status = success`
   - `CompleteTime = common.GetTimestamp()`
9. 增加用户额度：
   - `quotaToAdd = topUp.Amount * common.QuotaPerUnit`
10. 记录充值日志：
   - `RecordTopupLog(userId, ..., topUp.PaymentMethod, model.PaymentProviderXunhuPay)`
11. 返回纯文本 `success`。

异常处理：

- 签名失败：返回 `fail`。
- 金额不一致：返回 `fail`，不入账。
- 订单不存在：建议返回 `success`，避免虎皮椒持续重试造成噪音；同时记录 warning。
- 订单已成功：返回 `success`，不重复入账。
- provider 不匹配：返回 `fail`，记录安全日志。

## 5. 前端需求

### 5.1 系统设置页

位置：

- `web/default/src/features/system-settings/integrations/payment-settings-section.tsx`

新增配置区块：`XunhuPay`

字段：

| 字段 | 控件 | 校验 |
| --- | --- | --- |
| 启用虎皮椒支付 | Switch | 合规未确认时禁用 |
| 网关地址 | Input | 必须以 `http://` 或 `https://` 开头 |
| APPID | Input | 启用时必填 |
| SECRET | Password Input | 启用时必填 |
| 最低充值 | Number Input | `>= 1` |
| Notify URL | Input | 可空，非空时必须为 URL |
| Return URL | Input | 可空，非空时必须为 URL |

说明文案：

- “网关地址以虎皮椒后台「我的支付渠道」显示为准。”
- “Notify URL 必须公网可访问。”
- “回调地址默认：`<ServerAddress>/api/xunhupay/notify`。”

### 5.2 钱包页

位置：

- `web/default/src/features/wallet/api.ts`
- `web/default/src/features/wallet/types.ts`
- `web/default/src/features/wallet/lib/payment.ts`
- `web/default/src/features/wallet/index.tsx`
- `web/default/src/features/wallet/components/recharge-form-card.tsx`

类型扩展：

```ts
export type XunhuPayPaymentResponse = ApiResponse<{
  payment_url?: string
  qrcode_url?: string
  trade_no?: string
  open_order_id?: string
}>

export interface TopupInfo {
  enable_xunhupay_topup?: boolean
  xunhupay_min_topup?: number
}
```

API：

```ts
export async function requestXunhuPayPayment(
  request: { amount: number }
): Promise<XunhuPayPaymentResponse> {
  const res = await api.post('/api/user/xunhupay/pay', request, {
    skipBusinessError: true,
  } as Record<string, unknown>)
  return res.data
}
```

交互：

- 充值方式列表展示“虎皮椒支付”。
- 用户点击后打开确认弹窗。
- 确认后调用 `/api/user/xunhupay/pay`。
- P0：拿到 `payment_url` 后 `window.open(payment_url, '_blank')`。
- P1：拿到 `qrcode_url` 后弹窗展示二维码，同时提供“打开支付页面”和“已支付，刷新余额”按钮。

## 6. 订阅购买 P1

新增 `SubscriptionRequestXunhuPay`：

- 输入：`{ "plan_id": 1 }`
- 校验套餐启用、价格大于 0、用户购买限制。
- 创建 `SubscriptionOrder`：
  - `PaymentMethod = xunhupay`
  - `PaymentProvider = xunhupay`
  - `Status = pending`
- 调用虎皮椒创建支付订单。
- 回调成功后调用：

```go
model.CompleteSubscriptionOrder(tradeNo, payloadJson, model.PaymentProviderXunhuPay, model.PaymentMethodXunhuPay)
```

前端订阅购买弹窗增加“虎皮椒支付”按钮。

## 7. 查单与退款 P1

### 查单

后端封装虎皮椒 `/payment/query.html`：

- 支持 `trade_order_id` 或 `open_order_id`。
- 返回状态：
  - `OD`: 已支付
  - `WP`: 待付款
  - `CD`: 已取消

用途：

- 管理员排障。
- 用户支付后手动刷新订单状态。

### 退款

后端封装虎皮椒 `/payment/refund.html`：

- 仅支持全额退款。
- 仅允许管理员操作。
- 退款前校验订单已支付、provider 为 `xunhupay`。
- 退款成功后记录日志。
- 是否扣减用户额度需单独产品决策，P1 不默认自动扣减。

## 8. 验收标准

### 功能验收

- 管理员完整配置虎皮椒后，钱包页出现“虎皮椒支付”。
- 用户点击充值，后端能成功创建本地 `TopUp` 订单并返回虎皮椒支付链接。
- 虎皮椒回调签名正确且金额一致时，订单从 `pending` 变为 `success`，用户额度增加。
- 重复回调不会重复加额度。
- 签名错误、金额不一致、provider 不匹配、订单不存在时不入账。
- 未完成支付合规确认时，前端不展示虎皮椒支付，后端创建订单接口也拒绝。
- 回调成功处理后返回纯文本 `success`。
- 用户和管理员充值记录能看到虎皮椒订单。

### 测试验收

后端测试：

- 签名算法测试。
- 创建订单参数测试。
- 回调验签失败测试。
- 回调金额不一致测试。
- 回调 provider mismatch 测试。
- 重复回调幂等测试。
- 成功回调加额度测试。

前端测试：

- 系统设置表单校验。
- `TopupInfo` 解析虎皮椒字段。
- 钱包页展示虎皮椒支付方式。
- 支付成功返回 `payment_url` 后正确打开。

建议执行：

```sh
go test ./controller ./model
corepack pnpm@9.15.9 typecheck
```

## 9. 开发拆分

### 后端

- 新增虎皮椒 setting 与 option 映射。
- 新增签名、nonce、金额格式化、HTTP client helper。
- 新增充值创建 controller。
- 新增 notify controller。
- 扩展 `GetTopUpInfo` 和支付启用判断。
- 扩展 `TopUp` 支付常量。
- 增加单元测试。

### 前端

- 扩展系统设置类型与默认值。
- `PaymentSettingsSection` 增加虎皮椒配置区。
- wallet API/types 增加 xunhupay。
- wallet 支付分发逻辑增加 xunhupay。
- 充值确认弹窗展示虎皮椒支付方式。
- i18n 增加中英文文案。

## 10. 风险与注意事项

- 虎皮椒网关域名不固定，必须允许后台配置。
- 回调是 `application/x-www-form-urlencoded`，不是 JSON。
- 签名拼接末尾直接追加 secret，不加 `&key=`。
- 不能将 `hash` 参与签名。
- 空字符串和 nil 参数不参与签名。
- `notify_url` 必须公网可访问。
- 虎皮椒只支持全额退款，P1 退款功能不能做部分退款入口。
- 前端跳转成功不代表支付成功，必须等待后端异步回调入账。

