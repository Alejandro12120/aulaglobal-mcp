package selfupdate

import "os"

// dockerEnvFile exists inside Docker containers; a variable so tests can point elsewhere.
var dockerEnvFile = "/.dockerenv"

// InContainer reports whether the process runs in a container image, where the
// binary must not be replaced in place. The Dockerfile sets
// AULAGLOBAL_IN_CONTAINER=1; /.dockerenv covers other Docker-built images.
func InContainer() bool {
	if os.Getenv("AULAGLOBAL_IN_CONTAINER") != "" {
		return true
	}
	_, err := os.Stat(dockerEnvFile)
	return err == nil
}
