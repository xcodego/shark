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
	go t.Test()
}

func (t *Test) Test() {

}
