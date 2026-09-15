package test

import (
	"testing"

	"github.com/lornshark/shark/sharkzip"
)

func TestCompressDecompress(t *testing.T) {
	original := []byte("hello world, this is a test string for zlib compression")
	compressed, err := sharkzip.Compress(original)
	if err != nil {
		t.Fatalf("Compress error: %v", err)
	}
	if len(compressed) == 0 {
		t.Fatal("压缩后数据为空")
	}
	decompressed, err := sharkzip.Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress error: %v", err)
	}
	if string(decompressed) != string(original) {
		t.Errorf("解压后不匹配: got %q, want %q", decompressed, original)
	}
	t.Logf("压缩比: %d → %d bytes (%.1f%%)", len(original), len(compressed), float64(len(compressed))/float64(len(original))*100)
}

func TestCompressEmpty(t *testing.T) {
	compressed, err := sharkzip.Compress([]byte{})
	if err != nil {
		t.Fatalf("Compress empty error: %v", err)
	}
	decompressed, err := sharkzip.Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress empty error: %v", err)
	}
	if len(decompressed) != 0 {
		t.Errorf("解压空数据应返回空: got %d bytes", len(decompressed))
	}
}

func TestCompressLargeData(t *testing.T) {
	// 1KB of 'A'
	original := make([]byte, 1024)
	for i := range original {
		original[i] = 'A'
	}
	compressed, err := sharkzip.Compress(original)
	if err != nil {
		t.Fatalf("Compress error: %v", err)
	}
	if len(compressed) >= len(original) {
		t.Errorf("重复数据压缩后不应该更大: %d >= %d", len(compressed), len(original))
	}
	decompressed, err := sharkzip.Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress error: %v", err)
	}
	if string(decompressed) != string(original) {
		t.Error("大数据解压不匹配")
	}
}

func TestDecompressInvalid(t *testing.T) {
	_, err := sharkzip.Decompress([]byte("not zlib data"))
	if err == nil {
		t.Error("非法数据解压应该返回错误")
	}
}
