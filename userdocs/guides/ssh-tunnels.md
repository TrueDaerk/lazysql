# SSH tunnels

A connection to a server engine can be dialed **through a jump host**. Enable
it in the connection form's *SSH tunnel* section (`e` on panel `[1]`).

File engines — SQLite, DuckDB — never tunnel: there is no network connection
to tunnel.

## The fields

| Field | Meaning |
|---|---|
| **Enabled** | Dial the database through a jump host |
| **SSH host** | The bastion — a `~/.ssh/config` alias works too |
| **SSH port** | Default `22` |
| **SSH user** | Empty = whatever `~/.ssh/config` or the OS says |
| **SSH auth** | `agent`, `key file` or `password` |
| **Key file** | For `key file` auth; empty = the `IdentityFile` from `~/.ssh/config` |
| **SSH secret** | The SSH password, or the key's passphrase — kept in the OS keyring |

Only the fields the chosen auth method needs are shown, and `agent` needs no
secret at all.

The database **Host** and **Port** stay what they are: they are resolved *from
the jump host's side*, so `localhost:5432` means the database on the bastion.

## What `~/.ssh/config` contributes

lazysql reads exactly four keywords from it — `HostName`, `User`, `Port` and
`IdentityFile` — so a host alias you already have works as the SSH host.

Everything else (`ProxyJump`, `ProxyCommand`, `ControlMaster`, `Match`
blocks…) is **not** honoured. lazysql dials the bastion itself rather than
shelling out to `ssh(1)`, so honouring those would mean reimplementing them,
and silently ignoring a `ProxyJump` while pretending to read the file would be
worse than a short explicit list.

Precedence: what you typed into the profile wins, the config fills in blanks,
and built-in defaults (port 22, the current OS user) fill in the rest.

!!! note "`HostName` is the one thing that overrides you"
    That is the point of an alias — `bastion` is the name of a config block,
    not a resolvable hostname, so the block gets to say what it really means.
    The port, user and key file you typed are never overridden.

## Host keys

The jump host's key is checked against `~/.ssh/known_hosts`, and lazysql never
accepts one silently:

- an **unknown** host key opens a prompt showing the fingerprint; accepting it
  appends the key to `known_hosts` and dials again;
- a **changed** host key is refused outright — that is what a
  man-in-the-middle looks like, and there is no "accept anyway" button.

## How the tunnel works

It is not a local forwarded port. The tunnel is injected **underneath** the
database driver as its dial function, so:

- no port is opened on your machine that another process could reach;
- the tunnel's lifetime is the connection's — it goes away when the connection
  does, including a synchronous teardown on quit.

The one exception is [dump and restore](dump-and-restore.md): `pg_dump` and
friends are separate processes and cannot use an in-process transport, so a
**loopback-only local forward** is opened for the duration of that run and the
tool is pointed at it.

## Secrets

The SSH password or key passphrase goes to the OS keyring under the
connection's own SSH entry — never into `config.toml`. Removing the connection
removes both of its keyring entries.
