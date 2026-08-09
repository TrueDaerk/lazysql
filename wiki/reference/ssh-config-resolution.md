---
type: Reference
title: Which ~/.ssh/config keywords lazysql resolves, and which it ignores
description: The four keywords sshtunnel.Resolve reads from an OpenSSH client config, the precedence rule between a profile and an alias, and why the rest of ~/.ssh/config is deliberately not honoured.
tags: [ssh, tunnel, config, reference]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
sources:
  - resource: https://pkg.go.dev/github.com/kevinburke/ssh_config
  - resource: https://man.openbsd.org/ssh_config
---

# ~/.ssh/config resolution

`sshtunnel.Resolve` parses `~/.ssh/config` with
`github.com/kevinburke/ssh_config` and reads exactly four keywords:

| Keyword | Used for |
| --- | --- |
| `HostName` | the address actually dialled |
| `User` | the SSH login |
| `Port` | the SSH port |
| `IdentityFile` | the private key, when auth is `key` and no path was typed |

Everything else — `ProxyJump`, `ProxyCommand`, `ControlMaster`,
`ForwardAgent`, `Match` blocks — is **not** honoured. lazysql dials the
bastion itself with `golang.org/x/crypto/ssh`; it does not shell out to
`ssh(1)`, so honouring a keyword would mean reimplementing it. Silently
ignoring `ProxyJump` while pretending to read the file would be worse
than not reading it, hence the short explicit list.

## Precedence

The profile wins; an alias only fills in blanks.

```
profile value  >  ~/.ssh/config value  >  built-in default
```

with one asymmetry worth remembering: **`HostName` always overrides**
`Config.Host`. That is not an exception to the rule but the point of an
alias — `bastion` is a name for a config block, not a resolvable
hostname, so the block has to be allowed to say what it really means.
The port, user and key file typed into the form are never overridden.

Built-in defaults, when neither source says anything: port 22, and the
current OS user as the login.

## Gotchas

- **`(*ssh_config.Config).Get` applies no defaults.** The package-level
  `ssh_config.Get` does — it would answer `22` for `Port` and
  `~/.ssh/identity` for `IdentityFile` on a host nobody configured,
  which would defeat the precedence rule above. lazysql decodes the file
  itself and uses the method, so an unset keyword reads back as `""`.
- **A `Host *` block still matches**, so a wildcard `User` is picked up
  for a host that has no block of its own. That is the same thing
  `ssh(1)` does.
- **`~` is expanded by lazysql, not by the parser.** `IdentityFile
  ~/.ssh/id_x` comes back with the tilde intact; `expandHome` resolves a
  leading `~/` against the home directory. `~user/…` is left alone —
  resolving it needs the user database, and a wrong guess would read the
  wrong key.
- **A missing config file is not an error.** It just means no aliases
  exist. A file that exists but does not parse *is* an error, because
  silently ignoring it would connect somewhere the user did not intend.
- **A non-numeric or out-of-range `Port` is an error**, not a fallback
  to 22, for the same reason.

`Config.SSHConfigFile` and `Config.KnownHostsFile` exist so tests can
point at a temporary directory instead of the developer's real `~/.ssh`.
