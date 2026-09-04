// Package clerkurl derives Clerk's Frontend API origin from the application
// URL.
//
// It is a leaf on purpose. The generated config loader calls FrontendAPIURL at
// load time, because the origin has to be resolved before internal/web reads
// it for the CSP connect-src directive — so this package may not import
// anything that imports internal/config, which rules out the Clerk adapter
// itself. Three lines of Go in a package of their own is the price of keeping
// the derivation with the module that owns the key: remove
// ggg/system/identity-clerk and the declaration, the import and the call all
// go with it.
package clerkurl

import "strings"

// FrontendAPIURL is the origin clerk-js talks to, which is exactly what CSP
// connect-src must allow. Clerk fronts the Frontend API at clerk.<domain> on a
// production instance and at a shared wildcard dev host otherwise. It is
// derived from APP_URL rather than asked for, because getting it wrong breaks
// CSP in a way that is hard to read.
func FrontendAPIURL(environment, appURL string) string {
	if environment != "production" {
		return "https://*.clerk.accounts.dev"
	}
	host := strings.TrimPrefix(strings.TrimPrefix(appURL, "https://"), "http://")
	return "https://clerk." + host
}
