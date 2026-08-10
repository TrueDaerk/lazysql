package dump

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// A password must not reach argv: `ps` shows another user's argv on every
// Unix, so `--password=hunter2` leaks the secret for as long as the tool
// runs. Both engine families offer a file instead, and both refuse to read
// one that is group- or world-readable, so the file is created 0600 and
// removed the moment the child exits.
//
// The environment is only marginally better than argv (/proc/<pid>/environ
// is readable by the owner, and a crash reporter may capture it), so
// PGPASSWORD is not used at all: PGPASSFILE names the file rather than
// carrying the secret.

// writeTempCred creates a 0600 file with content in the user's temp
// directory and returns its path.
func writeTempCred(pattern, content string) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("dump: create credential file: %w", err)
	}
	// The 0600 is set before anything is written: CreateTemp already
	// creates the file 0600, and Chmod makes that a property of this code
	// rather than of the standard library's current behaviour.
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", fmt.Errorf("dump: chmod credential file: %w", err)
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", fmt.Errorf("dump: write credential file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("dump: close credential file: %w", err)
	}
	return f.Name(), nil
}

// writePgpass writes a one-line `.pgpass` for PGPASSFILE. libpq refuses a
// file with permissions wider than 0600 and silently ignores a malformed
// line, so both the mode and the escaping matter.
func writePgpass(host string, port int, database, user, password string) (string, error) {
	line := strings.Join([]string{
		pgpassField(host),
		pgpassField(strconv.Itoa(port)),
		pgpassField(database),
		pgpassField(user),
		pgpassField(password),
	}, ":")
	return writeTempCred("lazysql-pgpass-*", line+"\n")
}

// pgpassField escapes the two characters libpq reads specially in a
// .pgpass field: the `:` separator and the `\` that escapes it.
func pgpassField(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, ":", `\:`)
}

// writeMyCnf writes a MySQL option file for `--defaults-extra-file`. The
// `[client]` group is read by both mysqldump and mysql, so one file covers
// dump and restore alike.
func writeMyCnf(password string) (string, error) {
	return writeTempCred("lazysql-mycnf-*", "[client]\npassword="+myCnfValue(password)+"\n")
}

// myCnfValue quotes an option-file value. MySQL reads a double-quoted
// value with backslash escapes (`\\`, `\"`, and the `\b \t \n \r \s`
// shorthands), so quoting and escaping the backslash and the quote is
// enough for any password — including one with a `#`, which would
// otherwise start a comment.
func myCnfValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
