// Package sharkdecimal 提供高精度数值转换和截断工具。
package sharkdecimal

import (
	"math/big"

	"github.com/shopspring/decimal"
	"github.com/spf13/cast"
)

// minRoundScale 是内部 Round 使用的最低精度。
const minRoundScale int32 = 8

// Normalize2 将数值转换为 decimal.Decimal，并截断到 2 位小数。
func Normalize2(v any) decimal.Decimal {
	return Normalize(v, 2)
}

// Normalize6 将数值转换为 decimal.Decimal，并截断到 6 位小数。
func Normalize6(v any) decimal.Decimal {
	return Normalize(v, 6)
}

// Normalize 将数值转换为 decimal.Decimal，并截断（不四舍五入）到指定小数位。
// 支持 decimal、字符串、整数和浮点数，其他类型先经 cast 转为字符串再解析。
// 非法输入返回 decimal.Zero，负的 places 按 0 处理。
func Normalize(v any, places int32) decimal.Decimal {
	if places < 0 {
		places = 0
	}
	var d decimal.Decimal
	switch val := v.(type) {
	case decimal.Decimal:
		d = val
	case string:
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
		d = decimal.NewFromBigInt(new(big.Int).SetUint64(val), 0)
	default:
		s := cast.ToString(v)
		dd, err := decimal.NewFromString(s)
		if err != nil {
			return decimal.Zero
		}
		d = dd
	}
	// 截断前先四舍五入到更高精度（至少 minRoundScale 位），吸收输入中的微小尾差。
	roundScale := places + 2
	if roundScale < minRoundScale {
		roundScale = minRoundScale
	}
	d = d.Round(roundScale)
	return d.Truncate(places)
}
