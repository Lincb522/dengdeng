package provider

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dengdeng/internal/payment"
)

func TestValidateWxOutTradeNo(t *testing.T) {
	valid := []string{
		"ddp_1234567890123456789012345678",
		"ABCdef_-|*123",
	}
	for _, value := range valid {
		if err := validateWxOutTradeNo(value); err != nil {
			t.Fatalf("%q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"short", "ddp_12345678901234567890123456789", "ddp_bad.order"} {
		if err := validateWxOutTradeNo(value); err == nil {
			t.Fatalf("%q should be rejected", value)
		}
	}
}

func TestWxPayMerchantTransferUsesStableBillAndValidatesResponse(t *testing.T) {
	merchantKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	platformKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/fund-app/mch-transfer/transfer-bills" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") == "" {
			t.Fatal("missing merchant authorization")
		}
		var payload struct {
			OutBillNo      string `json:"out_bill_no"`
			OpenID         string `json:"openid"`
			TransferAmount int64  `json:"transfer_amount"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.OutBillNo != "DDC260725123456ABCDEF" || payload.OpenID != "openid-12345678" || payload.TransferAmount != 123 {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		body := []byte(`{"out_bill_no":"DDC260725123456ABCDEF","transfer_bill_no":"wx-1","state":"WAIT_USER_CONFIRM","package_info":"package-value"}`)
		timestamp, nonce := "1784950000", "response-nonce"
		digest := sha256.Sum256([]byte(timestamp + "\n" + nonce + "\n" + string(body) + "\n"))
		signature, err := rsa.SignPKCS1v15(rand.Reader, platformKey, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Wechatpay-Timestamp", timestamp)
		w.Header().Set("Wechatpay-Nonce", nonce)
		w.Header().Set("Wechatpay-Signature", base64.StdEncoding.EncodeToString(signature))
		_, _ = w.Write(body)
	}))
	defer server.Close()

	client := &WxPay{appID: "wx-app", mchID: "mch-1", serialNo: "merchant-serial", apiV3Key: "12345678901234567890123456789012", apiBase: server.URL, privateKey: merchantKey, platformKey: &platformKey.PublicKey, client: server.Client()}
	result, err := client.CreateMerchantTransfer(t.Context(), payment.MerchantTransferRequest{OutBillNo: "DDC260725123456ABCDEF", OpenID: "openid-12345678", AmountMinor: 123, SceneID: "1000", Remark: "推广佣金", NotifyURL: "https://example.test/callback", SceneReportInfo: []payment.MerchantTransferSceneInfo{{InfoType: "佣金类型", InfoContent: "推广佣金"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "WAIT_USER_CONFIRM" || result.PackageInfo != "package-value" || result.ProviderBillNo != "wx-1" {
		t.Fatalf("unexpected transfer response: %#v", result)
	}
}
