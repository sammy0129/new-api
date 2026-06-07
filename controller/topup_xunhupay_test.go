package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMakeXunhuPaySignFiltersAndSortsParams(t *testing.T) {
	params := map[string]string{
		"total_fee": "9.90",
		"hash":      "ignored",
		"empty":     "",
		"appid":     "test123",
		"time":      "1700000000",
		"nonce_str": "abc",
	}

	require.Equal(t, "53ed2802e729ad481445ec83ac158400", makeXunhuPaySign(params, "mysecret"))
}

func TestBuildXunhuPayPaymentParams(t *testing.T) {
	originalAppID := setting.XunhuPayAppID
	originalSecret := setting.XunhuPaySecret
	originalNotifyURL := setting.XunhuPayNotifyUrl
	originalReturnURL := setting.XunhuPayReturnUrl
	t.Cleanup(func() {
		setting.XunhuPayAppID = originalAppID
		setting.XunhuPaySecret = originalSecret
		setting.XunhuPayNotifyUrl = originalNotifyURL
		setting.XunhuPayReturnUrl = originalReturnURL
	})

	setting.XunhuPayAppID = "appid"
	setting.XunhuPaySecret = "secret"
	setting.XunhuPayNotifyUrl = "https://callback.example.com/xunhupay"
	setting.XunhuPayReturnUrl = "https://app.example.com/console/topup"

	params := buildXunhuPayPaymentParams("XHPUSR1NOabc", 100, 12.345)
	require.Equal(t, "1.1", params["version"])
	require.Equal(t, "appid", params["appid"])
	require.Equal(t, "XHPUSR1NOabc", params["trade_order_id"])
	require.Equal(t, "12.35", params["total_fee"])
	require.Equal(t, "TUC100", params["title"])
	require.Equal(t, "https://callback.example.com/xunhupay", params["notify_url"])
	require.Equal(t, "https://app.example.com/console/topup", params["return_url"])
	require.Len(t, params["nonce_str"], 32)
	require.True(t, verifyXunhuPaySign(params, "secret"))
}

func TestIsSameXunhuPayAmountRequiresExactCentValue(t *testing.T) {
	require.True(t, isSameXunhuPayAmount("9.90", 9.9))
	require.True(t, isSameXunhuPayAmount("9.9", 9.9))
	require.False(t, isSameXunhuPayAmount("9.89", 9.9))
	require.False(t, isSameXunhuPayAmount("9.91", 9.9))
	require.False(t, isSameXunhuPayAmount("bad", 9.9))
}

func TestIsValidXunhuPayNotifyURLRejectsLocalhost(t *testing.T) {
	require.True(t, isValidXunhuPayNotifyURL("https://pay.example.com/api/xunhupay/notify"))
	require.False(t, isValidXunhuPayNotifyURL("http://localhost:3000/api/xunhupay/notify"))
	require.False(t, isValidXunhuPayNotifyURL("http://127.0.0.1:3000/api/xunhupay/notify"))
	require.False(t, isValidXunhuPayNotifyURL("ftp://pay.example.com/api/xunhupay/notify"))
}

func setupXunhuPayNotifyTest(t *testing.T) {
	t.Helper()
	originalDB := model.DB
	originalLogDB := model.LOG_DB
	originalUsingSQLite := common.UsingSQLite
	originalUsingMySQL := common.UsingMySQL
	originalUsingPostgreSQL := common.UsingPostgreSQL
	originalRedisEnabled := common.RedisEnabled
	originalBatchUpdateEnabled := common.BatchUpdateEnabled
	originalLogConsumeEnabled := common.LogConsumeEnabled

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = true
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Log{}))

	originalEnabled := setting.XunhuPayEnabled
	originalGateway := setting.XunhuPayGateway
	originalAppID := setting.XunhuPayAppID
	originalSecret := setting.XunhuPaySecret
	originalServerAddress := system_setting.ServerAddress
	paymentSetting := operation_setting.GetPaymentSetting()
	originalConfirmed := paymentSetting.ComplianceConfirmed
	originalTermsVersion := paymentSetting.ComplianceTermsVersion
	t.Cleanup(func() {
		setting.XunhuPayEnabled = originalEnabled
		setting.XunhuPayGateway = originalGateway
		setting.XunhuPayAppID = originalAppID
		setting.XunhuPaySecret = originalSecret
		system_setting.ServerAddress = originalServerAddress
		paymentSetting.ComplianceConfirmed = originalConfirmed
		paymentSetting.ComplianceTermsVersion = originalTermsVersion
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		common.UsingSQLite = originalUsingSQLite
		common.UsingMySQL = originalUsingMySQL
		common.UsingPostgreSQL = originalUsingPostgreSQL
		common.RedisEnabled = originalRedisEnabled
		common.BatchUpdateEnabled = originalBatchUpdateEnabled
		common.LogConsumeEnabled = originalLogConsumeEnabled
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	setting.XunhuPayEnabled = true
	setting.XunhuPayGateway = "https://pay.example.com"
	setting.XunhuPayAppID = "appid"
	setting.XunhuPaySecret = "secret"
	system_setting.ServerAddress = "https://app.example.com"
	paymentSetting.ComplianceConfirmed = true
	paymentSetting.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion

	user := &model.User{Id: 901, Username: "xunhupay_user", Status: common.UserStatusEnabled, Quota: 0}
	require.NoError(t, model.DB.Create(user).Error)
}

func insertXunhuPayTopUpForNotifyTest(t *testing.T, tradeNo string, provider string, status string, money float64) {
	t.Helper()
	topUp := &model.TopUp{
		UserId:          901,
		Amount:          2,
		Money:           money,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodXunhuPay,
		PaymentProvider: provider,
		Status:          status,
		CreateTime:      time.Now().Unix(),
	}
	require.NoError(t, topUp.Insert())
}

func signedXunhuPayNotifyForm(values map[string]string) string {
	values["appid"] = "appid"
	values["time"] = "1700000000"
	values["nonce_str"] = "nonce"
	values["hash"] = makeXunhuPaySign(values, "secret")

	form := url.Values{}
	for key, value := range values {
		form.Set(key, value)
	}
	return form.Encode()
}

func performXunhuPayNotify(body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/xunhupay/notify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Request = req
	XunhuPayNotify(c)
	return recorder
}

func getNotifyTestUserQuota(t *testing.T) int {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.Select("quota").Where("id = ?", 901).First(&user).Error)
	return user.Quota
}

func TestXunhuPayNotifyRejectsBadSignature(t *testing.T) {
	setupXunhuPayNotifyTest(t)
	insertXunhuPayTopUpForNotifyTest(t, "XHPUSR901NObad", model.PaymentProviderXunhuPay, common.TopUpStatusPending, 9.9)

	form := url.Values{}
	form.Set("trade_order_id", "XHPUSR901NObad")
	form.Set("total_fee", "9.90")
	form.Set("status", xunhuPaySuccessStatus)
	form.Set("hash", "bad")

	recorder := performXunhuPayNotify(form.Encode())
	require.Equal(t, "fail", recorder.Body.String())
	require.Equal(t, 0, getNotifyTestUserQuota(t))
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo("XHPUSR901NObad").Status)
}

func TestXunhuPayNotifyRejectsAppIDMismatch(t *testing.T) {
	setupXunhuPayNotifyTest(t)
	insertXunhuPayTopUpForNotifyTest(t, "XHPUSR901NOappid", model.PaymentProviderXunhuPay, common.TopUpStatusPending, 9.9)

	values := map[string]string{
		"appid":          "other_appid",
		"trade_order_id": "XHPUSR901NOappid",
		"total_fee":      "9.90",
		"status":         xunhuPaySuccessStatus,
		"time":           "1700000000",
		"nonce_str":      "nonce",
	}
	values["hash"] = makeXunhuPaySign(values, "secret")

	form := url.Values{}
	for key, value := range values {
		form.Set(key, value)
	}

	recorder := performXunhuPayNotify(form.Encode())
	require.Equal(t, "fail", recorder.Body.String())
	require.Equal(t, 0, getNotifyTestUserQuota(t))
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo("XHPUSR901NOappid").Status)
}

func TestXunhuPayNotifyRejectsAmountMismatch(t *testing.T) {
	setupXunhuPayNotifyTest(t)
	insertXunhuPayTopUpForNotifyTest(t, "XHPUSR901NOmismatch", model.PaymentProviderXunhuPay, common.TopUpStatusPending, 9.9)

	body := signedXunhuPayNotifyForm(map[string]string{
		"trade_order_id": "XHPUSR901NOmismatch",
		"total_fee":      "8.80",
		"status":         xunhuPaySuccessStatus,
	})

	recorder := performXunhuPayNotify(body)
	require.Equal(t, "fail", recorder.Body.String())
	require.Equal(t, 0, getNotifyTestUserQuota(t))
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo("XHPUSR901NOmismatch").Status)
}

func TestXunhuPayNotifyRejectsOneCentUnderpayment(t *testing.T) {
	setupXunhuPayNotifyTest(t)
	insertXunhuPayTopUpForNotifyTest(t, "XHPUSR901NOunderpay", model.PaymentProviderXunhuPay, common.TopUpStatusPending, 9.9)

	body := signedXunhuPayNotifyForm(map[string]string{
		"trade_order_id": "XHPUSR901NOunderpay",
		"total_fee":      "9.89",
		"status":         xunhuPaySuccessStatus,
	})

	recorder := performXunhuPayNotify(body)
	require.Equal(t, "fail", recorder.Body.String())
	require.Equal(t, 0, getNotifyTestUserQuota(t))
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo("XHPUSR901NOunderpay").Status)
}

func TestXunhuPayNotifyRejectsProviderMismatch(t *testing.T) {
	setupXunhuPayNotifyTest(t)
	insertXunhuPayTopUpForNotifyTest(t, "XHPUSR901NOprovider", model.PaymentProviderStripe, common.TopUpStatusPending, 9.9)

	body := signedXunhuPayNotifyForm(map[string]string{
		"trade_order_id": "XHPUSR901NOprovider",
		"total_fee":      "9.90",
		"status":         xunhuPaySuccessStatus,
	})

	recorder := performXunhuPayNotify(body)
	require.Equal(t, "fail", recorder.Body.String())
	require.Equal(t, 0, getNotifyTestUserQuota(t))
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo("XHPUSR901NOprovider").Status)
}

func TestXunhuPayNotifySuccessAndDuplicateIsIdempotent(t *testing.T) {
	setupXunhuPayNotifyTest(t)
	insertXunhuPayTopUpForNotifyTest(t, "XHPUSR901NOsuccess", model.PaymentProviderXunhuPay, common.TopUpStatusPending, 9.9)
	body := signedXunhuPayNotifyForm(map[string]string{
		"trade_order_id": "XHPUSR901NOsuccess",
		"total_fee":      "9.90",
		"status":         xunhuPaySuccessStatus,
	})

	recorder := performXunhuPayNotify(body)
	require.Equal(t, "success", recorder.Body.String())
	require.Equal(t, int(2*common.QuotaPerUnit), getNotifyTestUserQuota(t))
	require.Equal(t, common.TopUpStatusSuccess, model.GetTopUpByTradeNo("XHPUSR901NOsuccess").Status)

	recorder = performXunhuPayNotify(body)
	require.Equal(t, "success", recorder.Body.String())
	require.Equal(t, int(2*common.QuotaPerUnit), getNotifyTestUserQuota(t))
}

func TestRequestXunhuPayPaymentRejectsResponseWithoutPaymentURL(t *testing.T) {
	originalGateway := setting.XunhuPayGateway
	t.Cleanup(func() {
		setting.XunhuPayGateway = originalGateway
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/payment/do.html", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"success","hash":"ignored","data":{"url_qrcode":"https://pay.example.com/qrcode.png","open_order_id":"HPJ1"}}`))
	}))
	defer server.Close()

	setting.XunhuPayGateway = server.URL

	result, err := requestXunhuPayPayment(map[string]string{"appid": "appid"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing payment url")
	require.NotNil(t, result)
}
