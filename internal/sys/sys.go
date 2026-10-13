// Package sys wraps the few operating system features the program needs:
// opening folders and links, starting programs, finding running games and
// showing a folder picker.
package sys

import "errors"

// ErrNoPicker is returned when no folder picker is available.
var ErrNoPicker = errors.New("no folder picker available")

// ErrCancelled is returned when the user closes the folder picker.
var ErrCancelled = errors.New("cancelled")
