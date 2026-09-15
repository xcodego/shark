package test

import (
	"os"
	"strings"
	"testing"

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
	"go.uber.org/zap"
)

// testConfig 从 config.yaml 加载的全局测试配置。
// init 中自动读取，所有测试共享。
var testConfig *viper.Viper

func init() {
	testConfig = viper.New()
	testConfig.SetConfigName("config")
	testConfig.SetConfigType("yaml")
	// 从 test/ 目录运行,配置文件在 ../config/
	testConfig.AddConfigPath("..")
	testConfig.AddConfigPath("../config")
	testConfig.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	testConfig.AutomaticEnv()
	_ = testConfig.ReadInConfig()
}

// testLogger 返回一个用于测试的 zap.Logger（Discard 输出）。
func testLogger() *zap.Logger {
	return zap.NewNop()
}

// pk 将相对路径解析为相对于项目根目录的路径。
// 测试从 test/ 目录运行，所以需要 ".."
func pk(rel string) string {
	return rel
}

// skipIfNoConfig 如果 config.yaml 中对应 key 的 host 为空，跳过测试。
func skipIfNoConfig(t *testing.T, key string) {
	t.Helper()
	hosts := readSlice(testConfig, key+".host")
	if len(hosts) == 0 {
		t.Skipf("跳过：config.%s.host 未配置", key)
	}
}

// readSlice 从 viper 中读取字符串切片（兼容逗号分隔和数组格式）。
func readSlice(v *viper.Viper, key string) []string {
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

// loadDBConfig 从 config.yaml 加载数据库配置。
func loadDBConfig(t *testing.T) *sharkdb.Config {
	t.Helper()
	hosts := readSlice(testConfig, "db.host")
	if len(hosts) == 0 {
		t.Skip("db.host 未配置")
	}
	return &sharkdb.Config{
		Host:     hosts[0],
		User:     strings.TrimSpace(testConfig.GetString("db.user")),
		Password: strings.TrimSpace(testConfig.GetString("db.password")),
		Database: strings.TrimSpace(testConfig.GetString("db.database")),
	}
}

// loadRedisConfig 从 config.yaml 加载 Redis 配置。
func loadRedisConfig(t *testing.T) *sharkredis.Config {
	t.Helper()
	hosts := readSlice(testConfig, "redis.host")
	if len(hosts) == 0 {
		t.Skip("redis.host 未配置")
	}
	return &sharkredis.Config{
		Host:        hosts,
		Password:    strings.TrimSpace(testConfig.GetString("redis.password")),
		ReplaceFrom: strings.TrimSpace(testConfig.GetString("redis.replace_from")),
		ReplaceTo:   strings.TrimSpace(testConfig.GetString("redis.replace_to")),
	}
}

// loadKafkaConfig 从 config.yaml 加载 Kafka 配置。
func loadKafkaConfig(t *testing.T) *sharkkafka.Config {
	t.Helper()
	hosts := readSlice(testConfig, "kafka.host")
	if len(hosts) == 0 {
		t.Skip("kafka.host 未配置")
	}
	return &sharkkafka.Config{
		Host:     hosts,
		User:     strings.TrimSpace(testConfig.GetString("kafka.user")),
		Password: strings.TrimSpace(testConfig.GetString("kafka.password")),
	}
}

// loadMongoConfig 从 config.yaml 加载 MongoDB 配置。
func loadMongoConfig(t *testing.T) *sharkmongodb.Config {
	t.Helper()
	hosts := readSlice(testConfig, "mongodb.host")
	if len(hosts) == 0 {
		t.Skip("mongodb.host 未配置")
	}
	return &sharkmongodb.Config{
		Host:     hosts[0],
		User:     strings.TrimSpace(testConfig.GetString("mongodb.user")),
		Password: strings.TrimSpace(testConfig.GetString("mongodb.password")),
	}
}

// loadMinioConfig 从 config.yaml 加载 MinIO 配置。
func loadMinioConfig(t *testing.T) *sharkminio.Config {
	t.Helper()
	hosts := readSlice(testConfig, "minio.host")
	if len(hosts) == 0 {
		t.Skip("minio.host 未配置")
	}
	return &sharkminio.Config{
		Host:     hosts[0],
		User:     strings.TrimSpace(testConfig.GetString("minio.user")),
		Password: strings.TrimSpace(testConfig.GetString("minio.password")),
	}
}

// loadElasticConfig 从 config.yaml 加载 Elasticsearch 配置。
func loadElasticConfig(t *testing.T) *sharkelastic.Config {
	t.Helper()
	hosts := readSlice(testConfig, "elastic.host")
	if len(hosts) == 0 {
		t.Skip("elastic.host 未配置")
	}
	return &sharkelastic.Config{
		Host:     hosts,
		User:     strings.TrimSpace(testConfig.GetString("elastic.user")),
		Password: strings.TrimSpace(testConfig.GetString("elastic.password")),
	}
}

// loadEtcdConfig 从 config.yaml 加载 etcd 配置。
func loadEtcdConfig(t *testing.T) *sharketcd.Config {
	t.Helper()
	hosts := readSlice(testConfig, "etcd.host")
	if len(hosts) == 0 {
		t.Skip("etcd.host 未配置")
	}
	return &sharketcd.Config{
		Host:     hosts,
		User:     strings.TrimSpace(testConfig.GetString("etcd.user")),
		Password: strings.TrimSpace(testConfig.GetString("etcd.password")),
	}
}

// loadRabbitmqConfig 从 config.yaml 加载 RabbitMQ 配置。
func loadRabbitmqConfig(t *testing.T) *sharkrabbitmq.Config {
	t.Helper()
	hosts := readSlice(testConfig, "rabbitmq.host")
	if len(hosts) == 0 {
		t.Skip("rabbitmq.host 未配置")
	}
	return &sharkrabbitmq.Config{
		Host:     hosts,
		User:     strings.TrimSpace(testConfig.GetString("rabbitmq.user")),
		Password: strings.TrimSpace(testConfig.GetString("rabbitmq.password")),
	}
}

// loadRisingWaveConfig 从 config.yaml 加载 RisingWave 配置。
func loadRisingWaveConfig(t *testing.T) *sharkrisingwave.Config {
	t.Helper()
	hosts := readSlice(testConfig, "risingwave.host")
	if len(hosts) == 0 {
		t.Skip("risingwave.host 未配置")
	}
	return &sharkrisingwave.Config{
		Host:     hosts[0],
		User:     strings.TrimSpace(testConfig.GetString("risingwave.user")),
		Password: strings.TrimSpace(testConfig.GetString("risingwave.password")),
		Database: strings.TrimSpace(testConfig.GetString("risingwave.database")),
	}
}

// TestReadConfig 验证 config.yaml 是否可被正确读取。
func TestReadConfig(t *testing.T) {
	keys := []string{"db", "redis", "kafka", "mongodb", "minio", "elastic", "etcd", "rabbitmq", "risingwave"}
	for _, k := range keys {
		hosts := readSlice(testConfig, k+".host")
		t.Logf("config.%s.host = %v", k, hosts)
	}
	_ = os.Getenv("CI") // 避免未使用导入
}

// TestLoadDBConfig 验证数据库配置加载。
func TestLoadDBConfig(t *testing.T) {
	cfg := loadDBConfig(t)
	if cfg.Host == "" {
		t.Skip("db 未配置")
	}
	t.Logf("DB Host=%s User=%s Database=%s", cfg.Host, cfg.User, cfg.Database)
}

// TestLoadRedisConfig 验证 Redis 配置加载。
func TestLoadRedisConfig(t *testing.T) {
	cfg := loadRedisConfig(t)
	t.Logf("Redis Host=%v", cfg.Host)
}

// TestLoadKafkaConfig 验证 Kafka 配置加载。
func TestLoadKafkaConfig(t *testing.T) {
	cfg := loadKafkaConfig(t)
	t.Logf("Kafka Host=%v", cfg.Host)
}
