package main

import (
	"github.com/xcodego/shark/sharkapp"
	"github.com/xcodego/shark/sharkdb"
	"go.uber.org/zap"
)

// @title game-demo API
// @version 1.0

// @BasePath  /api

// @securityDefinitions.apiKey ApiKeyAuth
// @in header
// @name x-token

// go get -u github.com/swaggo/swag/cmd/swag
// go install github.com/swaggo/swag/cmd/swag@v1.16.4
// swag v1.16.4

// swag init --parseDependency -g  main.go

func main() {
	options, err := sharkapp.NewOption("kgame", "game-test")
	if err != nil {
		panic(err)
	}
	app, err := sharkapp.New(options)
	if err != nil {
		panic(err)
	}
	app.Hunt(&Test{svc: app})
}

// Test is the example application's service.
type Test struct {
	svc *sharkapp.App
	cdc *sharkdb.BinlogListener
}

// Start is called when the application starts.
func (t *Test) Start() {
	go t.Binlog()
}

func (t *Test) Binlog() {
	t.cdc = sharkdb.NewBinlogListener(&sharkdb.BinlogConfig{
		Addr:     "192.168.191.100:3306",
		User:     "root",
		Password: "CEki57pxTJyYaLD",
		Logger:   t.svc.Logger,
		Redis:    t.svc.RedisCluster,
		PosKey:   "binlog_pos",
		Database: "kgame",
	})
	t.cdc.OnInsert(func(table string, data map[string]any) {
		t.svc.Logger.Info("OnInsert", zap.String("table", table), zap.Any("data", data))
	})
	t.cdc.OnUpdate(func(table string, data map[string]any) {
		t.svc.Logger.Info("OnUpdate", zap.String("table", table), zap.Any("data", data))
	})
	t.cdc.OnDelete(func(table string, data map[string]any) {
		t.svc.Logger.Info("OnDelete", zap.String("table", table), zap.Any("data", data))
	})
	err := t.cdc.Start()
	if err != nil {
		t.svc.Logger.Error("Failed to start binlog listener", zap.Error(err))
	}
}
