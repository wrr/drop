---
title: Configuration
description: "Drop sandbox TOML config reference: expose files, dirs, environment variables and localhost network services."
weight: 4
---

## Configuration files

Config files are stored in `~/.config/drop/` by default and can be
edited at any time.

Drop is a high-level sandboxing tool with minimal configuration. On
systems following standard Linux/Unix conventions, an empty Drop
config creates a secure sandbox. Configuration settings make the
sandbox more convenient to use by exposing additional files,
environment variables and network services.

When `drop init` is run for the first time, it creates a `base.toml`
config file, which is shared by all Drop environments.

The created `base.toml` has sensible defaults that expose several
common dotfiles that are present in your home dir. The config also
exposes common environment variables. Review the generated settings,
ensure that no files with secrets are exposed, and expose config
files of other programs that you use.

{{% details title="Shared `base.toml`" closed="true" %}}
{{< toml-file "configs/base.example.toml" >}}
{{% /details %}}

`drop init` also creates a tiny, environment-specific config file.
This file extends `base.toml` and allows you to add settings that
apply only to this environment.

{{% details title="Environment-specific config" closed="true" %}}
{{< toml-file "configs/env.example.toml" >}}
{{% /details %}}

## Config settings

The following sections document all the settings supported by Drop's
TOML config. Many of these settings can be overridden or extended by
command-line arguments.

### `runtime`

Sandboxing runtime:

- `runtime = "native"` - run directly on the host kernel.
- `runtime = "gvisor"` - for added isolation run on the gVisor user-space kernel.

The command-line override `--runtime` takes priority over the TOML setting:
```
drop run --runtime gvisor
```

### `mounts`

A list of directories and files exposed to Drop. Directories are
exposed with all content, including subdirectories.

The list entries can have a compact string syntax, like:

- `"~/bin"` - expose the `~/bin` directory as read-only.
- `"~/bin:~/bin-host"` - expose the `~/bin` directory as read-only `~/bin-host`.
- `"~/plan::rw"` - expose the `~/plan` file as writable.
- `"~/plan:~/plan-host:rw"` - expose the `~/plan` file as writable `~/plan-host`.

Alternatively, a verbose dictionary syntax can be used; it allows
handling paths with `:` characters. Equivalents of the examples
above with the verbose syntax are:

```
{source="~/bin"}
{source="~/bin", target="~/bin-host"}
{source="~/plan", rw=true}
{source="~/plan", target="~/plan-host", rw=true}
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
to create isolated history files, one per environment.
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

The command-line modifier `-m, --mount` adds mounts for this run only,
without changing the TOML file:

```
drop run --mount ~/tmp --mount "~/bin"
```

### `blocked_paths`

Paths to dirs or files to block access to.

Host filesystem access restrictions still apply in Drop, so you
don't need to block files your current user already can't access
(for example `/etc/shadow`). Drop also mounts almost
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
with a common prefix/suffix.

{{< callout type="warning" >}}
Do not expose variables containing secrets.
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
Values can include existing vars as `${VAR_NAME}`.

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

- `mode = "off"` - programs in the sandbox cannot access remote or local network services. Ports opened by the programs are not accessible from the host.
- `mode = "isolated"` (default) - programs in the sandbox can access remote services. Port mapping settings below determine which services running in the sandbox can be accessed from the host and which services running on the host can be accessed from the sandbox.

The command-line override `-n, --net` takes priority over the TOML setting:
```
drop run --net off
```

#### `tcp_published_ports`

A list of TCP ports published from the sandbox.

Entries have the form: `[host_ip/][HOST_PORT:]DROP_PORT`.
If host_ip is not specified, it defaults to 127.0.0.1.
If HOST_PORT is not specified, it defaults to DROP_PORT.
An empty list means no ports are exposed.
Example valid list items:

- `"8080"` - publish port 8080 from the sandbox as 127.0.0.1:8080 on the host
- `"8080:8000"` - publish port 8000 from the sandbox as 127.0.0.1:8080 on the host
- `"0.0.0.0/8080:8000"` - publish port 8000 from the sandbox as 8080 on the host, bind it to all the host's IP addresses. This makes the port externally accessible if the host has no firewall rules to block outside traffic to this port
- `"127.0.0.1/auto"` - all ports open in the sandbox are automatically published and bound to the host's localhost address. This is preferable to the plain "auto" option below when external exposure is not needed, but requires pasta version 2026_05_07.1afd4ed or newer.
- `"auto"` - all ports open in the sandbox are automatically published and bound to ALL the host's IP addresses. This is convenient, but must be used with care; make sure the host has a firewall configured to filter outside traffic.

Example:
```
tcp_published_ports = [
    "12000",
    "8080:8000"
]
```

The command-line modifier `-t, --tcp-publish` adds published TCP ports
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

The command-line modifier `-u, --udp-publish` adds published UDP ports
for this run only:
```
drop run --udp-publish auto
```

#### `tcp_host_ports`

A list of localhost TCP ports open on the host that the sandbox can
access.

Entries have the form `HOST_PORT[:DROP_PORT]`. If DROP_PORT is not
specified, it defaults to HOST_PORT.

```
tcp_host_ports = [
    "22",
    "13:5013"
]
```

The command-line modifier `-T, --tcp-host` adds to the list for this run
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

The command-line modifier `-U, --udp-host` adds to the list for this run only:

```
drop run --udp-host 111
```

### `extends`

To allow configuration reuse, a Drop config file can extend another Drop
config file.

All the list settings set in the child config file are appended to the
equivalent list settings from the parent config. The `runtime` and
`net.mode`, if set in the child file, override the settings from the
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

## Package managers

The sections below document configuration for third-party package
managers, such as Python's uv and pipx, Go's `go install` and Rust's
Cargo, that install tools to your home dir.

Many of these settings are already part of the default `base.toml`
generated by Drop when it is first run.

The goals are the following:

- Allow sandbox access to tools installed on the host.
- Make sure the sandbox can't install or overwrite tools that are
  accessible on the host.
- Allow the sandbox to install sandbox-only tools.

The first two goals can be easily achieved by mounting the relevant
package dirs read-only. The third goal requires setting
package-manager-specific environment variables to change the install
location within Drop. Whenever possible, the tools are configured to
put executables in the sandbox-only `~/.local/bin`.

### uv

uv by default installs packages and Python versions to
`~/.local/share/uv`, so it needs to be exposed as read-only to
Drop. It can't be mounted at some other location, like
`~/.local-host/share/uv`, because host-installed uv tools in
`~/.local-host/bin` are symlinks with absolute paths that point to
`~/.local/share/uv`.

Within the sandbox, the uv install location needs to be changed to a
sandbox-only, writable dir, for example `~/.local/share/uv-drop`.

```
mounts = [
  "~/.local/share/uv", # Packages installed by uv
]

[environ]
set_vars = [
  "UV_TOOL_DIR=${HOME}/.local/share/uv-drop/tools",
  "UV_PYTHON_INSTALL_DIR=${HOME}/.local/share/uv-drop/python",
  "UV_TOOL_BIN_DIR=${HOME}/.local/bin",
]
```

With these vars, `uv tool list` from within Drop only shows tools
installed within the sandbox. This makes sense, as tools installed
outside of the sandbox are read-only, can be executed but can't be
removed or upgraded, so they are not managed by sandboxed uv.

### pipx

New pipx versions install packages to `~/.local/share/pipx`; older
versions, or installations that have not been migrated, use
`~/.local/pipx`. Whichever of these dirs exists needs to be exposed to
Drop as read-only. As is the case with uv, the dir can't be mounted at
some other location, like `~/.local-host/share/pipx`, because pipx
uses symlinks with absolute paths that point to files in this dir.

Within the sandbox, the pipx install location needs to be changed to a
sandbox-only, writable dir, for example `~/.local/share/pipx-drop`.

```
mounts = [
  "~/.local/share/pipx", # Packages installed by pipx (new location)
  "~/.local/pipx", # Packages installed by pipx (old location)
]

[environ]
set_vars = [
  "PIPX_HOME=${HOME}/.local/share/pipx-drop",
]
```

Like uv, `pipx list` shows only packages installed from within Drop.

### Go

`go install` by default installs self-contained commands to
`~/go/bin` and downloads module source code to `~/go/pkg/`. Only
`~/go/bin` needs to be exposed as read-only, so the sandbox can
continue to use its own writable `~/go/pkg` dir for building. Within
the sandbox, the bin directory needs to be changed to a sandbox-only,
writable dir that is on PATH, for example `~/.local/bin`.

```
mounts = [
  "~/go/bin", # Commands installed by go install
]

[environ]
set_vars = [
  "GOBIN=${HOME}/.local/bin",
]
```

An alternative is to mount the host's `~/go/bin` at a different
location and add it to PATH. Then the sandbox can install to the
standard `~/go/bin` location, and the `GOBIN` variable can be
removed. Something like:

```
mounts = [
  "~/go/bin:~/go-host/bin", # Commands installed by go install
]

[environ]
set_vars = [
  "PATH=${PATH}:${HOME}/go-host/bin",
]
```

### Rust

The following config makes it possible to use the host-installed Rust
toolchain, run host-installed commands and install sandbox-only
commands. It doesn't allow updating the Rust toolchain from within the
sandbox, as it is read-only.

```
mounts = [
  "~/.cargo/bin", # Commands installed by cargo install and rustup
  "~/.cargo/config.toml", # Remove if you keep secrets in cargo config
  "~/.cargo/env", # Sourced by shell config files
  # "~/.cargo/env.fish", # For fish shell users
  # "~/.cargo/env.nu", # For Nushell users
  "~/.rustup", # Rust toolchains
]

[environ]
set_vars = [
  "CARGO_INSTALL_ROOT=${HOME}/.local",
]
```


