package api

import "strings"

// ValidateProjectName is the project name rule shared by both transports
// (HTML form and JSON API): required, ≤80 chars after trimming.
//
// It sits in package api but belongs to the resource, not to the transport:
// the rule is the resource's, and the app's own form handler reads it. Keeping
// it here rather than in the transport payload is what lets a project install
// the projects resource without installing its JSON API — the two are separate
// modules, and the API one depends on this, never the other way round.
func ValidateProjectName(name string) (string, string) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return name, "Name is required."
	case len(name) > 80:
		return name, "Name must be 80 characters or fewer."
	default:
		return name, ""
	}
}
