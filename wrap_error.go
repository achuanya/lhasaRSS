// Author: 游钓四方 <haibao1027@gmail.com>
// File: wrap_error.go
// Description: 一个帮助函数, 在返回错误时, 追加文件名和行号

package main

import (
	"fmt"
	"runtime"
)

// wrapErrorf 用于在错误信息里加上调用处的文件名和行号
//
// Description:
//
//	本函数在生成错误时，通过 runtime.Caller(1) 获取到上层调用位置的文件名和行号，
//	并将其包装到最终错误信息中，方便定位问题
//
// Parameters:
//   - err           : 原始错误
//   - format, args  : 类似 fmt.Printf 的格式化字符串和参数，用于生成更详细的错误描述
//
// Returns:
//   - error: 一个带有文件名、行号信息的错误对象
func wrapErrorf(err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	_, file, line, _ := runtime.Caller(1) // 获取上层调用者的文件名和行号
	msg := fmt.Sprintf(format, args...)
	return fmt.Errorf("%s:%d => %s | 原因: %w", file, line, msg, err)
}
