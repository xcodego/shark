// Package sharkzip 提供了基于 zlib 的数据压缩与解压功能。
//
// 适用于对数据进行无损压缩以减少存储/传输体积的场景。
// 注意：zlib 不是 zip 文件格式，而是一种流式压缩算法。
// 如需 .zip 文件打包，请使用 archive/zip 标准库。
package sharkzip

import (
	"bytes"
	"compress/zlib"
)

// Compress 使用 zlib 算法压缩数据。
//
// 使用示例:
//
//	// 压缩 JSON 数据
//	original := []byte(`{"name":"张三","age":25,"bio":"这是一段很长的介绍文字..."}`)
//	compressed, err := sharkzip.Compress(original)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	// compressed 通常比 original 小很多
//	fmt.Printf("压缩前: %d bytes, 压缩后: %d bytes\n", len(original), len(compressed))
//
// 参数:
//   - data: 待压缩的原始字节数据
//
// 返回:
//   - []byte: 压缩后的数据
//   - error: 压缩过程中出错时返回
func Compress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer := zlib.NewWriter(&buf)
	_, err := writer.Write(data)
	if err != nil {
		writer.Close()
		return nil, err
	}
	// 必须先 Close 才能确保所有数据写入 buf
	err = writer.Close()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decompress 解压由 Compress 函数压缩的 zlib 数据。
//
// 使用示例:
//
//	// 解压数据（与 Compress 配对使用）
//	compressed, _ := sharkzip.Compress([]byte("hello world"))
//	decompressed, err := sharkzip.Decompress(compressed)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Println(string(decompressed)) // "hello world"
//
// 参数:
//   - data: 由 Compress 函数压缩的数据
//
// 返回:
//   - []byte: 解压后的原始数据
//   - error: 解压失败（如数据格式不正确）时返回
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
