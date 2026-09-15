package test

import (
	"context"
	"testing"
	"time"

	"github.com/lornshark/shark/sharkmongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestMongoNew(t *testing.T) {
	cfg := loadMongoConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := sharkmongodb.New(ctx, cfg)
	if err != nil {
		t.Fatalf("连接 MongoDB 失败: %v", err)
	}
	defer func() {
		disCtx, disCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer disCancel()
		client.Disconnect(disCtx)
	}()
	t.Log("MongoDB 连接成功")

	// 写入测试文档
	coll := client.Database("test_shark").Collection("test_collection")
	doc := bson.D{{Key: "name", Value: "shark-test"}, {Key: "ts", Value: time.Now().Unix()}}
	_, err = coll.InsertOne(ctx, doc)
	if err != nil {
		t.Fatalf("MongoDB InsertOne 失败: %v", err)
	}
	t.Log("InsertOne 成功")

	// 查询
	var result struct {
		Name string `bson:"name"`
	}
	err = coll.FindOne(ctx, bson.D{{Key: "name", Value: "shark-test"}}).Decode(&result)
	if err != nil {
		t.Fatalf("MongoDB FindOne 失败: %v", err)
	}
	if result.Name != "shark-test" {
		t.Errorf("查询结果 = %s, want shark-test", result.Name)
	}

	// 清理
	coll.Drop(ctx)
	t.Log("MongoDB CRUD 验证通过")
}
