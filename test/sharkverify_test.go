package test

import (
	"strings"
	"testing"

	"github.com/lornshark/shark/sharkverify"
)

func TestVerifyCodeInvalid(t *testing.T) {
	// 无效的验证码应该返回 false
	if sharkverify.VerifyCode("JBSWY3DPEHPK3PXP", "000000") {
		t.Error("无效验证码应返回 false")
	}
}

func TestVerifyCodeEmpty(t *testing.T) {
	// 空字符串验证码
	if sharkverify.VerifyCode("JBSWY3DPEHPK3PXP", "") {
		t.Error("空验证码应返回 false")
	}
}

func TestNewSecret(t *testing.T) {
	secret, qrUrl := sharkverify.NewSecret("TestIssuer", "test@example.com")
	if secret == "" {
		t.Fatal("密钥不应为空")
	}
	if qrUrl == "" {
		t.Fatal("二维码 URL 不应为空")
	}
	if !strings.HasPrefix(qrUrl, "otpauth://totp/") {
		t.Errorf("URL 应以 otpauth://totp/ 开头: %s", qrUrl)
	}
	if !strings.Contains(qrUrl, "TestIssuer") {
		t.Errorf("URL 应包含 Issuer: %s", qrUrl)
	}
	if !strings.Contains(qrUrl, "test@example.com") {
		t.Errorf("URL 应包含账户名: %s", qrUrl)
	}
	t.Logf("secret: %s", secret)
	t.Logf("qrUrl: %s", qrUrl)
}

func TestNewSecretUnique(t *testing.T) {
	// 两次调用应生成不同的密钥（TOTP 每次随机生成）
	s1, _ := sharkverify.NewSecret("Issuer", "user1")
	s2, _ := sharkverify.NewSecret("Issuer", "user2")
	if s1 == s2 {
		t.Error("两次生成的密钥应该不同")
	}
}

func TestGetQrCodeUrl(t *testing.T) {
	secret, _ := sharkverify.NewSecret("MyApp", "user@test.com")
	qrUrl, err := sharkverify.GetQrCodeUrl(secret, "MyApp", "user@test.com")
	if err != nil {
		t.Fatalf("GetQrCodeUrl error: %v", err)
	}
	if qrUrl == "" {
		t.Fatal("二维码 URL 不应为空")
	}
	if !strings.HasPrefix(qrUrl, "otpauth://totp/") {
		t.Errorf("URL 应以 otpauth://totp/ 开头: %s", qrUrl)
	}
	if !strings.Contains(qrUrl, "MyApp") {
		t.Errorf("URL 应包含 Issuer: %s", qrUrl)
	}
}

func TestGetQrCodeUrlWithCustomSecret(t *testing.T) {
	// 使用已有的密钥重新生成二维码 URL
	secret, _ := sharkverify.NewSecret("TestApp", "user@test.com")
	qrUrl, err := sharkverify.GetQrCodeUrl(secret, "TestApp", "other@test.com")
	if err != nil {
		t.Fatalf("GetQrCodeUrl error: %v", err)
	}
	if qrUrl == "" {
		t.Fatal("二维码 URL 不应为空")
	}
	// 不同账户名应反映在 URL 中
	if !strings.Contains(qrUrl, "other@test.com") {
		t.Errorf("URL 应包含新账户名: %s", qrUrl)
	}
}
