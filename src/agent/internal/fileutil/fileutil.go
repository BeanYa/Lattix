// Package fileutil 提供 agent 各组件共用的文件落盘原语：
// 同目录临时文件 + rename 的原子写入/复制，避免进程崩溃留下半截文件。
package fileutil

import (
	"io"
	"os"
)

// WriteFileAtomic 将 data 原子写入 path（先写 path+".tmp" 再 rename），
// perm 为目标文件权限；rename 失败时清理临时文件。
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// CopyFileAtomic 复制文件并设置权限（目标先写临时文件再原子替换）。
func CopyFileAtomic(src, dest string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dest + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}
