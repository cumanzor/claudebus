package client

import (
	"errors"
	"os"
)

// OpenGrantTerminal: the operator confirmation is unix-only for now.
func OpenGrantTerminal() (*os.File, string, error) {
	return nil, "", errors.New("cbus grant is not supported on windows yet")
}

func IsTerminal(*os.File) bool { return false }
