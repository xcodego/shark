// Package sharkverify 提供与 Google Authenticator 兼容的 TOTP 密钥生成和验证码校验。
// 默认 SHA1、6 位数字、30 秒一个周期；校验时允许前后各一个周期。
package sharkverify

import "github.com/pquerna/otp/totp"

// VerifyCode 用当前 UTC 时间校验 TOTP 验证码。
// secret 为 Base32 密钥。验证码不匹配或 secret 无法解码时返回 false。
func VerifyCode(secret string, code string) bool {
	return totp.Validate(code, secret)
}

// NewSecret 生成 20 字节随机密钥，返回其 Base32 字符串和对应的 otpauth URL。
// issuer 与 accountName 不能为空，否则 panic。
func NewSecret(issuer string, accountName string) (string, string) {
	key, _ := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
	})
	return key.Secret(), key.URL()
}

// GetQrCodeUrl 用已有密钥生成绑定用的 otpauth URL。
// secret 按原始字节做 Base32 编码后写入 URL。issuer 与 accountName 不能为空，否则 panic。
func GetQrCodeUrl(secret string, issuer string, accountName string) (string, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
		Secret:      []byte(secret),
	})
	return key.URL(), err
}
