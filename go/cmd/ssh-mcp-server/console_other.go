//go:build !windows

package main

// ownsConsole is only meaningful on Windows, where a double-clicked program
// gets a console window that closes as soon as it exits.
func ownsConsole() bool { return false }
