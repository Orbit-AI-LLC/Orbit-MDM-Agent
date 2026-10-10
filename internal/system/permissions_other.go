//go:build !darwin

package system

import "context"

// permissions is macOS-only (privacy permissions, TCC). Elsewhere the agent runs
// with the rights it needs (SYSTEM on Windows, root on Linux), so there is
// nothing to report.
func permissions(ctx context.Context) map[string]string { return nil }

// RequestPermissions is macOS-only (privacy permissions, TCC); elsewhere the
// agent already has the rights it needs.
func RequestPermissions(ctx context.Context) {}

// ConsoleUser is macOS-only (it drives when the agent re-asks for permissions);
// elsewhere nothing reads it.
func ConsoleUser() string { return "" }
