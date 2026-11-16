//go:build windows

package uninstall

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// registryPath is the user's PATH: HKCU\Environment, value "Path".
type registryPath struct{}

func realPathStore() pathStore { return registryPath{} }

func (registryPath) Get() (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer key.Close()
	value, _, err := key.GetStringValue("Path")
	if err == registry.ErrNotExist {
		return "", nil
	}
	return value, err
}

// Set keeps the value's type: install.ps1 and Windows tools usually store
// REG_EXPAND_SZ so %VARIABLES% in the entries keep working.
func (registryPath) Set(value string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	_, typ, err := key.GetStringValue("Path")
	if err == nil && typ == registry.SZ {
		return key.SetStringValue("Path", value)
	}
	return key.SetExpandStringValue("Path", value)
}

// spawnDetached starts cmdline (a full cmd.exe command line) without a console
// window and without tying it to this process, so it outlives us.
func spawnDetached(cmdline string) error {
	c := exec.Command("cmd.exe")
	c.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       cmdline,
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}

// broadcastEnvironmentChange tells running programs (Explorer) that the user
// environment changed. Best effort: new terminals read the registry anyway.
func broadcastEnvironmentChange() {
	env, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	send := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	const hwndBroadcast, wmSettingChange, smtoAbortIfHung = 0xffff, 0x001A, 0x0002
	var result uintptr
	send.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&result)))
}
