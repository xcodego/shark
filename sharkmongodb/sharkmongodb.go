// Package sharkmongodb 提供了 MongoDB 客户端的创建和连接管理。
//
// 封装了 MongoDB Go Driver v2 的连接创建、认证配置和健康检查，
// 为 Shark 应用提供开箱即用的 MongoDB 连接能力。
//
// 连接 URI 格式：mongodb://user:password@host/?authSource=admin
// 认证源固定为 admin 数据库，适用于大多数 MongoDB 部署场景。
//
// 使用示例：
//
//	// 创建 MongoDB 客户端
//	client, err := sharkmongodb.New(ctx, &sharkmongodb.Config{
//	    Host:     "127.0.0.1:27017",
//	    User:     "admin",
//	    Password: "password",
//	})
//	if err != nil {
//	    log.Fatalf("连接 MongoDB 失败: %v", err)
//	}
//	defer client.Disconnect(ctx)
//
//	// 使用客户端操作数据库
//	coll := client.Database("mydb").Collection("users")
//
//	// 插入文档
//	_, err = coll.InsertOne(ctx, bson.D{
//	    {Key: "name", Value: "张三"},
//	    {Key: "age", Value: 25},
//	    {Key: "email", Value: "zhangsan@example.com"},
//	})
//
//	// 查询文档
//	var result struct {
//	    Name  string `bson:"name"`
//	    Age   int    `bson:"age"`
//	    Email string `bson:"email"`
//	}
//	err = coll.FindOne(ctx, bson.D{{Key: "name", Value: "张三"}}).Decode(&result)
//	fmt.Printf("用户: %s, 年龄: %d\n", result.Name, result.Age)
//
//	// 更新文档
//	coll.UpdateOne(ctx,
//	    bson.D{{Key: "name", Value: "张三"}},
//	    bson.D{{Key: "$set", Value: bson.D{{Key: "age", Value: 26}}}},
//	)
//
//	// 删除文档
//	coll.DeleteOne(ctx, bson.D{{Key: "name", Value: "张三"}})
package sharkmongodb

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Config 是 MongoDB 的连接配置。
//
// 支持从 JSON、YAML、viper（mapstructure）等多种配置源加载。
// 所有字段均为必填（User/Password 为空时使用匿名访问）。
//
// 配置示例（config.yaml）：
//
//	mongodb:
//	  host: "192.168.1.100:27017"
//	  user: "root"
//	  password: "mysecret"
//
// 使用示例：
//
//	cfg := &sharkmongodb.Config{
//	    Host:     "127.0.0.1:27017",
//	    User:     "admin",
//	    Password: "securePassword",
//	}
//	client, err := sharkmongodb.New(ctx, cfg)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Disconnect(ctx)
type Config struct {
	// Host MongoDB 连接地址，格式为 "host:port"（如 "127.0.0.1:27017" 或 "mongo-cluster:27017"）
	Host string `json:"host" yaml:"host" mapstructure:"host"`

	// User 数据库用户名。为空时使用匿名访问（不推荐生产环境使用）
	User string `json:"user" yaml:"user" mapstructure:"user"`

	// Password 数据库密码。为空时使用匿名访问
	Password string `json:"password" yaml:"password" mapstructure:"password"`
}

// New 创建 MongoDB 客户端并验证连接。
//
// 连接配置：
//   - 认证源：固定为 admin 数据库（authSource=admin）
//   - 连接 URI 格式：mongodb://user:password@host/?authSource=admin
//   - 创建后立即执行 Ping 验证连接可用性
//
// 参数：
//   - ctx:    上下文，用于连接超时和健康检查控制
//   - config: MongoDB 连接配置，不能为 nil
//
// 返回值：
//   - *mongo.Client: 已通过 Ping 验证的 MongoDB 客户端实例
//   - error: 配置为空、连接失败或 Ping 不通时返回错误
//
// 使用示例：
//
//	// 基础用法
//	client, err := sharkmongodb.New(ctx, &sharkmongodb.Config{
//	    Host:     "127.0.0.1:27017",
//	    User:     "admin",
//	    Password: "password",
//	})
//	if err != nil {
//	    log.Fatalf("连接 MongoDB 失败: %v", err)
//	}
//	defer client.Disconnect(ctx)
//
//	// 匿名访问（无认证）
//	client, err = sharkmongodb.New(ctx, &sharkmongodb.Config{
//	    Host: "127.0.0.1:27017",
//	})
//
//	// 多数据库操作
//	db1 := client.Database("users_db")
//	db2 := client.Database("orders_db")
//
//	// 列出所有数据库
//	databases, err := client.ListDatabaseNames(ctx, bson.D{})
//	fmt.Printf("数据库列表: %v\n", databases)
//
//	// 检查连接状态
//	err = client.Ping(ctx, readpref.Primary())
//	if err != nil {
//	    log.Printf("MongoDB 连接异常: %v", err)
//	}
func New(ctx context.Context, config *Config) (*mongo.Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config required")
	}

	// 构建 MongoDB 连接 URI
	// 格式：mongodb://user:password@host/?authSource=admin
	uri := fmt.Sprintf("mongodb://%v:%v@%v/?authSource=admin", config.User, config.Password, config.Host)

	// 使用 MongoDB Go Driver v2 创建客户端
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}

	// Ping 验证连接可用性（使用默认的读偏好）
	err = client.Ping(ctx, nil)
	if err != nil {
		return nil, err
	}
	return client, nil
}
