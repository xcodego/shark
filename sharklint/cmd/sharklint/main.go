// 命令 sharklint 对指定目录运行全部源码检查, 用法: sharklint <目录>。
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

// run 执行检查并返回进程退出码: 0 通过, 1 检查失败, 2 参数错误; 错误信息写入 stderr。
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

// XTtes 声明 XTest 方法; 目前未被仓库内其他代码引用。
type XTtes interface {
	XTest() error
}
