//go:build !windows

package paths

func downloadsDirOS() string { return xdgDownloads() }

// Linux games of Windows origin only use a documents folder inside their
// Proton prefix, so the home folder's is rarely needed.
func documentsDirOS() string { return "" }
