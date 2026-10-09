//go:build !darwin && !windows

package main

// managedEnrollment: Linux has no MDM settings to read; packages pass
// --server and --token.
func managedEnrollment() (server, token string) { return "", "" }
