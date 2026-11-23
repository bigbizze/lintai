package observe

import "golang.org/x/sys/windows"

func isReparsePoint(path string) bool {
	// Match the compiler's reparse-point probe for Windows long paths.
	if len(path) >= 248 {
		path = `\\?\` + path
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attributes, err := windows.GetFileAttributes(name)
	return err == nil && attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
