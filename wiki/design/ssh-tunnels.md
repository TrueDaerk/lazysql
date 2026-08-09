---
type: Design Decision
title: SSH tunnels as a transport under the driver, not a feature inside it
description: Why internal/sshtunnel hands out a net.Conn dialer that internal/db injects into the concrete SQL driver, how host keys are enforced with a prompt-or-refuse rule, where the SSH secret lives, and what ties the tunnel's lifetime to the connection.
tags: [ssh, tunnel, security, db, connections, keyring]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
sources:
  - resource: https://pkg.go.dev/golang.org/x/crypto/ssh
  - resource: https://pkg.go.dev/github.com/kevinburke/ssh_config
---

# SSH tunnels

## Decision

A tunnel is a **transport**, not a database feature. `internal/sshtunnel`
knows how to reach a bastion host and hand out `net.Conn`s forwarded
through it; `internal/db` knows how to give such a dialer to a concrete
SQL driver. Neither knows about the other's domain, and UI code touches
neither: it fills in a `config.SSH` section and gets a working `Driver`
back.

The seam is one type:

```go
// internal/db
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

func OpenWith(engine Engine, dial DialFunc) (Driver, error)
```

`sshtunnel.Tunnel.DialContext` has exactly that signature, which is also
what both drivers want, so nothing has to adapt anything.

## Why the dialer is injected at open time, not at DSN time

The DSN keeps naming the *database's* address as seen from the jump host
(`db.internal:5432`), and the tunnel is attached separately. The
alternative — rewriting the DSN to point at a local forwarded port —
would mean binding a listener on localhost for every connection, which
is a second thing to leak and lets anything else on the machine reach
the database. There is no local listener anywhere in this design.

Injecting the dialer is where the two engines differ, so each `Dialect`
implements it (`internal/db/dial.go`):

| Engine | How a dialer gets in | Undone by |
| --- | --- | --- |
| MySQL / MariaDB | `mysql.RegisterDialContext(name, …)` registers a fake "network" in a process-global map; the DSN's `tcp(addr)` is rewritten to `<name>(addr)` | `mysql.DeregisterDialContext(name)` |
| PostgreSQL | `pgx.ParseConfig(dsn)` → set `DialFunc` → `stdlib.RegisterConnConfig(cfg)` returns an opaque DSN name for `sql.Open("pgx", name)` | `stdlib.UnregisterConnConfig(name)` |
| SQLite / DuckDB | refused — a file has no socket | — |

Both registrations are global maps keyed by a name, so each connection
takes a unique one (`lazysql-tunnel-<n>`) and drops it in `conn.Close`.
Without that, reconnecting would grow the map forever and two live
connections would fight over one entry.

Two smaller rules fall out of this:

- A MySQL profile whose host is an absolute path is a unix socket; it is
  rejected rather than silently tunnelled to a path on the wrong machine.
- The pgx config gets a `LookupFunc` that returns the hostname
  unresolved. Name resolution has to happen on the far side of the
  tunnel — resolving `db.internal` locally would either fail or, worse,
  succeed and point somewhere else.

## Host keys: prompt or refuse, never silently accept

`known_hosts` is checked with `golang.org/x/crypto/ssh/knownhosts`, and
the outcome is one of three, never "trust anyway":

- **Known and matching** — connect.
- **Unknown** — `*UnknownHostKeyError` carrying the address, key type and
  SHA256 fingerprint. `Open` fails. The UI shows a danger-styled confirm
  modal with the fingerprint; only `enter` calls
  `sshtunnel.AcceptHostKey`, which appends the entry to `known_hosts`,
  and then redials.
- **Changed** — `*HostKeyMismatchError` naming the file and line of the
  recorded key. This is what a man-in-the-middle looks like, so the modal
  built for it has **no** confirm action at all: `onConfirm` is nil and
  the only way out is `esc`.

`ssh.NewClientConn` wraps whatever the host key callback returned, so the
callback also records its own error and `Open` prefers that recorded
error over the wrapped one. Without that, the UI would see a generic
"handshake failed" and could not offer the prompt.

A missing `known_hosts` file is not an error: every host is then simply
unknown, so a first run prompts instead of failing.

## Secrets

The SSH password and the private key passphrase are the same slot in the
config model — `sshtunnel.Config.Secret` — and both live in the OS
keyring under `secrets.SSHKey(name)`, i.e. the connection name plus
`#ssh`. `config.toml` holds the host, port, user, auth method and key
*path*, never a secret. `secrets.Rename` moves both slots together and
`secrets.Forget` removes both, so a renamed or deleted profile leaves no
orphan.

Two failures are recoverable with a prompt rather than an error, and the
`dialRequest` that travels back in the result message is what makes the
retry possible without re-typing anything else:

- an encrypted key with no passphrase (`ErrPassphraseRequired`);
- password auth with nothing in the keyring.

`hasSSHSecret` on the request stops that from becoming a prompt loop: a
second failure after a secret was supplied is reported, not re-prompted.

## Lifetime

The tunnel's lifetime is the connection's. `Model.tunnel` sits beside
`Model.driver` and the two are always torn down together, driver first
so its handles are gone before the transport under them is:

- replacing a connection → `closeSessionCmd(prevDriver, prevTunnel)`;
- deleting the active profile → the same command;
- **quit** → `m.closeSession()`, called *synchronously* in the key
  handler. `tea.Quit` can stop the program before a batched `tea.Cmd`
  ever runs, so a command here would be a real leak.

`Tunnel.Close` closes every forwarded conn it handed out, then the SSH
client, then waits on `client.Wait()` so teardown is observable rather
than eventually-consistent, and finally the ssh-agent socket if one was
opened. Handed-out conns track themselves and drop out of the set when
closed individually, so a long-lived tunnel does not accumulate dead
entries. A dial that races `Close` gets `ErrTunnelClosed` and the
just-opened channel is closed rather than handed out.

## Form

The SSH section is appended to the same connection `formModal`, not a
separate wizard — see
[design/connection-form-modal](connection-form-modal.md). Visibility
predicates do the work: the whole section is hidden unless the engine is
network-based, everything below the toggle is hidden while it is off, and
the auth choice decides whether the key-file and secret fields appear
(agent auth asks for neither).

`~/.ssh/config` is consulted for `HostName`, `User`, `Port` and
`IdentityFile` — see
[reference/ssh-config-resolution](../reference/ssh-config-resolution.md).

## Testing

`internal/sshtunnel/sshtest` runs a real SSH server in-process: it does
the real handshake and honours `direct-tcpip` channel opens. Everything
is tested against it rather than a stub, including the acceptance
criterion that teardown leaks nothing — `TestCloseLeavesNoLeaks` opens
five tunnels with three forwarded conns each and compares a settled
goroutine count before and after.

The end-to-end tests in `internal/db` put a minimal MySQL and PostgreSQL
server behind that tunnel: each answers the client's first packet with a
protocol-level error, so the assertion is that the *server's own message*
comes back through the tunnel. That exercises DSN, dialer registration,
SSH channel and bytes in both directions with the engines' real client
libraries.
