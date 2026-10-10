//go:build darwin

package service

import "testing"

func TestMenuVersionRE(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"generated", menuInfoPlist("1.2.3"), "1.2.3"},
		// The format packaging/macos/Info.plist ships, after its __VERSION__
		// is filled in; the agent reads apps installed from the .pkg too.
		{"packaged", "<key>CFBundleShortVersionString</key><string>4.5.6</string>", "4.5.6"},
		{"spaced", "<key>CFBundleShortVersionString</key>\n    <string>7.8.9</string>", "7.8.9"},
		{"missing", "<key>CFBundleName</key><string>Orbit Agent</string>", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got string
			if m := menuVersionRE.FindStringSubmatch(c.in); m != nil {
				got = m[1]
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
