// Package sharkutils 提供随机数、哈希、客户端 IP、时间格式、TiDB CDC 解码等通用工具。
package sharkutils

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	crand "math/rand/v2"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// RandNum 返回 [min, max) 内的随机整数。min <= 0 或 min >= max 时返回 0。
func RandNum(min int, max int) int {
	if min <= 0 || min >= max {
		return 0
	}
	return crand.IntN(max-min) + min
}

// Md5 返回 data 的 MD5 十六进制摘要（小写）。
func Md5(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

// GetClientIp 按 X-Forwarded-For、X-Real-Ip、RemoteAddr 的顺序取客户端 IP。
// X-Forwarded-For 只取逗号前第一段；X-Real-Ip 取整段。两者都去掉首尾空白，命中后直接返回。
// RemoteAddr 能拆出主机时返回主机，否则返回原值。request 为 nil 时返回空字符串。
// 不判断这些头是否来自可信代理。
func GetClientIp(request *http.Request) string {
	if request == nil {
		return ""
	}
	ip := request.Header.Get("X-Forwarded-For")
	if ip != "" {
		return strings.TrimSpace(strings.SplitN(ip, ",", 2)[0])
	}
	ip = request.Header.Get("X-Real-Ip")
	if ip != "" {
		return strings.TrimSpace(ip)
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil {
		return host
	}
	return request.RemoteAddr
}

// BcryptHash 用 bcrypt.DefaultCost 哈希密码。生成失败时返回空字符串。
func BcryptHash(password string) string {
	bytes, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes)
}

// BcryptCheck 判断 password 是否与 bcrypt 哈希 hashedPassword 匹配。
func BcryptCheck(hashedPassword string, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil
}

// DrainChannelN 从 ch 取出最多 size 条数据。
//
// 先阻塞直到读到第一条、通道关闭或 ctx 取消，这段不受 timeout 限制。
// 读到第一条之后，通道里已有的数据会继续取出。通道暂时为空时，timeout > 0 才继续等，
// 等待从读到第一条起算，最长约 timeout，并且只在通道空闲时生效；持续有数据则会一直读到 size 条。
// 等待使用 time.Sleep，每段最多 1 秒，睡眠期间不检查 ctx。
// ch 为 nil 或 size 为 0 时返回空切片；size 小于 0 时 panic。ctx 为 nil 时使用 context.Background。
func DrainChannelN[T any](ctx context.Context, ch <-chan T, size int, timeout time.Duration) []T {
	if ctx == nil {
		ctx = context.Background()
	}
	var result []T = make([]T, 0, size)
	if ch == nil || size <= 0 {
		return result
	}
	select {
	case <-ctx.Done():
		return result
	case v, ok := <-ch:
		if !ok {
			return result
		}
		result = append(result, v)
	}
	start := time.Now()
	for len(result) < size {
		select {
		case <-ctx.Done():
			return result
		case v, ok := <-ch:
			if !ok {
				return result
			}
			result = append(result, v)
		default:
			since := time.Since(start)
			if timeout > 0 && since <= timeout {
				sleepTime := time.Second
				if timeout-since < sleepTime {
					sleepTime = timeout - since
				}
				time.Sleep(sleepTime)
				continue
			}
			return result
		}
	}
	return result
}

// Pointer 返回指向 v 副本的指针。
func Pointer[T any](v T) *T {
	return &v
}

// FormatToRFC3330 把 "2006-01-02 15:04:05" 按 UTC 解析，并格式化为 RFC3339，如 "2024-01-02T15:04:05Z"。
// s 为 nil、空串或解析失败时返回 nil。
func FormatToRFC3330(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse(time.DateTime, *s)
	if err != nil {
		return nil
	}
	str := t.Format(time.RFC3339)
	return &str
}

// FormatRFC3330WithLocation 按 loc 解析 "2006-01-02 15:04:05"，再格式化为 RFC3339。
// 例如上海时区得到 "2024-01-02T15:04:05+08:00"。s 为 nil、空串或解析失败时返回 nil。loc 不能为 nil。
func FormatRFC3330WithLocation(s *string, loc *time.Location) *string {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.ParseInLocation(time.DateTime, *s, loc)
	if err != nil {
		return nil
	}
	str := t.Format(time.RFC3339)
	return &str
}

// FormatRFC3330WithFormat 按 format 解析（字符串本身无时区时视为 UTC），再格式化为 RFC3339。
// 如 format 为 "02/01/2006 15:04:05" 时，"02/01/2024 15:04:05" 得到 "2024-01-02T15:04:05Z"。
// s 为 nil、空串或解析失败时返回 nil。
func FormatRFC3330WithFormat(s *string, format string) *string {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse(format, *s)
	if err != nil {
		return nil
	}
	str := t.Format(time.RFC3339)
	return &str
}

// FormatToRFC3330WithFormatAndLocation 按 format 和 loc 解析，再格式化为 RFC3339。
// 如上海时区、format 为 "02/01/2006 15:04:05" 时，"02/01/2024 15:04:05" 得到 "2024-01-02T15:04:05+08:00"。
// s 为 nil、空串或解析失败时返回 nil。loc 不能为 nil。
func FormatToRFC3330WithFormatAndLocation(s *string, format string, loc *time.Location) *string {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.ParseInLocation(format, *s, loc)
	if err != nil {
		return nil
	}
	str := t.Format(time.RFC3339)
	return &str
}

// MakeKey 把参数按序号组成 JSON 对象作为键。MakeKey("a", 1) 得到 {"0":"a","1":1}，无参数时得到 {}。
// 序列化失败时返回空字符串。
func MakeKey(args ...any) string {
	m := make(map[int]any)
	for k, v := range args {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// TidbCdcUnmarshal 把 TiDB CDC 的行 JSON 解码进结构体。各字段的值须为字符串或 null。
// v 必须是非 nil 的结构体指针。按 json 标签逗号前的名字匹配；标签为空、为 "-" 或未导出的字段会跳过。
// 匿名嵌入的结构体值（json 名为空，且不是 time.Time）会展开到同一层。
// 字段缺失时保持原值；值为 null 时置为零值。
//
// 字符串按字段类型转换：
//   - *string 的空串保留为指向 "" 的指针；其他类型的空串置为零值
//   - 整数按十进制解析，失败时接受没有小数部分的浮点写法（如 "1.0"）
//   - 无符号整数、浮点数按十进制解析
//   - bool 接受 1/t/true/y/yes/on 与 0/f/false/n/no/off，忽略大小写
//   - time.Time 按 time.Local 解析 RFC3339、日期、日期时间或当天时间
//   - []byte 写入字符串的原始字节
//   - 其他类型把该字符串再按 JSON 解码
func TidbCdcUnmarshal(data string, v any) error {
	if data == "" {
		return fmt.Errorf("cdc: data is empty")
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("cdc: v must be a non-nil pointer to struct")
	}
	raw := make(map[string]*string)
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return fmt.Errorf("cdc: unmarshal data: %w", err)
	}

	timeType := reflect.TypeOf(time.Time{})

	// setValue 把一列字符串写入字段。sp 为 nil 表示 JSON null。
	var setValue func(fv reflect.Value, sp *string) error
	setValue = func(fv reflect.Value, sp *string) error {
		if fv.Kind() == reflect.Pointer {
			// 空串只对 *string 保留，其余指针置为 nil
			if sp == nil || (*sp == "" && fv.Type().Elem().Kind() != reflect.String) {
				fv.SetZero()
				return nil
			}
			nv := reflect.New(fv.Type().Elem())
			if err := setValue(nv.Elem(), sp); err != nil {
				return err
			}
			fv.Set(nv)
			return nil
		}
		if sp == nil {
			fv.SetZero()
			return nil
		}
		s := *sp
		if fv.Kind() == reflect.String {
			fv.SetString(s)
			return nil
		}
		if s == "" {
			fv.SetZero()
			return nil
		}
		if fv.Type() == timeType {
			// 有 T 且带时区用 RFC3339Nano；否则按日期、日期时间或当天时间解析，无时区用 time.Local
			var layout string
			switch {
			case strings.Contains(s, "T"):
				if len(s) > 19 && strings.ContainsAny(s[19:], "Z+-") {
					layout = time.RFC3339Nano
				} else {
					layout = "2006-01-02T15:04:05.999999999"
				}
			case len(s) == len("2006-01-02"):
				layout = "2006-01-02"
			case len(s) >= len("2006-01-02") && s[4] == '-' && s[7] == '-':
				if strings.ContainsAny(s[10:], "+-Z") {
					layout = "2006-01-02 15:04:05.999999999Z07:00"
				} else {
					layout = "2006-01-02 15:04:05.999999999"
				}
			default:
				layout = "15:04:05.999999999"
			}
			t, err := time.ParseInLocation(layout, s, time.Local)
			if err != nil {
				return fmt.Errorf("invalid time %q: %w", s, err)
			}
			fv.Set(reflect.ValueOf(t))
			return nil
		}
		switch fv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				// CDC 有时把整数写成 "1.0"
				f, ferr := strconv.ParseFloat(s, 64)
				if ferr != nil || f != float64(int64(f)) {
					return err
				}
				n = int64(f)
			}
			if fv.OverflowInt(n) {
				return fmt.Errorf("value %s overflows %s", s, fv.Type())
			}
			fv.SetInt(n)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			n, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				return err
			}
			if fv.OverflowUint(n) {
				return fmt.Errorf("value %s overflows %s", s, fv.Type())
			}
			fv.SetUint(n)
		case reflect.Float32, reflect.Float64:
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return err
			}
			fv.SetFloat(f)
		case reflect.Bool:
			switch strings.ToLower(s) {
			case "1", "t", "true", "y", "yes", "on":
				fv.SetBool(true)
			case "0", "f", "false", "n", "no", "off":
				fv.SetBool(false)
			default:
				return fmt.Errorf("invalid bool %q", s)
			}
		case reflect.Slice:
			if fv.Type().Elem().Kind() == reflect.Uint8 {
				fv.SetBytes([]byte(s))
				return nil
			}
			fallthrough
		default:
			return json.Unmarshal([]byte(s), fv.Addr().Interface())
		}
		return nil
	}

	// fill 按 json 标签填字段；匿名结构体展开，缺失的键不改原值
	var fill func(sv reflect.Value) error
	fill = func(sv reflect.Value) error {
		st := sv.Type()
		for i := 0; i < st.NumField(); i++ {
			sf := st.Field(i)
			name, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
			if sf.Anonymous && name == "" && sf.Type.Kind() == reflect.Struct && sf.Type != timeType {
				if err := fill(sv.Field(i)); err != nil {
					return err
				}
				continue
			}
			if name == "" || name == "-" || !sf.IsExported() {
				continue
			}
			sp, ok := raw[name]
			if !ok {
				continue
			}
			if err := setValue(sv.Field(i), sp); err != nil {
				return fmt.Errorf("cdc: field %s: %w", sf.Name, err)
			}
		}
		return nil
	}
	return fill(rv.Elem())
}

// Compress 用 zlib 默认压缩级别压缩 data。
func Compress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer := zlib.NewWriter(&buf)
	_, err := writer.Write(data)
	if err != nil {
		writer.Close()
		return nil, err
	}
	err = writer.Close()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decompress 解压 zlib 数据并返回全部字节。
func Decompress(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var buf bytes.Buffer
	_, err = buf.ReadFrom(reader)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
