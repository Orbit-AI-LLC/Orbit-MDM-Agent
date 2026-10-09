//go:build windows

package main

import "golang.org/x/sys/windows/registry"

// managedEnrollment is the server and install token a group policy or an MDM
// set for the agent: the Server and Token values of
// HKLM\SOFTWARE\Policies\Orbit\Agent. (The .msi also takes them as SERVER and
// TOKEN on its command line.)
func managedEnrollment() (server, token string) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Policies\Orbit\Agent`, registry.QUERY_VALUE)
	if err != nil {
		return "", ""
	}
	defer key.Close()
	server, _, _ = key.GetStringValue("Server")
	token, _, _ = key.GetStringValue("Token")
	return server, token
}
