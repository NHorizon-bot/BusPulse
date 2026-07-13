// Package errutil 提供错误包装与判断工具，与业务语义无关。
package errutil

import (
	"errors"
	"fmt"
)

// Wrap 为 err 包装一层带 op 前缀的上下文信息。
// 若 err 为 nil，返回 nil。
func Wrap(err error, op string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", op, err)
}

// Wrapf 为 err 包装带格式化消息的上下文。
func Wrapf(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf(format+": %w", append(args, err)...)
}

// Is 是 errors.Is 的直接别名，暴露到本包便于统一 import。
var Is = errors.Is

// As 是 errors.As 的直接别名。
var As = errors.As
