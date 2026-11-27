// Package svn runs the svn command-line client and parses its --xml output.
//
// Everything goes through the real svn binary (TortoiseSVN's or SlikSVN's
// svn.exe on Windows), so the app behaves exactly like the command line and
// shares its working copies and credential cache. Every call is
// --non-interactive: a password comes in on stdin (--password-from-stdin),
// never on the command line, and a prompt svn would have shown comes back as
// an *Error the app turns into a dialog.
package svn

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// Creds are a login for one server. An empty Username lets svn use its own
// cache (or anonymous access).
type Creds struct {
	Username string
	Password string
}

// Opts are the per-call settings that depend on the server being reached.
type Opts struct {
	Creds Creds
	// TrustCert accepts a certificate svn could not verify (self-signed, an
	// in-house CA, a name mismatch). Set only after the user agreed.
	TrustCert bool
}

// Client runs one svn binary.
type Client struct {
	Path string // the svn executable
}

// Find locates svn: on PATH, then where the Windows installers put it.
func Find() (*Client, error) {
	if p, err := exec.LookPath("svn"); err == nil {
		return &Client{Path: p}, nil
	}
	for _, p := range extraPaths() {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return &Client{Path: p}, nil
		}
	}
	return nil, ErrNotInstalled
}

// ErrNotInstalled means no svn binary was found.
var ErrNotInstalled = errors.New("the svn command-line client is not installed")

// Version is the client's version, e.g. "1.14.5".
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := c.run(ctx, Opts{}, nil, "--version", "--quiet")
	return strings.TrimSpace(string(out)), err
}

// Kind classifies a failure so the app knows what to ask the user.
type Kind int

const (
	KindOther        Kind = iota
	KindAuth              // a username and password are needed, or were wrong
	KindCert              // the server's certificate is not trusted
	KindConflict          // the working copy is out of date or has conflicts
	KindLocked            // the working copy is locked: svn cleanup
	KindCanceled          // the user canceled
	KindLockConflict      // a file is locked by someone else, or this working copy's lock was taken
	KindNeedUpdate        // the working copy must be updated first (mixed revisions, for a merge)
	KindNeedClean         // the working copy must have no local changes (for some merges)
)

// Error is a failed svn run.
type Error struct {
	Kind  Kind
	Codes []string // svn's error codes, e.g. E170001
	Msg   string   // svn's stderr, trimmed
}

func (e *Error) Error() string { return e.Msg }

var codeRe = regexp.MustCompile(`\b([EW]\d{6}):`)

// Codes that need a login. E215004 is "no more credentials", which is what
// --non-interactive gives instead of a prompt; E170001 is "authorization
// failed"; E170013 wraps them when the connection itself was refused.
var (
	authCodes     = []string{"E170001", "E215004"}
	certCodes     = []string{"E230001"}
	conflictCodes = []string{"E155011", "E160028", "E170004", "E155015", "E160024"}
	lockedCodes   = []string{"E155004", "E155037"}
	// W160035: already locked by someone else (with E200009, "some locks
	// weren't obtained", which alone is svn's generic "illegal target" and so
	// isn't listed); E160037: committing a file whose lock was taken over (same
	// user, another working copy); E160039: committing or unlocking a file
	// another user has locked; E195022: unlocking a file locked by someone else.
	needUpdateCodes   = []string{"E195020"} // can't merge into a mixed-revision working copy
	lockConflictCodes = []string{"W160035", "E160037", "E160039", "E195022"}
)

func newError(stderr string, err error) *Error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = err.Error()
	}
	e := &Error{Msg: msg}
	for _, m := range codeRe.FindAllStringSubmatch(stderr, -1) {
		e.Codes = append(e.Codes, m[1])
	}
	switch {
	case e.has(certCodes):
		e.Kind = KindCert
	case e.has(authCodes):
		e.Kind = KindAuth
	case e.has([]string{"E195016"}): // can't merge into a working copy with local changes
		e.Kind = KindNeedClean
	case e.has(needUpdateCodes):
		e.Kind = KindNeedUpdate
	case e.has(lockConflictCodes):
		e.Kind = KindLockConflict
	case e.has(lockedCodes):
		e.Kind = KindLocked
	case e.has(conflictCodes):
		e.Kind = KindConflict
	}
	return e
}

func (e *Error) has(codes []string) bool {
	for _, c := range e.Codes {
		for _, want := range codes {
			if c == want {
				return true
			}
		}
	}
	return false
}

// KindOf is err's Kind, or KindOther.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	if errors.Is(err, context.Canceled) {
		return KindCanceled
	}
	return KindOther
}

// args puts the global options in front of a subcommand's arguments.
func (o Opts) args(sub []string) []string {
	a := []string{"--non-interactive"}
	if o.Creds.Username != "" {
		a = append(a, "--username", o.Creds.Username)
		if o.Creds.Password != "" {
			a = append(a, "--password-from-stdin")
		}
	}
	if o.TrustCert {
		a = append(a, "--trust-server-cert-failures=unknown-ca,cn-mismatch,expired,not-yet-valid,other")
	}
	return append(a, sub...)
}

func (c *Client) command(ctx context.Context, o Opts, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.Path, o.args(args)...)
	cmd.Env = environ()
	cmd.Dir = emptyDir()
	if o.Creds.Username != "" && o.Creds.Password != "" {
		cmd.Stdin = strings.NewReader(o.Creds.Password + "\n")
	}
	prepare(cmd)
	return cmd
}

// run runs svn and returns its stdout. stdin, when set, replaces the
// password as the process's input (only used without a password).
func (c *Client) run(ctx context.Context, o Opts, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := c.command(ctx, o, args)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, newError(errb.String(), err)
	}
	return out.Bytes(), nil
}

// stream runs svn and hands each line of its stdout to line as it arrives,
// for the long operations (checkout, update) whose progress the window shows.
func (c *Client) stream(ctx context.Context, o Opts, line func(string), args ...string) error {
	cmd := c.command(ctx, o, args)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			if line != nil {
				line(sc.Text())
			}
		}
	}()
	wg.Wait()
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return newError(errb.String(), err)
	}
	return nil
}

// emptyDir is a folder with nothing in it, that svn runs in.
//
// svn.exe on Windows (TortoiseSVN's, at least) expands wildcards in its
// arguments itself, against the folder it runs in, quoted or not: in a folder
// holding alpha.txt, `svn info "*a*"` is `svn info alpha.txt`. A pattern that
// matches nothing is passed on as typed. So svn runs where nothing can match
// a pattern such as a log search's *motor*. ("*" alone, and other patterns of
// only * ? and dots, still match Windows' "." and ".." entries: searchGlob
// never produces one, and svn:needs-lock's "*" is set from a file.) Every
// path svn is given is absolute, so the folder is never used for anything else.
var emptyDir = sync.OnceValue(func() string {
	if d, err := os.MkdirTemp("", "trunkline-svn-"); err == nil {
		return d
	}
	return "" // svn then runs in the app's folder
})

// environ is the process environment with svn's messages in English (the
// error codes are what the app matches, but the text is shown to users and
// pasted into bug reports) while keeping the character set, which svn needs
// to handle non-ASCII file names.
func environ() []string {
	var env []string
	ctype := ""
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "LC_ALL":
			ctype = v
			continue
		case "LC_MESSAGES", "LANGUAGE":
			continue
		case "LC_CTYPE":
			if ctype == "" {
				ctype = v
			}
			continue
		case "LANG":
			if ctype == "" {
				ctype = v
			}
		}
		env = append(env, kv)
	}
	if ctype == "" || ctype == "C" || ctype == "POSIX" {
		ctype = "C.UTF-8"
	}
	return append(env, "LC_CTYPE="+ctype, "LC_MESSAGES=C", "LANGUAGE=C")
}

// errorf wraps a parse failure with the command it came from.
func errorf(cmd string, err error) error {
	return fmt.Errorf("svn %s: reading its output: %w", cmd, err)
}
