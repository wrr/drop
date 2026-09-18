# Drop 
**Linux sandboxing that doesn't get in your way**

**[droprun.sh](https://droprun.sh)** | [Documentation](https://droprun.sh/docs/)

Drop allows you to easily create sandboxed environments that isolate
programs and coding agents while preserving as many aspects of
your work environment as possible. Drop uses your existing
distribution, so all the programs you've installed are available in
the sandbox. Your username is preserved, and selected configuration
files remain readable in the sandbox.

## Quick start

The workflow is inspired by Python's virtualenv: create an easily
disposable environment, enter it, work normally - but with enforced
sandboxing.

To create a new Drop environment you simply run:

```console
alice@zax:~/project$ drop init
Drop environment created with config at /home/alice/.config/drop/home-alice-project.toml
```

To start a sandboxed shell in the created environment:

```console
alice@zax:~/project$ drop run
```

The created environment gets its own writable home dir with selected
files and dirs from your original home available in read-only mode. By
default the environment has access to your current working directory
in read-write mode, with the exception of the `.git` subdirectory,
which is read-only:

```console
(drop)alice@zax:~/project$ file ~/.bashrc
/home/alice/.bashrc: ASCII text
(drop)alice@zax:~/project$ file ~/.ssh
/home/alice/.ssh: cannot open `/home/alice/.ssh' (No such file or directory)
(drop)alice@zax:~/project$ echo "evil command" >> ~/.bashrc
bash: /home/alice/.bashrc: Read-only file system
```

See also [the Drop tour](https://droprun.sh/docs/tour/) for a full
walkthrough of installing and running Claude Code inside a Drop
environment.

## Sandbox overview

* Drop doesn't require root and can't execute any operation that the current
  user is not allowed to execute.
* Uses Linux namespaces (user, mount, PID, IPC, cgroup and network)
  to isolate sandboxed programs from the host.
* Optionally runs sandboxed programs on the [gVisor](https://gvisor.dev) user-space
  kernel, so they don't issue syscalls directly to the host kernel.
* By default disallows network access to services running on
  localhost. Uses [pasta](https://passt.top) for networking.
* Drops all the capabilities before starting a sandboxed program, so
  sandboxed processes can't do privileged operations, such as bind
  mounts, within the user namespace.

Drop uses a mount namespace to arrange its own root filesystem, hiding
the original host filesystem. See the [Filesystem
layout](https://droprun.sh/docs/sandbox-overview/#filesystem-layout) doc.

## Installation

### Prerequisites

Drop requires the passt/pasta package for isolated networking, which
is [available on most Linux
distributions](https://passt.top/passt/about/#availability):

```console
$ sudo apt-get install passt  # Debian/Ubuntu
$ sudo dnf install passt      # Fedora
$ sudo pacman -S passt        # Arch
```

### Install Drop

Download a prebuilt binary from
[GitHub releases](https://github.com/wrr/drop/releases/latest/) and
place it in your PATH:

```
# Set ARCH to either amd64 or arm64
ARCH=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')

curl -o drop -L https://github.com/wrr/drop/releases/latest/download/drop-linux-$ARCH
install drop ~/.local/bin/
```

Ubuntu 24+ requires [AppArmor configuration](https://droprun.sh/docs/installation/#ubuntu-24---apparmor-config).
Fedora requires [SELinux configuration](https://droprun.sh/docs/installation/#fedora---selinux-config).

## Commands

The commands to work with Drop are:

 * `drop` - show help
 * `drop init [ENV_ID]` - create a new Drop environment. If ENV_ID is
   not given, it is derived from the current working directory.
 * `drop run [-e ENV_ID] [command...]` - run a command in a Drop
   environment. For example, `drop run -e vault13 ps aux`. If
   `[command...]` is not given, a shell is started. If `-e ENV_ID` is
   not given, it is derived from the current working directory.
 * `drop ls` - list created environments
 * `drop rm <ENV_ID>` - remove an environment
 * `drop update --check` - check if a new version of Drop is available

See also the [Running](https://droprun.sh/docs/running/) doc.

## Configuration

By default Drop config files are stored in `~/.config/drop`. Most of
the settings should be placed in `base.toml`, which is shared by
all Drop environments. In addition, each environment has its own
`<envid>.toml`.

Review the generated `~/.config/drop/base.toml` and make sure no files
containing secrets are exposed.

The comments in the generated files explain each of the settings. You
can also refer to the [Configuration](https://droprun.sh/docs/configuration/) doc.

## Drop compared to other tools

Drop's focus is productive UX for local workflows.

Unlike `runc`, `bubblewrap` or `nsjail`, which are low-level building
blocks for sandboxed environments, Drop is high-level, intended to be
used directly in day-to-day work without extensive configuration.

Unlike Docker/Podman, Drop is not intended for reproducible server
deployments with minimal dependencies. The assumption is that a local
work environment is different for every person. It takes effort to
configure a new machine with all the programs needed for productive
work. If a sandboxed environment is stripped of all these programs
and presents the user with a minimal environment where many familiar
tools and configuration files are missing, the sandbox gets in the way
of getting things done.

Unlike Flatpak and Snap, Drop is not intended for shipping sandboxed
desktop programs. With Flatpak/Snap, the program's author configures a
sandbox. With Drop, the user enables the sandbox and the executed
programs do not need to have any awareness or support for Drop.

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

## Building Drop from source

Requires the [Go compiler](https://go.dev/doc/install) 1.25+.

Clone this repo, download dependencies, build Drop:

```
git clone https://github.com/wrr/drop.git
cd drop
make get-deps
make build
```

To install to `/usr/local/bin` (requires sudo):

```
sudo make install
```

To install to another directory, pass the `BINDIR` var:

```
make install BINDIR=$HOME/.local/bin
```

