package lifecycle

import "errors"

// ErrAlreadyRunning distinguishes an existing owner from permission/I/O failure.
var ErrAlreadyRunning = errors.New("already running")
