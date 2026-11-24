package main

import (
	"fmt"
	"io"
	"os"

	"github.com/xcodego/shark/sharklint"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "用法: sharklint <目录>")
		return 2
	}
	if err := sharklint.Lint(args[0]); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

type XTtes interface {
	XTest() error
}
