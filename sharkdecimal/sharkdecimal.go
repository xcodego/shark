// Package sharkdecimal 提供基于 shopspring/decimal 的高精度数值处理工具。
//
// 核心功能：
//  1. 类型归一化：将任意数值类型（float/int/uint/string/decimal.Decimal）统一转换为 decimal.Decimal
//  2. 精度控制：通过 Round + Truncate 组合，消除浮点累积误差并精确截断到指定位数
//  3. 便捷函数：Normalize2（金额）和 Normalize6（汇率/费率）覆盖最常见的小数位数
//
// 处理流程（Normalize 系列函数）：
//  1. 类型转换：将输入 v 转为 decimal.Decimal（支持 10+ 种 Go 数值类型）
//  2. Round(roundScale)：以不低于 8 位的高精度四舍五入，消除 float32/float64 的累积误差
//  3. Truncate(places)：截断到目标小数位数（不做四舍五入，直接去掉多余位）
//
// 与 math/big.Rat 的区别：
//   - decimal.Decimal 是十进制定点数，天然适合金额计算（无二进制浮点精度问题）
//   - 支持 JSON 序列化/反序列化（数字以字符串形式存储，精度不丢失）
//   - 支持 GORM 数据库扫描（sql.Scanner 接口）
//
// 重要：Normalize 使用 Truncate（截断），不是 Round（四舍五入）！
//
//	Normalize(19.999, 2) → 19.99（截断）
//	若需要四舍五入：decimal.NewFromFloat(19.999).Round(2) → 20.00
//
// 使用示例：
//
//	import sd "github.com/lornshark/shark/sharkdecimal"
//
//	// 金额计算（保留 2 位）
//	price := sd.Normalize2(19.999)          // 19.99
//	tax := sd.Normalize2("3.50")            // 3.50
//	total := price.Add(tax)                 // 23.49
//
//	// 汇率计算（保留 6 位）
//	rate := sd.Normalize6(7.12345678)       // 7.123456
//	usdAmount := sd.Normalize2(100)
//	cnyAmount := usdAmount.Mul(rate)        // decimal 乘 decimal
//	result := sd.Normalize2(cnyAmount)       // 最终金额保留 2 位
//
//	// 自定义精度（保留 4 位）
//	score := sd.Normalize(3.1415926535, 4)  // 3.1415
//
//	// 数据库读写（decimal.Decimal 实现了 sql.Scanner）
//	type Product struct {
//	    Price decimal.Decimal `gorm:"column:price"`
//	}
package sharkdecimal

import (
	"math/big"

	"github.com/shopspring/decimal"
	"github.com/spf13/cast"
)

// minRoundScale 定义 Normalize 内部 Round 操作的最低精度。
//
// Round 精度取 max(places+2, minRoundScale)，确保：
//   - 消除 float32/float64 的二进制浮点累积误差
//   - 后续 Truncate 操作不会因 Round 精度不足而丢失低位数据
//
// 例如：Normalize(1.0/3.0, 2) 时 roundScale = max(2+2, 8) = 8，
// Round(8) 后将 0.33333333... 固定为 0.33333333，再 Truncate(2) → 0.33。
const minRoundScale int32 = 8

// Normalize2 将任意数值转换为 decimal.Decimal，截断保留 2 位小数。
//
// 适用于：金额、价格、余额等需要两位小数的金融场景。
// 底层调用 Normalize(v, 2)，处理逻辑完全一致。
//
// 参数：
//   - v: 任意数值类型（float/int/string/decimal.Decimal 等）
//
// 返回值：
//   - decimal.Decimal: 截断到 2 位的 decimal 值。非法输入返回 decimal.Zero（即 0）
//
// 使用示例：
//
//	import sd "github.com/lornshark/shark/sharkdecimal"
//
//	// 金额归一化
//	amount := sd.Normalize2(19.999)       // → 19.99（截断）
//	amount = sd.Normalize2("19.999")      // → 19.99（字符串输入）
//	amount = sd.Normalize2(20)            // → 20.00（整数补齐小数位）
//	amount = sd.Normalize2(0)             // → 0.00
//
//	// 金额计算
//	price := sd.Normalize2(19.99)
//	tax := sd.Normalize2(1.50)
//	total := sd.Normalize2(price.Add(tax)) // → 21.49
//
//	// 非法输入
//	sd.Normalize2("abc") // → 0.00
func Normalize2(v any) decimal.Decimal {
	return Normalize(v, 2)
}

// Normalize6 将任意数值转换为 decimal.Decimal，截断保留 6 位小数。
//
// 适用于：汇率、费率、手续费率、科学计算等需要较高精度的场景。
// 底层调用 Normalize(v, 6)，处理逻辑完全一致。
//
// 参数：
//   - v: 任意数值类型
//
// 返回值：
//   - decimal.Decimal: 截断到 6 位的 decimal 值。非法输入返回 decimal.Zero。
//
// 使用示例：
//
//	// 汇率精度（6 位小数）
//	rate := sd.Normalize6(7.12345678)       // → 7.123456
//	rate = sd.Normalize6("7.23456789")      // → 7.234567
//
//	// 汇率换算
//	usdRate := sd.Normalize6(7.250000)
//	usdAmount := sd.Normalize2(100)         // 100.00 USD
//	cnyAmount := sd.Normalize2(usdAmount.Mul(usdRate)) // → 725.00 CNY
//
//	// 科学计算
//	pi := sd.Normalize6(3.14159265358979)   // → 3.141592
func Normalize6(v any) decimal.Decimal {
	return Normalize(v, 6)
}

// Normalize 将任意数值转换为 decimal.Decimal，截断保留指定位数的小数。
//
// 支持的输入类型（按优先级匹配）：
//
//	go 类型              处理方式
//	──────────────────────────────────────────
//	decimal.Decimal       直接使用（无精度损失）
//	float32 / float64     转换为 Decimal（通过 Round 消除浮点误差）
//	int / int8 ... int64  整数转 Decimal
//	uint / uint8 ... uint64 无符号整数转 Decimal（含 uint64 大整数支持）
//	string                解析为 Decimal（非法字符串返回 Zero）
//	其他类型              通过 cast.ToString 转字符串再解析
//
// 处理流程：
//  1. 类型转换：根据 v 的实际类型转为 decimal.Decimal
//  2. Round(roundScale)：以 max(places+2, 8) 为精度四舍五入，消除浮点累积误差
//  3. Truncate(places)：截断到目标位数（不做四舍五入，多余位直接丢弃）
//
// 参数：
//   - v:      输入值（支持 10+ 种 Go 数值类型）
//   - places: 目标小数位数（< 0 时自动修正为 0）
//
// 返回值：
//   - decimal.Decimal: 截断后的 decimal 值。非法输入返回 decimal.Zero。
//
// 使用示例：
//
//	import sd "github.com/lornshark/shark/sharkdecimal"
//
//	// 基本用法：截断到指定位数
//	sd.Normalize(19.999, 2)       // → 19.99（截断，不是四舍五入！）
//	sd.Normalize(19.999, 0)       // → 19
//	sd.Normalize(3.14159, 3)      // → 3.141
//	sd.Normalize(42, 4)           // → 42.0000
//
//	// 浮点误差消除
//	// 1.0/3.0 在 float64 中为 0.3333333333333333
//	sd.Normalize(1.0/3.0, 6)      // → 0.333333
//
//	// 字符串输入
//	sd.Normalize("19.99", 2)      // → 19.99
//	sd.Normalize("1e2", 2)        // → 100.00
//
//	// 非法输入返回 Zero
//	sd.Normalize("not-a-number", 2) // → 0.00
//	sd.Normalize(nil, 2)            // → 0.00
//
//	// uint64 大整数
//	var bigAmount uint64 = 18446744073709551615
//	sd.Normalize(bigAmount, 0)       // → 18446744073709551615
//
//	// 与 decimal 原生方法配合
//	a := sd.Normalize2(10.50)
//	b := sd.Normalize2(3.25)
//	c := sd.Normalize2(a.Add(b))     // 13.75
//	d := sd.Normalize2(a.Mul(b))     // 34.12（10.50 * 3.25 = 34.125，截断为 34.12）
func Normalize(v any, places int32) decimal.Decimal {
	if places < 0 {
		places = 0
	}
	// 第一步：类型转换
	var d decimal.Decimal
	switch val := v.(type) {
	case decimal.Decimal:
		// decimal.Decimal：直接使用，无精度损失
		d = val
	case string:
		// 字符串：解析为 Decimal（非法字符串返回 Zero）
		dd, err := decimal.NewFromString(val)
		if err != nil {
			return decimal.Zero
		}
		d = dd
	case float32:
		d = decimal.NewFromFloat32(val)
	case float64:
		d = decimal.NewFromFloat(val)
	case int:
		d = decimal.NewFromInt(int64(val))
	case int8:
		d = decimal.NewFromInt(int64(val))
	case int16:
		d = decimal.NewFromInt(int64(val))
	case int32:
		d = decimal.NewFromInt(int64(val))
	case int64:
		d = decimal.NewFromInt(val)
	case uint:
		d = decimal.NewFromInt(int64(val))
	case uint8:
		d = decimal.NewFromInt(int64(val))
	case uint16:
		d = decimal.NewFromInt(int64(val))
	case uint32:
		d = decimal.NewFromInt(int64(val))
	case uint64:
		// uint64 可能超过 int64 范围，使用 big.Int 转换
		d = decimal.NewFromBigInt(new(big.Int).SetUint64(val), 0)
	default:
		// 其他类型：通过 cast.ToString 转字符串再解析
		s := cast.ToString(v)
		dd, err := decimal.NewFromString(s)
		if err != nil {
			return decimal.Zero
		}
		d = dd
	}
	// 第二步：高精度 Round，消除浮点误差
	// roundScale = max(places + 2, minRoundScale(8))
	// 例如：places=2 → roundScale=8; places=8 → roundScale=10
	roundScale := places + 2
	if roundScale < minRoundScale {
		roundScale = minRoundScale
	}
	d = d.Round(roundScale)
	// 第三步：Truncate 到目标精度（截断，不做四舍五入）
	return d.Truncate(places)
}
