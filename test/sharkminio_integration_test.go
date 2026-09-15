package test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/lornshark/shark/sharkminio"
	"github.com/minio/minio-go/v7"
)

func TestMinioNew(t *testing.T) {
	cfg := loadMinioConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := sharkminio.New(ctx, cfg)
	if err != nil {
		t.Fatalf("连接 MinIO 失败: %v", err)
	}
	t.Log("MinIO 连接成功")

	// 创建测试桶
	bucketName := "test-shark-bucket"
	exists, err := client.BucketExists(ctx, bucketName)
	if err != nil {
		t.Fatalf("BucketExists 失败: %v", err)
	}
	if !exists {
		err = client.MakeBucket(ctx, bucketName, minio.MakeBucketOptions{})
		if err != nil {
			t.Fatalf("MakeBucket 失败: %v", err)
		}
		t.Logf("创建桶: %s", bucketName)
	}

	// 上传对象
	objectName := "test/hello.txt"
	content := []byte("Hello from Shark MinIO test!")
	_, err = client.PutObject(ctx, bucketName, objectName, bytes.NewReader(content), int64(len(content)), minio.PutObjectOptions{})
	if err != nil {
		t.Fatalf("PutObject 失败: %v", err)
	}
	t.Log("PutObject 成功")

	// 下载并验证
	obj, err := client.GetObject(ctx, bucketName, objectName, minio.GetObjectOptions{})
	if err != nil {
		t.Fatalf("GetObject 失败: %v", err)
	}
	defer obj.Close()
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(obj)
	if err != nil {
		t.Fatalf("读取对象失败: %v", err)
	}
	if buf.String() != string(content) {
		t.Errorf("内容不匹配: got %q, want %q", buf.String(), string(content))
	}

	// 清理
	client.RemoveObject(ctx, bucketName, objectName, minio.RemoveObjectOptions{})
	client.RemoveBucket(ctx, bucketName)
	t.Log("MinIO CRUD 验证通过")
}
