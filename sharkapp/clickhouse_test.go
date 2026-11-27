package sharkapp

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spf13/viper"
	"github.com/xcodego/shark/sharkclickhouse"
)

func TestClickHouseConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("clickhouse:\n  host: \"host1:9000,host2:9000\"\n  user: default\n  database: analytics\n  tls: true\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLICKHOUSE_PASSWORD", "from-env")
	opts, err := NewOptionWithConfigFile("project", "app", path)
	if err != nil {
		t.Fatal(err)
	}
	want := &sharkclickhouse.Config{
		Host: []string{"host1:9000", "host2:9000"}, User: "default",
		Password: "from-env", Database: "analytics", TLS: true,
	}
	if !reflect.DeepEqual(opts.clickhouse, want) {
		t.Fatalf("config = %+v, want %+v", opts.clickhouse, want)
	}
	opts.WithClickHouse(nil).WithClickHouse(&sharkclickhouse.Config{})
	if !reflect.DeepEqual(opts.clickhouse, want) {
		t.Fatal("empty config replaced existing config")
	}
	override := &sharkclickhouse.Config{Host: []string{"localhost:9000"}}
	if opts.WithClickHouse(override) != opts || opts.clickhouse != override {
		t.Fatal("WithClickHouse did not set config")
	}
}

func TestClickHousePasswordDecryption(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	password, err := rsa.EncryptPKCS1v15(rand.Reader, &key.PublicKey, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	v := viper.New()
	v.Set("clickhouse.host", "localhost:9000")
	v.Set("clickhouse.password", base64.StdEncoding.EncodeToString(password))
	opts := &Options{rsaPrivateKey: key}
	opts.parseClickHouse(v)
	if opts.clickhouse == nil || opts.clickhouse.Password != "secret" {
		t.Fatal("ClickHouse password was not decrypted")
	}
}

func TestClickHouseConnectionFailure(t *testing.T) {
	opts := (&Options{}).WithClickHouse(&sharkclickhouse.Config{Host: []string{""}})
	if _, err := New(opts); err == nil {
		t.Fatal("New succeeded with an invalid ClickHouse address")
	}
}

func TestClickHouseConnection(t *testing.T) {
	host := os.Getenv("CLICKHOUSE_TEST_HOST")
	if host == "" {
		t.Skip("set CLICKHOUSE_TEST_HOST to test a live connection")
	}
	opts := (&Options{}).WithClickHouse(&sharkclickhouse.Config{
		Host:     []string{host},
		User:     os.Getenv("CLICKHOUSE_TEST_USER"),
		Password: os.Getenv("CLICKHOUSE_TEST_PASSWORD"),
		Database: os.Getenv("CLICKHOUSE_TEST_DATABASE"),
	})
	app, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.AntPool.Release()
	if app.ClickHouse == nil {
		t.Fatal("ClickHouse client was not initialized")
	}
	defer func() {
		if err := app.ClickHouse.Close(); err != nil {
			t.Error(err)
		}
	}()
	var result uint8
	if err := app.ClickHouse.QueryRow(app.Context, "SELECT toUInt8(1)").Scan(&result); err != nil {
		t.Fatal(err)
	}
	if result != 1 {
		t.Fatalf("result = %d, want 1", result)
	}
}
