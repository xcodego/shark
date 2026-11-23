// Package sharkutils 提供了一些通用的工具函数。
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

// RandNum 返回 [min, max) 内的随机整数；min <= 0 或 min >= max 时返回 0。
func RandNum(min int, max int) int {
	if min <= 0 || min >= max {
		return 0
	}
	return crand.IntN(max-min) + min
}

// Md5 返回数据的 MD5 十六进制摘要。
func Md5(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

// GetClientIp 从 X-Forwarded-For、X-Real-Ip 或 RemoteAddr 中获取客户端 IP。
func GetClientIp(request *http.Request) string {
	// 审查: 忽略信任问题
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

// BcryptHash 使用 bcrypt.DefaultCost 对密码进行哈希。
func BcryptHash(password string) string {
	bytes, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes)
}

// BcryptCheck 检查明文密码是否与 bcrypt 哈希匹配。
func BcryptCheck(hashedPassword string, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil
}

// DrainChannelN 最多读取 size 条数据，并支持 context 取消和超时等待。
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

// Pointer 返回 v 的指针。
func Pointer[T any](v T) *T {
	return &v
}

// FormatToRFC3330 将 "2024-01-02 15:04:05" 转为 RFC3339，如 "2024-01-02T15:04:05Z"。
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

// FormatRFC3330WithLocation 按指定时区解析时间，如上海时间 "2024-01-02 15:04:05" 转为 "2024-01-02T15:04:05+08:00"。
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

// FormatRFC3330WithFormat 按 format 解析后转为 RFC3339。
// 如 "02/01/2024 15:04:05"（format: "02/01/2006 15:04:05"）转为 "2024-01-02T15:04:05Z"。
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

// FormatToRFC3330WithFormatAndLocation 按 format 和指定时区解析后转为 RFC3339。
// 如上海时间 "02/01/2024 15:04:05"（format: "02/01/2006 15:04:05"）转为 "2024-01-02T15:04:05+08:00"。
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

// MakeKey 将参数序列化为字符串键。
func MakeKey(args ...any) string {
	m := make(map[int]any)
	for k, v := range args {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// TidbCdcUnmarshal 将字段值为字符串的 TiDB CDC JSON 解码到结构体，并按字段类型转换。
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

	var setValue func(fv reflect.Value, sp *string) error
	setValue = func(fv reflect.Value, sp *string) error {
		if fv.Kind() == reflect.Pointer {
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

// Compress 使用 zlib 压缩数据。
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

// Decompress 解压 zlib 数据。
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
