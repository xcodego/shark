package main

import (
	"github.com/xcodego/shark/sharkapp"
	"github.com/xcodego/shark/sharkdb"
)

// @title game-demo API
// @version 1.0

// @BasePath  /api

// @securityDefinitions.apiKey ApiKeyAuth
// @in header
// @name x-token

// 生成 swagger 文档：
//   go install github.com/swaggo/swag/cmd/swag@v1.16.4
//   swag init --parseDependency -g main.go

// main 加载配置、创建 App 并启动示例组件，阻塞直到收到退出信号。
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

// Test 是示例应用的业务组件，实现 sharkapp.AppComponent。
type Test struct {
	svc *sharkapp.App
	cdc *sharkdb.BinlogListener
}

// Start 在应用启动时由 Hunt 调用，不阻塞。
func (t *Test) Start() {
	go t.Test()
}

// Test 是示例业务入口，留空供使用者填充。
func (t *Test) Test() {

}
