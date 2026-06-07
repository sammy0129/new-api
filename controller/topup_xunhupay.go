package controller

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

const xunhuPaySuccessStatus = "OD"

type XunhuPayRequest struct {
	Amount int64 `json:"amount"`
}

type xunhuPayCreatePaymentResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
	Hash    string `json:"hash"`
	Data    struct {
		URL         string `json:"url"`
		URLQRCode   string `json:"url_qrcode"`
		OpenOrderID string `json:"open_order_id"`
	} `json:"data"`
}

var createXunhuPayPayment = requestXunhuPayPayment

func makeXunhuPaySign(params map[string]string, secret string) string {
	keys := make([]string, 0, len(params))
	for key, value := range params {
		if key == "hash" || value == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+params[key])
	}

	sum := md5.Sum([]byte(strings.Join(parts, "&") + secret))
	return fmt.Sprintf("%x", sum)
}

func verifyXunhuPaySign(params map[string]string, secret string) bool {
	received := strings.TrimSpace(params["hash"])
	if received == "" {
		return false
	}
	return strings.EqualFold(received, makeXunhuPaySign(params, secret))
}

func formatXunhuPayAmount(amount float64) string {
	return decimal.NewFromFloat(amount).Round(2).StringFixed(2)
}

func isSameXunhuPayAmount(callbackTotalFee string, orderMoney float64) bool {
	callbackAmount, err := decimal.NewFromString(strings.TrimSpace(callbackTotalFee))
	if err != nil {
		return false
	}
	orderAmount, err := decimal.NewFromString(formatXunhuPayAmount(orderMoney))
	if err != nil {
		return false
	}
	return callbackAmount.Equal(orderAmount)
}

func getXunhuPayMinTopup() int64 {
	minTopup := setting.XunhuPayMinTopUp
	if minTopup <= 0 {
		minTopup = 1
	}
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		dMinTopup := decimal.NewFromInt(int64(minTopup))
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		minTopup = int(dMinTopup.Mul(dQuotaPerUnit).IntPart())
	}
	return int64(minTopup)
}

func buildXunhuPayTradeNo(userID int) string {
	return fmt.Sprintf("XHPUSR%dNO%s%d", userID, common.GetRandomString(8), time.Now().Unix())
}

func xunhuPayGatewayURL(path string) string {
	return strings.TrimRight(strings.TrimSpace(setting.XunhuPayGateway), "/") + path
}

func xunhuPayNotifyURL() string {
	if strings.TrimSpace(setting.XunhuPayNotifyUrl) != "" {
		return strings.TrimSpace(setting.XunhuPayNotifyUrl)
	}
	return strings.TrimRight(service.GetCallbackAddress(), "/") + "/api/xunhupay/notify"
}

func xunhuPayReturnURL() string {
	if strings.TrimSpace(setting.XunhuPayReturnUrl) != "" {
		return strings.TrimSpace(setting.XunhuPayReturnUrl)
	}
	return paymentReturnPath("/console/topup?show_history=true")
}

func isValidXunhuPayNotifyURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return false
	}
	return true
}

func buildXunhuPayPaymentParams(tradeNo string, amount int64, payMoney float64) map[string]string {
	params := map[string]string{
		"version":        "1.1",
		"appid":          strings.TrimSpace(setting.XunhuPayAppID),
		"trade_order_id": tradeNo,
		"total_fee":      formatXunhuPayAmount(payMoney),
		"title":          fmt.Sprintf("TUC%d", amount),
		"notify_url":     xunhuPayNotifyURL(),
		"return_url":     xunhuPayReturnURL(),
		"time":           strconv.FormatInt(time.Now().Unix(), 10),
		"nonce_str":      common.GetRandomString(32),
	}
	params["hash"] = makeXunhuPaySign(params, strings.TrimSpace(setting.XunhuPaySecret))
	return params
}

func requestXunhuPayPayment(params map[string]string) (*xunhuPayCreatePaymentResponse, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, xunhuPayGatewayURL("/payment/do.html"), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")

	client := service.GetHttpClient()
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("xunhupay status=%d body=%s", resp.StatusCode, string(respBody))
	}

	var result xunhuPayCreatePaymentResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}
	if result.ErrCode != 0 {
		if result.ErrMsg == "" {
			result.ErrMsg = "unknown error"
		}
		return &result, fmt.Errorf("xunhupay errcode=%d errmsg=%s", result.ErrCode, result.ErrMsg)
	}
	if result.Data.URL == "" {
		return &result, errors.New("xunhupay response missing payment url")
	}
	return &result, nil
}

func RequestXunhuPay(c *gin.Context) {
	if !isXunhuPayTopUpEnabled() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "当前管理员未配置虎皮椒支付信息"})
		return
	}

	var req XunhuPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	if req.Amount < getXunhuPayMinTopup() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", getXunhuPayMinTopup())})
		return
	}

	id := c.GetInt("id")
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	payMoney := getPayMoney(req.Amount, group)
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}
	if !isValidXunhuPayNotifyURL(xunhuPayNotifyURL()) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "虎皮椒支付回调地址必须是公网 HTTP/HTTPS 地址"})
		return
	}

	amount := req.Amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		dAmount := decimal.NewFromInt(amount)
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		amount = dAmount.Div(dQuotaPerUnit).IntPart()
	}

	tradeNo := buildXunhuPayTradeNo(id)
	topUp := &model.TopUp{
		UserId:          id,
		Amount:          amount,
		Money:           payMoney,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodXunhuPay,
		PaymentProvider: model.PaymentProviderXunhuPay,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("虎皮椒支付 创建充值订单失败 user_id=%d trade_no=%s amount=%d error=%q", id, tradeNo, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	params := buildXunhuPayPaymentParams(tradeNo, amount, payMoney)
	result, err := createXunhuPayPayment(params)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("虎皮椒支付 拉起支付失败 user_id=%d trade_no=%s amount=%d error=%q", id, tradeNo, req.Amount, err.Error()))
		topUp.Status = common.TopUpStatusFailed
		if updateErr := topUp.Update(); updateErr != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("虎皮椒支付 标记充值订单失败状态失败 trade_no=%s error=%q", tradeNo, updateErr.Error()))
		}
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("虎皮椒支付 充值订单创建成功 user_id=%d trade_no=%s amount=%d money=%.2f open_order_id=%s", id, tradeNo, req.Amount, payMoney, result.Data.OpenOrderID))
	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"payment_url":   result.Data.URL,
			"qrcode_url":    result.Data.URLQRCode,
			"trade_no":      tradeNo,
			"open_order_id": result.Data.OpenOrderID,
		},
	})
}

func parseXunhuPayPostForm(c *gin.Context) (map[string]string, error) {
	if err := c.Request.ParseForm(); err != nil {
		return nil, err
	}
	params := make(map[string]string, len(c.Request.PostForm))
	for key := range c.Request.PostForm {
		params[key] = c.Request.PostForm.Get(key)
	}
	return params, nil
}

func XunhuPayNotify(c *gin.Context) {
	ctx := c.Request.Context()
	if !isXunhuPayWebhookEnabled() {
		logger.LogWarn(ctx, fmt.Sprintf("虎皮椒支付 webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", c.Request.RequestURI, c.ClientIP()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	params, err := parseXunhuPayPostForm(c)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("虎皮椒支付 webhook 表单解析失败 path=%q client_ip=%s error=%q", c.Request.RequestURI, c.ClientIP(), err.Error()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}
	logger.LogInfo(ctx, fmt.Sprintf("虎皮椒支付 webhook 收到请求 path=%q client_ip=%s params=%q", c.Request.RequestURI, c.ClientIP(), common.GetJsonString(params)))

	if len(params) == 0 || !verifyXunhuPaySign(params, strings.TrimSpace(setting.XunhuPaySecret)) {
		logger.LogWarn(ctx, fmt.Sprintf("虎皮椒支付 webhook 验签失败 path=%q client_ip=%s", c.Request.RequestURI, c.ClientIP()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}
	if strings.TrimSpace(params["appid"]) != strings.TrimSpace(setting.XunhuPayAppID) {
		logger.LogWarn(ctx, fmt.Sprintf("虎皮椒支付 webhook appid 不匹配 callback_appid=%s client_ip=%s", params["appid"], c.ClientIP()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	tradeNo := strings.TrimSpace(params["trade_order_id"])
	status := strings.TrimSpace(params["status"])
	if status != xunhuPaySuccessStatus {
		logger.LogInfo(ctx, fmt.Sprintf("虎皮椒支付 webhook 忽略非成功状态 trade_no=%s status=%s client_ip=%s", tradeNo, status, c.ClientIP()))
		_, _ = c.Writer.Write([]byte("success"))
		return
	}
	if tradeNo == "" {
		logger.LogWarn(ctx, fmt.Sprintf("虎皮椒支付 webhook 缺少订单号 client_ip=%s", c.ClientIP()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)

	topUp := model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil {
		logger.LogWarn(ctx, fmt.Sprintf("虎皮椒支付 回调订单不存在 trade_no=%s client_ip=%s", tradeNo, c.ClientIP()))
		_, _ = c.Writer.Write([]byte("success"))
		return
	}
	if topUp.PaymentProvider != model.PaymentProviderXunhuPay {
		logger.LogWarn(ctx, fmt.Sprintf("虎皮椒支付 订单支付网关不匹配 trade_no=%s order_provider=%s client_ip=%s", tradeNo, topUp.PaymentProvider, c.ClientIP()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}
	if topUp.Status == common.TopUpStatusSuccess {
		logger.LogInfo(ctx, fmt.Sprintf("虎皮椒支付 重复成功回调已忽略 trade_no=%s client_ip=%s", tradeNo, c.ClientIP()))
		_, _ = c.Writer.Write([]byte("success"))
		return
	}
	if topUp.Status != common.TopUpStatusPending {
		logger.LogWarn(ctx, fmt.Sprintf("虎皮椒支付 订单状态非 pending trade_no=%s status=%s client_ip=%s", tradeNo, topUp.Status, c.ClientIP()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	if !isSameXunhuPayAmount(params["total_fee"], topUp.Money) {
		logger.LogWarn(ctx, fmt.Sprintf("虎皮椒支付 回调金额不一致 trade_no=%s order_money=%s callback_money=%q client_ip=%s", tradeNo, formatXunhuPayAmount(topUp.Money), params["total_fee"], c.ClientIP()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	if err := model.RechargeXunhuPay(tradeNo, c.ClientIP()); err != nil {
		logger.LogError(ctx, fmt.Sprintf("虎皮椒支付 充值处理失败 trade_no=%s client_ip=%s error=%q", tradeNo, c.ClientIP(), err.Error()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	logger.LogInfo(ctx, fmt.Sprintf("虎皮椒支付 充值成功 trade_no=%s user_id=%d client_ip=%s money=%.2f", topUp.TradeNo, topUp.UserId, c.ClientIP(), topUp.Money))
	_, _ = c.Writer.Write([]byte("success"))
}
