//go:build !darwin

package main

func notifyUser(title, body string) (bool, string) {
	return false, "notifications not supported on this platform"
}
