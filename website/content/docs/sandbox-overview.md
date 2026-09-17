---
title: Sandbox overview
description: "How Drop sandbox isolates programs: rootless operation, Linux namespaces, isolated networking and the sandboxed filesystem layout."
weight: 1
---

* Drop doesn't require root and can't execute any operation that the current
  user is not allowed to execute.
* Uses Linux namespaces (user, mount, PID, IPC, cgroup and network)
  to isolate sandboxed programs from the host.
* Sandboxed processes can only see and interact with other processes
  from the sandbox.
* By default disallows network access to services running on
  localhost. Uses [pasta](https://passt.top) for networking.
* Exposes only allowlisted environment variables to the sandbox.
* Drops all the capabilities before starting a sandboxed program, so
  sandboxed processes can't do privileged operations, such as bind
  mounts, within the user namespace.
* Optionally runs sandboxed programs on the [gVisor]({{< ref "gvisor.md" >}}) user-space
  kernel, so they don't issue syscalls directly to the host kernel.


## Filesystem layout

Drop uses a mount namespace to arrange its own root filesystem,
hiding the original host filesystem. System dirs are exposed from the
host read-only, each Drop environment gets its own persistent home
dir, `/var` and `/tmp`, and only a few basic devices are available.
The table below shows the default mounts:

{{< include "html/default-mounts.html" >}}

In addition to the default mounts, Drop's [TOML config]({{< ref
"configuration.md#mounts" >}}) lists other dirs and files from the
host to mount in the sandbox. The default config mounts common
dotfiles, such as `~/.bashrc`, and executable dirs, such as
`~/.local/bin`, all read-only.

By default, new Drop environments are configured to mount the
directory in which `drop init` was run as writable,
with the exception of the `.git` subdirectory, which is read-only.

The table below shows some examples of TOML-configured mounts:

{{< include "html/toml-mounts.html" >}}

## Current limitations

* Terminal only. GUI programs will not run in the sandbox with the
  default config. While it is possible to expose X socket files to the
  sandbox in a way that allows GUI programs to run, doing so grants
  overly broad privileges to sandboxed processes.
* Only a small set of basic devices is available in the sandbox,
  so it is not possible to, for example, play or record sound.
* setuid programs don't run in the sandbox.
* Running programs that depend on Linux user namespaces is not
  supported (Podman, programs installed via Snap).
