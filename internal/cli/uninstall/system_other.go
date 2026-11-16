//go:build !windows

package main

import "errors"

// There is no user-PATH registry value or detached helper outside Windows;
// install.sh puts its PATH line in shell profiles instead.
func realPathStore() pathStore { return nil }

func spawnDetached(string) error { return errors.New("not supported on this system") }

func broadcastEnvironmentChange() {}
