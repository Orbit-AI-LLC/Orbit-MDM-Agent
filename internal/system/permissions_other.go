//go:build !darwin

package system

import "context"

// permissions is macOS-only (privacy permissions, TCC). Elsewhere the agent runs
// with the rights it needs (SYSTEM on Windows, root on Linux), so there is
// nothing to report.
func permissions(ctx context.Context) map[string]string { return nil }
