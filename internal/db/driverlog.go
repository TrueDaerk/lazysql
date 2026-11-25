package db

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

// DriverMessage is one line a dependency would otherwise have printed to
// the terminal on its own: the MySQL driver's error logger or the
// standard library's log package. Owner is the Logger of the connection
// the line belongs to, nil when it cannot be attributed to one.
type DriverMessage struct {
	At     time.Time
	Source string
	Text   string
	Owner  *Logger
}

// driverMessageBuffer bounds the queue between the goroutine that logs
// and the UI that drains it; past it, lines are dropped and counted.
const driverMessageBuffer = 256

var (
	driverMessages = make(chan DriverMessage, driverMessageBuffer)
	droppedMu      sync.Mutex
	dropped        int
)

// emitDriverMessage queues a line for the UI without ever blocking the
// caller, which may be a driver goroutine holding a connection.
func emitDriverMessage(source, text string, owner *Logger) {
	text = RedactSecrets(strings.TrimSpace(text))
	if text == "" {
		return
	}
	select {
	case driverMessages <- DriverMessage{At: time.Now(), Source: source, Text: text, Owner: owner}:
	default:
		droppedMu.Lock()
		dropped++
		droppedMu.Unlock()
	}
}

// NextDriverMessage blocks until a captured line is available. The first
// one read after the queue overflowed says how many were lost.
func NextDriverMessage() DriverMessage {
	m := <-driverMessages
	droppedMu.Lock()
	n := dropped
	dropped = 0
	droppedMu.Unlock()
	if n > 0 {
		m.Text = fmt.Sprintf("%s (%d more lines dropped)", m.Text, n)
	}
	return m
}

// mysqlLogger is the go-sql-driver/mysql Logger lazysql installs, both
// per connection and as the driver's package default.
type mysqlLogger struct {
	owner *Logger
}

func (l mysqlLogger) Print(v ...any) {
	emitDriverMessage("mysql", fmt.Sprint(v...), l.owner)
}

// lineWriter turns everything written to it into one message per line.
type lineWriter struct {
	source string
	mu     sync.Mutex
	buf    bytes.Buffer
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	for {
		i := bytes.IndexByte(w.buf.Bytes(), '\n')
		if i < 0 {
			break
		}
		line := string(w.buf.Next(i + 1))
		emitDriverMessage(w.source, line, nil)
	}
	return len(p), nil
}

// flush emits a trailing line that never got its newline.
func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() > 0 {
		emitDriverMessage(w.source, w.buf.String(), nil)
		w.buf.Reset()
	}
}

// CaptureProcessLogs routes the standard log package and the MySQL
// driver's package-level logger into the driver message queue until the
// returned func is called, which restores both. It is meant to bracket
// the TUI's lifetime: while the renderer owns the terminal, nothing else
// may write to it.
func CaptureProcessLogs() (restore func()) {
	prevOut, prevFlags, prevPrefix := log.Writer(), log.Flags(), log.Prefix()
	w := &lineWriter{source: "log"}
	log.SetOutput(w)
	log.SetFlags(0)
	log.SetPrefix("")
	_ = mysql.SetLogger(mysqlLogger{})
	return func() {
		w.flush()
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
		_ = mysql.SetLogger(log.New(os.Stderr, "[mysql] ", log.Ldate|log.Ltime))
	}
}

var (
	// scheme://user:password@host
	urlUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://[^\s:/@]*):[^\s@/]*@`)
	// MySQL DSN user:password@tcp(host)
	mysqlUserinfo = regexp.MustCompile(`(^|[\s'"=(])([^\s:/@'"=(]+):[^\s@]*@([a-z0-9]*\()`)
	// password=secret in a key/value DSN or a query string
	passwordParam = regexp.MustCompile(`(?i)(pass(?:word)?\s*=\s*)('[^']*'|[^\s&;]+)`)
)

// RedactSecrets masks passwords that may appear inside a DSN or URL
// quoted in a dependency's message.
func RedactSecrets(s string) string {
	s = urlUserinfo.ReplaceAllString(s, "$1:***@")
	s = mysqlUserinfo.ReplaceAllString(s, "$1$2:***@$3")
	s = passwordParam.ReplaceAllString(s, "$1***")
	return s
}
