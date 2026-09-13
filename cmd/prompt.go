package cmd

import (
	"fmt"
	"os"

	"github.com/lesomnus/z"
	"golang.org/x/term"
)

// askToken reads a token the configuration deliberately does not carry.
//
// The restore direction needs write access, and a token that can write is not
// something a scheduled job should be holding — the mirror that runs every hour
// is read-only and its deployment documents that as an invariant. A restore runs
// once, with somebody watching, so it can be asked instead of stored.
//
// Read from the terminal, not from an argument or an environment variable: both
// of those leave the token in a shell history or a process listing, and a token
// that can write to every repository an organisation has is not something to
// leave lying in either.
//
// Nothing is asked when there is no terminal. A job with no token and nobody to
// ask should fail saying so, which is what the configuration check does, rather
// than block forever on a read that will never return.
func askToken(what string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", nil
	}

	fmt.Fprintf(os.Stderr, "%s: ", what)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", z.Err(err, "read %s", what)
	}

	return string(b), nil
}
