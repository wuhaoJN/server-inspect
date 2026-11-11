//go:build !windows

// Package console 负责终端编码适配，避免 Windows 控制台中文乱码。
package console

// EnableUTF8 在非 Windows 平台无需处理。
func EnableUTF8() {}
