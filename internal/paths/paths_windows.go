package paths

import "golang.org/x/sys/windows"

// downloadsDirOS asks Windows for the real download folder, which the user
// may have moved to another drive.
func downloadsDirOS() string {
	p, err := windows.KnownFolderPath(windows.FOLDERID_Downloads, 0)
	if err != nil {
		return ""
	}
	return p
}

// documentsDirOS asks Windows for the documents folder, which OneDrive or
// the user may have moved.
func documentsDirOS() string {
	p, err := windows.KnownFolderPath(windows.FOLDERID_Documents, 0)
	if err != nil {
		return ""
	}
	return p
}
