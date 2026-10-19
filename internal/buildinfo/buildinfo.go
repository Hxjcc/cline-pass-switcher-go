// Package buildinfo carries values injected by the release build.
package buildinfo

var Version = "dev"

func UserAgent() string {
	return "cline-pass-switcher-go/" + Version
}
