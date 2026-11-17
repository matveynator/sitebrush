package goprofile

import (
	"fmt"
	"os"
	"os/user"
)

// Account lookup runs in the capture worker, never in the command owner's select loop.
func processUser() string {
	account, err := user.Current()
	if err == nil {
		return fmt.Sprintf("%s (UID/SID %s)", account.Username, account.Uid)
	}
	if identifier := os.Geteuid(); identifier >= 0 {
		return fmt.Sprintf("UID %d", identifier)
	}
	return "account unavailable"
}
