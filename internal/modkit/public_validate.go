package modkit

// ValidateManifest applies the published schema's semantic checks to an
// authored manifest before it enters a mutable project registry.
func ValidateManifest(manifest Manifest) error {
	return validateManifest(manifest, true)
}
