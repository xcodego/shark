// Package sharkverify 提供了基于 TOTP（时间一次性密码）的 Google 验证码功能。
//
// 用于实现两步验证（2FA），生成和验证 Google Authenticator 兼容的验证码。
//
// 典型使用流程:
//  1. NewSecret 生成密钥和二维码 URL（用户扫描绑定）
//  2. VerifyCode 验证用户输入的 6 位验证码
//  3. GetQrCodeUrl 根据已有密钥重新生成二维码 URL
package sharkverify

import "github.com/pquerna/otp/totp"

// VerifyCode 验证 Google Authenticator 生成的 6 位 TOTP 验证码。
//
// 使用示例:
//
//	// 验证用户输入的验证码
//	secret := "JBSWY3DPEHPK3PXP"   // 用户绑定时生成的密钥
//	code := "123456"               // 用户输入的 6 位验证码
//	if sharkverify.VerifyCode(secret, code) {
//	    // 验证成功
//	} else {
//	    // 验证失败
//	}
//
// 参数:
//   - secret: 用户绑定的 Base32 密钥
//   - code: 用户输入的 6 位数字验证码
//
// 返回:
//   - true: 验证通过
//   - false: 验证失败
func VerifyCode(secret string, code string) bool {
	return totp.Validate(code, secret)
}

// NewSecret 生成新的 TOTP 密钥和绑定二维码 URL。
//
// 使用示例:
//
//	// 生成新的密钥和二维码
//	secret, qrUrl := sharkverify.NewSecret("MyCompany", "user@example.com")
//	// secret = "JBSWY3DPEHPK3PXP"    ← 存入数据库
//	// qrUrl   = "otpauth://totp/..." ← 生成二维码让用户扫描
//
// 参数:
//   - issuer: 发行者名称（如公司名，会显示在 Google Authenticator 中）
//   - accountName: 账户名称（如用户名或邮箱）
//
// 返回:
//   - string: Base32 编码的密钥（需安全存储，用于后续验证）
//   - string: otpauth:// 格式的二维码 URL
func NewSecret(issuer string, accountName string) (string, string) {
	key, _ := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
	})
	return key.Secret(), key.URL()
}

// GetQrCodeUrl 根据已有密钥重新生成绑定二维码 URL。
//
// 适用于用户更换手机或重新绑定验证器的场景。
//
// 使用示例:
//
//	// 用户重新绑定
//	secret := "JBSWY3DPEHPK3PXP" // 已存储的密钥
//	qrUrl, err := sharkverify.GetQrCodeUrl(secret, "MyCompany", "user@example.com")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	// 将 qrUrl 生成二维码让用户扫描
//
// 参数:
//   - secret: 已有的 Base32 密钥
//   - issuer: 发行者名称
//   - accountName: 账户名称
//
// 返回:
//   - string: otpauth:// 格式的二维码 URL
//   - error: 生成失败时返回错误
func GetQrCodeUrl(secret string, issuer string, accountName string) (string, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
		Secret:      []byte(secret),
	})
	return key.URL(), err
}
