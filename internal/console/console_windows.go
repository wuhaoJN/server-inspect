//go:build windows

// Package console 负责终端编码适配，避免 Windows 控制台中文乱码。
package console

import "golang.org/x/sys/windows"

// EnableUTF8 将 Windows 控制台输入/输出代码页切换为 UTF-8（65001）。
func EnableUTF8() {
	_ = windows.SetConsoleOutputCP(65001)
	_ = windows.SetConsoleCP(65001)
}
