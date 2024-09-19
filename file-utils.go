package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func CopyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return
	}
	defer func() {
		if e := out.Close(); e != nil {
			err = e
		}
	}()

	_, err = io.Copy(out, in)
	if err != nil {
		return
	}

	err = out.Sync()
	if err != nil {
		return
	}

	si, err := os.Stat(src)
	if err != nil {
		return
	}
	err = os.Chmod(dst, si.Mode())
	if err != nil {
		return
	}

	return
}

func CopyDir(src string, dst string) (err error) {
	src = filepath.Clean(src)
	dst = filepath.Clean(dst)

	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !si.IsDir() {
		return fmt.Errorf("source is not a directory")
	}

	_, err = os.Stat(dst)
	if err != nil && !os.IsNotExist(err) {
		return
	}
	if err == nil {
		return fmt.Errorf("destination already exists")
	}

	err = os.MkdirAll(dst, si.Mode())
	if err != nil {
		return
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			err = CopyDir(srcPath, dstPath)
			if err != nil {
				return
			}
		} else {
			info, _ := entry.Info()
			// Skip symlinks.
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}

			err = CopyFile(srcPath, dstPath)
			if err != nil {
				return
			}
		}
	}

	return
}

func ReadLastLine(filename string) (string, error) {
	fileStat, err := os.Stat(filename)

	if err != nil {
		return "", err
	}

	lastLine := ""
	byteBuffer := make([]byte, 1)

	pos := fileStat.Size() - 1

	fileHandle, err := os.Open(filename)

	if err != nil {
		return "", err
	}

	firstByte := true

	for {
		_, err := fileHandle.ReadAt(byteBuffer, pos)

		if err != nil {
			return "", nil
		}

		pos--
		char := fmt.Sprintf("%s", byteBuffer)

		if char == "\n" && firstByte {
			firstByte = false
			continue
		}

		if char == "\n" {
			break
		}

		lastLine = char + lastLine
	}

	return lastLine, nil
}
