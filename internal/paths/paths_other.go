//go:build !windows

package paths

func downloadsDirOS() string { return xdgDownloads() }
