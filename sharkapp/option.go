package sharkapp

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/lornshark/shark/sharkdb"
	"github.com/lornshark/shark/sharkelastic"
	"github.com/lornshark/shark/sharketcd"
	"github.com/lornshark/shark/sharkkafka"
	"github.com/lornshark/shark/sharkminio"
	"github.com/lornshark/shark/sharkmongodb"
	"github.com/lornshark/shark/sharkrabbitmq"
	"github.com/lornshark/shark/sharkredis"
	"github.com/lornshark/shark/sharkrisingwave"
	"github.com/spf13/viper"
)

// Options 是应用的配置集合，包含了所有中间件的连接配置。
//
// 所有字段均为小写（私有），外部只能通过 NewOption() 加载，
// 或通过 WithXxx 方法链式覆盖单个配置项。
// 这种设计保证了配置来源的统一性和可控性。
//
// 配置加载优先级（由高到低）：
//  1. 环境变量（如 REDIS_HOST 覆盖 redis.host）
//  2. config.yaml 配置文件
//  3. 代码默认值
//
// 使用示例：
//
//	// 从本地配置文件加载
//	opts, err := sharkapp.NewOption("myproject", "game-server")
//	if err != nil {
//	    panic(err)
//	}
//
//	// 链式覆盖部分配置
//	opts.WithDB(&sharkdb.Config{Host: "custom-db:3306", User: "root", Password: "", Database: "mydb"}).
//	    WithRedis(&sharkredis.Config{Host: []string{"cache:6379"}, Password: ""})
//
//	// 传递给应用启动
//	app, err := sharkapp.New(opts)
type Options struct {
	id            string                  // 实例编号，默认 "1"
	db            *sharkdb.Config         // MySQL/GORM 数据库配置
	env           string                  // 运行环境: dev / test / prod
	name          string                  // 服务名称
	minio         *sharkminio.Config      // MinIO 对象存储配置
	timer         bool                    // 是否启用定时器
	pprof         int                     // pprof 端口
	kafka         *sharkkafka.Config      // Kafka 消息队列配置
	redis         *sharkredis.Config      // Redis 兼容模式配置（自动探测集群/单机）
	redis_cluster *sharkredis.Config      // Redis 集群模式配置
	redis_client  *sharkredis.Config      // Redis 单机/主从模式配置
	elastic       *sharkelastic.Config    // Elasticsearch 配置
	project       string                  // 项目名称
	mongodb       *sharkmongodb.Config    // MongoDB 配置
	rabbitmq      *sharkrabbitmq.Config   // RabbitMQ 配置
	grpc          int                     // gRPC 端口
	health        int                     // 健康检查端口
	risingwave    *sharkrisingwave.Config // RisingWave 配置
	etcd          *sharketcd.Config       // etcd 配置
	http          int                     // HTTP 端口
	rsaPrivateKey *rsa.PrivateKey         // RSA 私钥，用于解密配置中的密码
}

// -- WithXxx 链式配置方法 --

// WithDB 设置 MySQL 数据库配置。
// config 为 nil 时不做任何修改（保留现有配置）。
func (o *Options) WithDB(config *sharkdb.Config) *Options {
	if config == nil {
		return o
	}
	if config.Host == "" {
		return o
	}
	o.db = config
	return o
}

// WithRedis 设置 Redis 兼容模式配置（自动探测集群/单机）。
// config 为 nil 时不做任何修改。
func (o *Options) WithRedis(config *sharkredis.Config) *Options {
	if config == nil || len(config.Host) == 0 {
		return o
	}
	o.redis = config
	return o
}

// WithRedisCluster 设置 Redis 集群模式配置。
func (o *Options) WithRedisCluster(config *sharkredis.Config) *Options {
	if config == nil || len(config.Host) == 0 {
		return o
	}
	o.redis_cluster = config
	return o
}

// WithRedisClient 设置 Redis 单机/主从模式配置。
func (o *Options) WithRedisClient(config *sharkredis.Config) *Options {
	if config == nil || len(config.Host) == 0 {
		return o
	}
	o.redis_client = config
	return o
}

// WithKafka 设置 Kafka 消息队列配置。
func (o *Options) WithKafka(config *sharkkafka.Config) *Options {
	if config == nil || len(config.Host) == 0 {
		return o
	}
	o.kafka = config
	return o
}

// WithElastic 设置 Elasticsearch 配置。
func (o *Options) WithElastic(config *sharkelastic.Config) *Options {
	if config == nil || len(config.Host) == 0 {
		return o
	}
	o.elastic = config
	return o
}

// WithRabbitMQ 设置 RabbitMQ 配置。
func (o *Options) WithRabbitMQ(config *sharkrabbitmq.Config) *Options {
	if config == nil || len(config.Host) == 0 {
		return o
	}
	o.rabbitmq = config
	return o
}

// WithMongoDB 设置 MongoDB 配置。
func (o *Options) WithMongoDB(config *sharkmongodb.Config) *Options {
	if config == nil || config.Host == "" {
		return o
	}
	o.mongodb = config
	return o
}

// WithMinIO 设置 MinIO 配置。
func (o *Options) WithMinIO(config *sharkminio.Config) *Options {
	if config == nil || config.Host == "" {
		return o
	}
	o.minio = config
	return o
}

// WithRisingWave 设置 RisingWave 配置。
func (o *Options) WithRisingWave(config *sharkrisingwave.Config) *Options {
	if config == nil || config.Host == "" {
		return o
	}
	o.risingwave = config
	return o
}

// WithEtcd 设置 etcd 配置。
func (o *Options) WithEtcd(config *sharketcd.Config) *Options {
	if config == nil || len(config.Host) == 0 {
		return o
	}
	o.etcd = config
	return o
}

// WithHTTP 设置 HTTP 服务端口。
// port 必须在 1~65535 之间，否则设置无效。
func (o *Options) WithHTTP(port int) *Options {
	if port < 1 || port > 65535 {
		return o
	}
	o.http = port
	return o
}

// WithGrpc 设置 gRPC 服务端口。
// port 必须在 1~65535 之间，否则设置无效。
func (o *Options) WithGrpc(port int) *Options {
	if port < 1 || port > 65535 {
		return o
	}
	o.grpc = port
	return o
}

// WithHealth 设置健康检查服务端口。
// port 必须在 1~65535 之间，否则设置无效。
func (o *Options) WithHealth(port int) *Options {
	if port < 1 || port > 65535 {
		return o
	}
	o.health = port
	return o
}

// WithPprof 设置 pprof 性能分析端口。
// port 必须在 1~65535 之间，否则设置无效。
func (o *Options) WithPprof(port int) *Options {
	if port < 1 || port > 65535 {
		return o
	}
	o.pprof = port
	return o
}

// WithTimer 设置是否启用定时器。
func (o *Options) WithTimer(enable bool) *Options {
	o.timer = enable
	return o
}

// WithEnv 设置运行环境。
// 允许的值为 "dev"、"test"、"prod"。
func (o *Options) WithEnv(env string) *Options {
	switch env {
	case "dev", "test", "prod":
		o.env = env
	}
	return o
}

// WithID 设置实例编号。
func (o *Options) WithId(id string) *Options {
	if id == "" {
		return o
	}
	o.id = id
	return o
}

// parseRSAPrivateKey 解析 PEM 格式的 RSA 私钥（支持 PKCS1 和 PKCS8）。
func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("failed to parse PEM block")
	}
	// 先尝试 PKCS8
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
		return nil, fmt.Errorf("not an RSA private key (PKCS8)")
	}
	// 回退 PKCS1
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

// decryptPassword 使用 RSA 私钥解密密码。
// 对密码进行标准 PKCS#1 v1.5 解密：
// 密码是 Base64 编码的密文 → 解码 → RSA 解密，成功返回明文，失败返回原值。
func (o *Options) decryptPassword(password string) string {
	if o.rsaPrivateKey == nil || password == "" {
		return password
	}
	ciphertext, err := base64.StdEncoding.DecodeString(password)
	if err != nil {
		return password
	}
	plain, err := rsa.DecryptPKCS1v15(nil, o.rsaPrivateKey, ciphertext)
	if err != nil {
		return password
	}
	return string(plain)
}

// ========== 配置加载 ==========

// readSlices 从 viper 中读取字符串切片配置。
func readSlices(v *viper.Viper, key string) []string {
	ss := v.GetStringSlice(key)
	var result []string
	for _, s := range ss {
		for _, sub := range strings.Split(s, ",") {
			if t := strings.TrimSpace(sub); t != "" {
				result = append(result, t)
			}
		}
	}
	if len(result) == 0 {
		if s := strings.TrimSpace(v.GetString(key)); s != "" {
			for _, sub := range strings.Split(s, ",") {
				if t := strings.TrimSpace(sub); t != "" {
					result = append(result, t)
				}
			}
		}
	}
	return result
}

func NewOption(project string, name string) (*Options, error) {
	return NewOptionWithRsa(project, name, "")
}

// NewOptionWithRsa 从本地 YAML 配置文件（config.yaml）中加载应用配置。
//
// 配置加载流程:
//  1. 创建 viper 实例，读取当前目录或 ./config 目录下的 config.yaml
//  2. 同时支持环境变量覆盖（环境变量中 . 替换为 _，如 redis.host → REDIS_HOST）
//  3. 自动从环境变量 SHARK_PRIVATE_KEY 读取 RSA 私钥
//  4. 按照统一的 key 格式解析各中间件的连接信息，解析时对密码字段自动解密
//  5. 仅当 host 列表非空时才创建对应的 Config 实例
//
// 参数:
//   - project: 项目名称
//   - name:    服务名称
//
// 返回值:
//   - *Options: 配置对象
//   - error:    配置文件格式错误时返回
//
// 注意:
//   - 如果 config.yaml 文件不存在，不会报错，所有中间件配置为空
//   - 环境变量优先级高于配置文件
func NewOptionWithRsa(project string, name string, rsa string) (*Options, error) {
	if project == "" {
		return nil, fmt.Errorf("project required")
	}
	if name == "" {
		return nil, fmt.Errorf("name required")
	}

	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("./config")
	v.SetDefault("env", "dev")
	v.SetDefault("id", "1")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config file failed: %w", err)
		}
	}

	opts := &Options{
		project: project,
		name:    name,
		env:     strings.TrimSpace(v.GetString("env")),
		id:      strings.TrimSpace(v.GetString("id")),
		timer:   v.GetBool("timer"),
		pprof:   v.GetInt("pprof"),
		grpc:    v.GetInt("grpc"),
		health:  v.GetInt("health"),
		http:    v.GetInt("http"),
	}

	if rsa != "" {
		key := strings.TrimSpace(rsa)
		k, err := parseRSAPrivateKey(key)
		if err != nil {
			panic(fmt.Errorf("parse RSA private key failed: %w", err))
		}
		opts.rsaPrivateKey = k
	}

	// 校验端口范围
	if err := opts.validatePorts(); err != nil {
		return nil, err
	}

	// 解析各中间件配置（密码字段在读取时自动解密）
	opts.parseRedisCluster(v)
	opts.parseRedisClient(v)
	opts.parseRedis(v)
	opts.parseDB(v)
	opts.parseElastic(v)
	opts.parseMinIO(v)
	opts.parseKafka(v)
	opts.parseMongoDB(v)
	opts.parseRabbitMQ(v)
	opts.parseRisingWave(v)
	opts.parseEtcd(v)

	return opts, nil
}

// validatePorts 校验所有端口是否在有效范围内 (1~65535)。
// 端口为 0 表示未启用，跳过校验。
func (o *Options) validatePorts() error {
	ports := map[string]int{
		"http":   o.http,
		"grpc":   o.grpc,
		"health": o.health,
		"pprof":  o.pprof,
	}
	for name, port := range ports {
		if port == 0 {
			continue
		}
		if port < 1 || port > 65535 {
			return fmt.Errorf("invalid %s port: %d, must be 1~65535", name, port)
		}
	}
	return nil
}

// parseRedisCluster 解析 Redis 集群模式配置。
func (o *Options) parseRedisCluster(v *viper.Viper) {
	hosts := readSlices(v, "redis_cluster.host")
	if len(hosts) == 0 {
		return
	}
	o.redis_cluster = &sharkredis.Config{
		Host:         hosts,
		Password:     o.decryptPassword(strings.TrimSpace(v.GetString("redis_cluster.password"))),
		ReplaceFrom:  strings.TrimSpace(v.GetString("redis_cluster.replace_from")),
		ReplaceTo:    strings.TrimSpace(v.GetString("redis_cluster.replace_to")),
		PoolSize:     v.GetInt("redis_cluster.pool_size"),
		MinIdleConns: v.GetInt("redis_cluster.min_idle_conns"),
	}
}

// parseRedisClient 解析 Redis 单机/主从模式配置。
func (o *Options) parseRedisClient(v *viper.Viper) {
	hosts := readSlices(v, "redis_client.host")
	if len(hosts) == 0 {
		return
	}
	o.redis_client = &sharkredis.Config{
		Host:         hosts,
		Password:     o.decryptPassword(strings.TrimSpace(v.GetString("redis_client.password"))),
		ReplaceFrom:  strings.TrimSpace(v.GetString("redis_client.replace_from")),
		ReplaceTo:    strings.TrimSpace(v.GetString("redis_client.replace_to")),
		PoolSize:     v.GetInt("redis_client.pool_size"),
		MinIdleConns: v.GetInt("redis_client.min_idle_conns"),
	}
}

// parseRedis 解析 Redis 兼容模式配置。
func (o *Options) parseRedis(v *viper.Viper) {
	hosts := readSlices(v, "redis.host")
	if len(hosts) == 0 {
		return
	}
	o.redis = &sharkredis.Config{
		Host:         hosts,
		Password:     o.decryptPassword(strings.TrimSpace(v.GetString("redis.password"))),
		ReplaceFrom:  strings.TrimSpace(v.GetString("redis.replace_from")),
		ReplaceTo:    strings.TrimSpace(v.GetString("redis.replace_to")),
		PoolSize:     v.GetInt("redis.pool_size"),
		MinIdleConns: v.GetInt("redis.min_idle_conns"),
	}
}

// parseDB 解析 MySQL 数据库配置。
func (o *Options) parseDB(v *viper.Viper) {
	hosts := readSlices(v, "db.host")
	if len(hosts) == 0 {
		return
	}
	o.db = &sharkdb.Config{
		Host:                  hosts[0],
		User:                  strings.TrimSpace(v.GetString("db.user")),
		Password:              o.decryptPassword(strings.TrimSpace(v.GetString("db.password"))),
		Database:              strings.TrimSpace(v.GetString("db.database")),
		MaxIdleConns:          v.GetInt("db.max_idle_conns"),
		MaxOpenConns:          v.GetInt("db.max_open_conns"),
		ConnMaxIdleMinute:     v.GetInt("db.conn_max_idle_minute"),
		ConnMaxLifetimeMinute: v.GetInt("db.conn_max_lifetime_minute"),
	}
}

// parseElastic 解析 Elasticsearch 配置。
func (o *Options) parseElastic(v *viper.Viper) {
	hosts := readSlices(v, "elastic.host")
	if len(hosts) == 0 {
		return
	}
	o.elastic = &sharkelastic.Config{
		Host:     hosts,
		User:     strings.TrimSpace(v.GetString("elastic.user")),
		Password: o.decryptPassword(strings.TrimSpace(v.GetString("elastic.password"))),
	}
}

// parseMinIO 解析 MinIO 配置。
func (o *Options) parseMinIO(v *viper.Viper) {
	hosts := readSlices(v, "minio.host")
	if len(hosts) == 0 {
		return
	}
	o.minio = &sharkminio.Config{
		Host:     hosts[0],
		User:     strings.TrimSpace(v.GetString("minio.user")),
		Password: o.decryptPassword(strings.TrimSpace(v.GetString("minio.password"))),
	}
}

// parseKafka 解析 Kafka 配置。
func (o *Options) parseKafka(v *viper.Viper) {
	hosts := readSlices(v, "kafka.host")
	if len(hosts) == 0 {
		return
	}
	o.kafka = &sharkkafka.Config{
		Host:     hosts,
		User:     strings.TrimSpace(v.GetString("kafka.user")),
		Password: o.decryptPassword(strings.TrimSpace(v.GetString("kafka.password"))),
		TLS:      v.GetBool("kafka.tls"),
	}
}

// parseMongoDB 解析 MongoDB 配置。
func (o *Options) parseMongoDB(v *viper.Viper) {
	hosts := readSlices(v, "mongodb.host")
	if len(hosts) == 0 {
		return
	}
	o.mongodb = &sharkmongodb.Config{
		Host:     hosts[0],
		User:     strings.TrimSpace(v.GetString("mongodb.user")),
		Password: o.decryptPassword(strings.TrimSpace(v.GetString("mongodb.password"))),
	}
}

// parseRabbitMQ 解析 RabbitMQ 配置。
func (o *Options) parseRabbitMQ(v *viper.Viper) {
	hosts := readSlices(v, "rabbitmq.host")
	if len(hosts) == 0 {
		return
	}
	o.rabbitmq = &sharkrabbitmq.Config{
		Host:     hosts,
		User:     strings.TrimSpace(v.GetString("rabbitmq.user")),
		Password: o.decryptPassword(strings.TrimSpace(v.GetString("rabbitmq.password"))),
	}
}

// parseRisingWave 解析 RisingWave 配置。
func (o *Options) parseRisingWave(v *viper.Viper) {
	hosts := readSlices(v, "risingwave.host")
	if len(hosts) == 0 {
		return
	}
	o.risingwave = &sharkrisingwave.Config{
		Host:     hosts[0],
		User:     strings.TrimSpace(v.GetString("risingwave.user")),
		Password: o.decryptPassword(strings.TrimSpace(v.GetString("risingwave.password"))),
		Database: strings.TrimSpace(v.GetString("risingwave.database")),
	}
}

// parseEtcd 解析 etcd 配置。
func (o *Options) parseEtcd(v *viper.Viper) {
	hosts := readSlices(v, "etcd.host")
	if len(hosts) == 0 {
		return
	}
	o.etcd = &sharketcd.Config{
		Host:     hosts,
		User:     strings.TrimSpace(v.GetString("etcd.user")),
		Password: o.decryptPassword(strings.TrimSpace(v.GetString("etcd.password"))),
	}
}
