---
title: Configuration
weight: 3
---

## Configuration files

When `drop init` is run for the first time, it creates a
[base.toml](https://github.com/wrr/drop/blob/main/docs/base.example.toml) config file, which by default is shared
by all Drop environments.

The created `base.toml` has sensible defaults that expose several
common dotfiles that are present in your home dir to Drop
environments. The config also exposes common environment
variables. Review the generated defaults, ensure that no files with
secrets are exposed, expose config files of other programs that you
use.

`drop init` also creates a tiny, [environment specific config
file](https://github.com/wrr/drop/blob/main/docs/env.example.toml).
This file extends `base.toml` and allows to add environment specific
configuration.

## Config settings

The following sections document all the settings supported by Drop's TOML config. Many of these settings can be
overwritten or extended by command line arguments.

### `runtime`

Sandboxing runtime:

- `runtime = "native"` - run directly on the host kernel.
- `runtime = "gvisor"` - for added isolation run on the gVisor user-space kernel.

Command line overwrite `--runtime`, takes priority over the TOML setting:
```
drop run --runtime gvisor
```

### `mounts`

A list of directories and files exposed to Drop. Directories are
exposed with all content, including sub-directories.

The list entries can have a compact string syntax, like:

- `"~/bin"` - expose `~/bin` directory as read-only.
- `"~/bin:~/bin-host"` - expose `~/bin` directory as read-only `~/bin-host`.
- `"~/plan::rw"` - expose `~/plan` file as writable.
- `"~/plan:~/plan-host:rw"` - expose `~/plan` file as writable `~/plan-host`.

Alternatively, a verbose dictionary syntax can be used; it allows
handling paths with `:` characters. Equivalents of the examples
above with the verbose syntax are:

```
{source="~/bin"}
{source="~/bin", target="~/host-bin"}
{source="~/plan", rw=true}
{source="~/plan", target="~/host-plan", rw=true}
```

All paths must be normalized and either start with / or ~/.

{{< callout type="warning" >}}
Be sure not to expose files with secrets or other sensitive
data. Configs without sensitive data are safe to expose as read-only.

Use files exposed as read-write carefully and sparingly - untrusted
programs should not be able to write files that are executed outside
of the sandbox. Shell config scripts are executed by the host, so
exposing them as read-write would let sandboxed programs inject
commands that run outside the sandbox. Read-only is safe.
Similarly, entries from ~/.bash_history can be executed, so it is
best not to expose history, but allow shells in Drop environments
to create isolated history files, one per each environment.
{{< /callout >}}

Example:
```
mounts = [
  "~/.ackrc",
  "~/.profile",
  "~/go",
  "~/.nvm",
  "~/.screenrc",
  "~/.bashrc",
  "~/.bash_logout",
  "~/.bash_profile",
  "~/.local/bin:~/.local-host/bin",
  "~/.local/include:~/.local-host/include",
  "~/.local/lib:~/.local-host/lib",
]
```

Command line modifier `-m, --mount`, adds mounts for this run only,
without changing the TOML file:

```
drop run --mount ~/tmp --mount "~/bin"
```

### `blocked_paths`

Paths to dirs or files to block access to.

Host filesystem access restrictions still apply in Drop, so you
don't need to block files your current user already can't access
(for example /etc/shadow). Drop also mounts almost
all dirs read-only, so you don't need to include files just to block
writing to them.

Example:

```
blocked_paths = [ "~/project/.secrets" ]
```

### `[environ]`

Groups all the settings related to environment variables available
within the sandbox.

#### `exposed_vars`

A list of environment variables to expose from the process starting
Drop to the sandbox. You can use glob patterns to expose all variables
with common prefix/suffix.

{{< callout type="warning" >}}
Do not expose variables containing secrets. Expose all
other variables needed for convenient work.
{{< /callout >}}

Example:
```
exposed_vars = [
  "XDG_DATA_HOME",
  "XDG_CONFIG_HOME",
  "XDG_STATE_HOME",
  "XDG_DATA_DIRS",
  "XDG_CONFIG_DIRS",
  "XDG_CACHE_HOME",
  "XDG_RUNTIME_DIR",
  "SHELL",
  "LC_*",
  "XTERM_SHELL",
  "EDITOR",
  "PWD",
  "LOGNAME",
  "HOME",
  "LANG",
  "LESSCLOSE",
  "LESSOPEN",
  "LS_COLORS",
  "XTERM_LOCALE",
  "TERM",
  "USER",
  "SHLVL",
  "PATH",
]
```

#### `set_vars`

A list of new environment variables passed to the sandboxed process.
Values can include existing vars as `${VAR_NAME}`

Example:

```
set_vars = [
  "debian_chroot=drop", # Add '(drop)' prefix to shell prompts on Debian-based systems
  "PATH=${PATH}:${HOME}/.local-host/bin", # Add .local/bin from host (mounted as .local-host/bin) to PATH.
]
```

### `[net]`

Groups all the settings related to networking.

#### `mode`

Network mode:

- `mode = "off"` - programs in the sandbox cannot access remote and local network services. Ports opened by the programs are not accessible from the host.
- `mode = "isolated"` - programs in the sandbox can access remote services. Port mapping settings below determine which services running in the sandbox can be accessed from the host and which services running on the host can be accessed from the sandbox.

Command line overwrite `-n, --net`, takes priority over the TOML setting:
```
drop run --net off
```

#### `tcp_published_ports`

A list of TCP ports published from the sandbox.

Entries have the form: `[host_ip/][HOST_PORT:]DROP_PORT`
If host_ip is not specified, it defaults to 127.0.0.1.
If HOST_PORT is not specified, it defaults to DROP_PORT.
Empty list means no ports are exposed.
Example valid list items:

- `"8080"` - publish port 8080 from the sandbox as 127.0.0.1:8080 on the host
- `"8080:8000"` - publish port 8000 from the sandbox as 127.0.0.1:8080 on the host
- `"0.0.0.0/8080:8000"` - publish port 8000 from the sandbox as 8080 on the host, bind it to all the host's IP addresses. This makes the port externally accessible if the host has no firewall rules to block outside traffic to this port
- `"127.0.0.1/auto"` - all ports open in the sandbox are automatically published and bound to the host's localhost address. This is preferable to the plain "auto" option below when external exposure is not needed, but requires pasta version 2026_05_07.1afd4ed or newer.
- `"auto"` - all ports open in the sandbox are automatically published and bound to ALL the host's IP addresses. This is convenient, but must be used with care, make sure the host has firewall configured to filter outside traffic.

Example:
```
tcp_published_ports = [
    "12000",
    "8080:8000"
]
```

Command line modifier `-t, --tcp-publish`, adds published TCP ports
for this run only:
```
drop run --tcp-publish auto
```

#### `udp_published_ports`

A list of UDP ports published from the sandbox (see [tcp_published_ports](#tcp_published_ports)).

```
udp_published_ports = [
    "9000",
    "17564"
]
```

Command line modifier `-u, --udp-publish`, adds published UDP ports
for this run only:
```
drop run --udp-publish auto
```

#### `tcp_host_ports`

A list of localhost TCP ports open on the host that the sandbox can access.
Entries have the form
`HOST_PORT[:DROP_PORT]`
If DROP_PORT is not specified, it defaults to HOST_PORT

```
tcp_host_ports = [
    "22",
    "13:5013"
]
```

Command line modifier `-T, --tcp-host`, adds to the list for this run
only:

```
drop run --tcp-host 43
```

#### `udp_host_ports`

A list of localhost UDP ports open on the host that the sandbox can
access (see [tcp_host_ports](#tcp_host_ports)).

```
udp_host_ports = ["37"]
```

Command line modifier `-U, --udp-host`, adds to the list for this run only:

```
drop run --udp-host 111
```

### `extends`

To allow configuration reuse, Drop config file can extend another Drop
config file.

All the list settings set in the child config file are appended to the
equivalent list settings from the parent config. The `runtime` and
`net.mode`, if set in the child file, overwrite the settings from the
parent.

Example:
```
extends = "./base.toml"

# Use gVisor runtime, ignore runtime setting from base.toml
runtime = "gvisor"

# In addition to all the mounts configured in base.toml,
# mount the ~/project dir as read-write.
mounts = [
    "~/project::rw"
]

[net]
# Disable networking, ignore net.mode setting from base.toml
mode = "off"
```
