package main

import (
	"syscall"
	"unsafe"
)

// ownsConsole reports whether this process is the only one attached to its
// console window, which means it was started by double-clicking it in
// Explorer rather than from a terminal.
func ownsConsole() bool {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleProcessList")
	if proc.Find() != nil {
		return false
	}
	var pids [2]uint32
	count, _, _ := proc.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return count == 1
}
