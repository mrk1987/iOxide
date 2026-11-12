//go:build windows

package main

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func setProcessIdentity() {
	id, _ := windows.UTF16PtrFromString("io.github.mrk1987.ioxide.desktop")
	setID := windows.NewLazySystemDLL("shell32.dll").NewProc("SetCurrentProcessExplicitAppUserModelID")
	_, _, _ = setID.Call(uintptr(unsafe.Pointer(id)))
	runtime.KeepAlive(id)
}
