// Package env reads environment variables for the command line.
package env

import "os"

// Or returns the environment variable key, or fallback when it is empty or unset.
func Or(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
