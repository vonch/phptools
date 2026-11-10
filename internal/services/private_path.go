package services

// ProtectPrivatePath applies the runtime's existing private-file ACL policy.
func ProtectPrivatePath(path string, directory bool) error {
	return protectPath(path, directory)
}
