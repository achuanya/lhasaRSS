// 作者: 游钓四方 <haibao1027@gmail.com>
// 文件: wrap_error.go
// 说明: 一个帮助函数, 在返回错误时, 追加文件名和行号

package main

import (
	"fmt"
	"runtime"
)

// wrapErrorf 用于在错误信息里加上调用处的文件名和行号
// 【游钓四方 <haibao1027@gmail.com>】
// 参数:
//   - err: 原始错误
//   - format, args: 类似fmt.Printf, 用于生成更详细的错误描述
//
// 返回:
//   - error: 带有文件名和行号的新错误
func wrapErrorf(err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	_, file, line, _ := runtime.Caller(1)
	msg := fmt.Sprintf(format, args...)
	return fmt.Errorf("%s:%d => %s | 原因: %w", file, line, msg, err)
}
