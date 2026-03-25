# Drop - productivity-focused sandboxing for Linux

Drop allows you to easily create sandboxed environments that isolate
executed programs and LLM agents while preserving as many aspects of
your work environment as possible. Drop uses your existing
distribution, so all the programs you've installed are available in
the sandbox. Your username is preserved, and selected configuration
files remain readable in the sandbox.

## Quick start

The workflow is inspired by Python's virtualenv: create an easily
disposable environment, enter it, work normally - but with enforced
sandboxing.

To create a new Drop environment you simply:

```console
alice@zax:~/project$ drop init
Drop environment created with config at /home/alice/.config/drop/home-alice-project.toml
```

To start a sandboxed shell in the created environment:

```console
alice@zax:~/project$ drop run bash
```

The created environment gets its own writeable home dir with selected
files and dirs from your original home available in read-only mode. By
default the environment has access to your current working directory
in read-write mode:

```console
(drop)alice@zax:~/project$ file ~/.bashrc
/home/alice/.bashrc: ASCII text
(drop)alice@zax:~/project$ file ~/.ssh
/home/alice/.ssh: cannot open `/home/alice/.ssh' (No such file or directory)
(drop)alice@zax:~/project$ echo "evil command" >> ~/.bashrc
bash: /home/alice/.bashrc: Read-only file system
```

## Sandbox overview

Drop uses a Linux mount namespace to arrange its own root filesystem, hiding the original host file system:

* `/usr`, `/bin`, `/sbin`, `/lib`, `/etc` are bind mounted from the host in read-only mode.
* Fresh `/proc`, `/run`, `/dev`, `/sys` are mounted.
* Each Drop environment gets its own writable and persistent home dir,
  `/tmp` and `/var`. The original user home dir is hidden.
* By default, new Drop environments are configured to mount the
  directory in which the environment was initialized in read-write
  mode.

A TOML configuration file specifies which other dirs and files from the
host should be mounted to the sandbox. Default config mounts common
configuration files, such as `~/.bashrc`, executables dirs, such as
`~/.local/bin`, all in read-only mode.

In addition to filesystem restriction, the sandbox has:

* own process and IPC namespaces, so it only sees and can interact
  with processes from the sandbox.
* own network namespace which, by default, allows external network
  access, but disallows access to services running on localhost.

## Installation

### Prerequisites
Drop requires passt/pasta package for isolated networking, which is
available on most Linux distributions
(https://passt.top/passt/about/#availability):

```console
$ sudo apt-get install passt  # Debian/Ubuntu
$ sudo dnf install passt      # Fedora
$ sudo pacman -S passt        # Arch
```

### Downloading release binary

Download a prebuilt binary from
[GitHub releases](https://github.com/wrr/drop/releases/latest/) and
place it in your PATH:

```
# Set ARCH to either amd64 or arm64
ARCH=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')

curl -o drop -L https://github.com/wrr/drop/releases/latest/download/drop-linux-$ARCH
install -m 755 drop ~/.local/bin/
```

### Installing with Go

Requires [Go compiler](https://go.dev/doc/install) (1.24+)

```
CGO_ENABLED=0 go install github.com/wrr/drop/cmd/drop@latest
```

The option `CGO_ENABLED=0` produces a statically linked binary and does not
require a C compiler, but is not strictly required.

For [Ubuntu 24+ AppArmor config](#ubuntu-24---apparmor-config) and
[Fedora SELinux config](#fedora---selinux-config) see distro-specific
sections.

## Running

The commands to work with Drop are:

 * `drop` - show help
 * `drop init [ENV_ID]` - create a new Drop environment. If ENV_ID is
   not given, it is derived from the current working directory.
 * `drop run [-e ENV_ID] [command...]` - run a command in a Drop
   environment. For example, `drop run -e vault13 ps aux`, if command is
   not given, a shell is started. If `-e ENV_ID` is not
   given, it is derived from the current working directory.
 * `drop ls` - list created environments
 * `drop rm <ENV_ID>` - remove an environment
 * `drop update --check` - check if a new version of Drop is available


## Configuration

By default Drop config files are stored in `~/.config/drop`.

When `drop init` is run for the first time, it creates a
[base.toml](./base.example.toml) config file, which by default is shared
by all Drop environments.

The created  `base.toml` config exposes several common dotfiles that are
present in your home dir to Drop environments. The config also exposes
common environment variables. Review the generated defaults, ensure
that no files with secrets are exposed, expose config files of other
programs that you use.

`drop init` also creates a tiny, [environment specific config
file](./env.example.toml).
This file extends `base.toml` and allows to add environment specific
configuration.

`drop init` configures the created environment to have access to the
directory in which `drop init` was run in read-write mode. If the
directory contains a `.git` subdirectory, that subdirectory is
configured read-only by default. This can be changed with `--no-cwd`
flag:

```
drop init --no-cwd
```

The generated files can be edited at any time to remove or add
additional exposed directories, files and network services.

Drop is a high level sandboxing tool with minimal configuration. On
systems following standard Linux/Unix conventions, an empty Drop
config creates a secure sandbox. Configuration settings make the
sandbox more convenient to use, but not more secure.


## Drop tour

This tour demonstrates key characteristics of Drop. We will install
and run Claude Code within Drop to work on a project stored in
`~/project`.

First, let's create an environment with id `claude`:

```console
alice@zax:~/project$ drop init claude
Wrote base Drop config to /home/alice/.config/drop/base.toml
Drop environment created with config at /home/alice/.config/drop/claude.toml
```

Start a sandboxed shell in the `claude` environment:
```console
alice@zax:~/project$ drop run -e claude
(drop)alice@zax:~/project$
```

Notice that, unlike in a Docker container, upon entering Drop your
username and the current path are preserved. You only see processes
started by this Drop instance:

```console
(drop)alice@zax:~/project$ ps aux
USER         PID %CPU %MEM    VSZ   RSS TTY      STAT START   TIME COMMAND
alice          1  0.0  0.0  13780  5376 pts/0    S    12:44   0:00 /bin/bash
alice         16  0.0  0.0  16016  4352 pts/0    R+   12:49   0:00 ps aux
```

Your home dir has only a few files, but these are your original
config files, so your shell and tools will behave the same in Drop as
outside of it:

```console
(drop)alice@zax:~/project$ ls -a ~
.  ..  .ackrc  .bash_logout  .bash_profile  .bashrc  code  .gitconfig  .profile  .screenrc
```

Files should in most cases be exposed read-only. This is because
sandboxed programs shouldn't be able to write to any files that are
executed outside of a sandbox:

```console
(drop)alice@zax:~/project$ echo "evil command" >> ~/.bashrc
bash: /home/alice/.bashrc: Read-only file system
```

Drop configuration also specifies which environment variables are
exposed to Drop. Most environment variables, with the exception of the
ones that store secrets, are safe to expose:

```console
(drop)alice@zax:~/project$ env
SHELL=/bin/bash
EDITOR=emacs
LS_COLORS=...
[...]
```

Now let's install Claude Code using its .sh installer:

```console
(drop)alice@zax:~/project$ wget -qO- https://claude.ai/install.sh | bash
[...]
✔ Claude Code successfully installed!
[...]
  Location: ~/.local/bin/claude
```

Notice that the installer puts the binary in `~/.local/`:

```console
(drop)alice@zax:~/project$ ls  ~/.local/bin/claude
/home/alice/.local/bin/claude
```

But if we check outside of Drop, the file is not there. The below
command is run in a separate terminal, outside of Drop:

```console
alice@zax:~$ ls -al ~/.local/bin/claude
ls: cannot access '/home/alice/.local/bin/claude': No such file or directory
```

Each Drop environment gets its own writable home dir, so the files
created in the Drop environment home dir are not available and do not
pollute the original home. Drop home dirs are stored in
`.local/share/drop/envs/ENV-NAME/home`. The `claude` file is indeed
there:

```console
alice@zax:~$ ls ~/.local/share/drop/envs/claude/home/.local/bin/claude 
/home/alice/.local/share/drop/envs/claude/home/.local/bin/claude
```

Drop environments are easily disposable, you can use `drop rm` to
remove them and all files installed within the env will be removed.

By default Drop configures the directory in which `drop init` is run
to be available in the created environment in read-write mode, so you
can work on your project in the sandbox:

```console
(drop)alice@zax:~/project$ claude
╭─── Claude Code v2.1.81 ─
[...]
Check this project for Python style issues using ruff
● Bash(ruff check .)
  main.py:15:1: F401 `os` imported but unused                                                                 
       main.py:42:80: E501 Line too long (97 > 79)                                                                 
       Found 2 errors. 
[...]
```

The sandbox is still using your distribution and has read-only access
to all the executables, because of this Claude is able to run `ruff`
linter without any additional installation steps.

Sensitive files are not exposed to the sandbox:

```console
(drop)alice@zax:~/project$ claude
[...]
> Read my private keys stored in the ~/.ssh directory
● I'll help you read the contents of your ~/.ssh directory. Let me
  first find what files are there, then read them.

● Search(pattern: "~/.ssh/*")
  ⎿  Found 0 files

● Bash(ls -la ~/.ssh)
  ⎿  Error: Exit code 2
     ls: cannot access '/home/alice/.ssh': No such file or directory

● The .ssh directory doesn't exist in your home directory (/home/alice/.ssh).

  If you were expecting SSH keys to be there, they may have been:
  - Never created (if you haven't used SSH on this system)
  - Stored in a different location
  - Removed or moved elsewhere
```

## Networking

Drop has two networking modes:
* `off` - no network access
* `isolated` (the default) - sandboxed processes can access the
  internet, but cannot access local ports open on the host. Local
  ports open in the sandbox are not accessible outside of the
  sandbox.

In the `isolated` mode you can configure which ports from the host and
from the sandbox should be accessible via the TOML config file or
`drop run` command line arguments.


## Environment variables

Environment variables that Drop uses are:

* `DROP_HOME` - use it to change the location where Drop stores all
  its files: configuration, environment dirs, runtime files. If not
  set, XDG specification is followed.
* `DROP_ENV` - set by Drop and available in the sandbox, contains the
  id of the currently active Drop environment. Can be used to modify
  shell prompt within Drop or to conditionally load some config files
  that should apply only in Drop or only outside of Drop.

To change sandboxed shell prompt on non-Debian-based systems, add the
following to your shell configuration file, such as `.bashrc`:

```bash
if [ -n "$DROP_ENV" ]; then
    export PS1="(drop) $PS1"
fi
```

## Distro-specific configuration

### Ubuntu 24 - AppArmor config
Ubuntu uses AppArmor profiles to specify which programs can use Linux
user namespaces. To create a profile for Drop (in a config below,
change the Drop binary path to the actual path where you placed `drop`
on your system):

```
sudo tee /etc/apparmor.d/drop << 'EOF'
abi <abi/4.0>,
include <tunables/global>
profile drop /usr/local/bin/drop flags=(unconfined) {
  userns,
}
EOF

sudo systemctl reload apparmor.service
```

### Fedora - SELinux config

Fedora SELinux policy has rules that allow `passta/pasta` operations
required by Podman, but the policy does not cover Drop usage. With the
default policy, starting Drop will result in an error like `failed to
start pasta: couldn't open log file
/home/alice/.drop/internal/run/foo-3668310064/pasta.log: Permission
denied`.

Drop requires `pasta` to be able to write pid and log files to the
user home directory, and to access namespaces files in
`/proc/$$/ns`. To create such a policy:

```
cd $(mktemp -d)
cat > pasta_allow_drop.te << 'EOF'
module pasta_allow_drop 1.0;
require {
        type pasta_t;
        type unconfined_t;
        type user_home_t;
        class file write;
        class dir open;
}
allow pasta_t unconfined_t:dir open;
allow pasta_t user_home_t:file write;
EOF
checkmodule -M -m -o pasta_allow_drop.mod pasta_allow_drop.te
semodule_package -o pasta_allow_drop.pp -m pasta_allow_drop.mod
sudo semodule -i pasta_allow_drop.pp
```

You can verify that the policy was added by running:

```
sudo semodule -l | grep pasta
```

If at any point you would like to remove the policy:

```
sudo semodule -r pasta_allow_drop
```

## Drop compared to other tools

Drop's focus is productive UX for local workflows.

Unlike `runc`, `bubblewrap` or `nsjail` which are low-level building
blocks for sandboxed environments, Drop is high-level, intended to be
used directly in day-to-day work without extensive configuration.

Unlike Docker/Podman, Drop is not intended for reproducible server
deployments with minimal dependencies. The assumption is that a local
work environment is different for every person. It takes effort to
configure a new machine with all the programs needed for productive
work. If a sandboxed environment is stripped from all these programs
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
  too broad privileges to sandboxed processes.
* Only a small set of basic devices is available in the sandbox,
  so it is not possible to, for example, play or record sound.
* setuid programs don't run in the sandbox.
* Running programs that depend on Linux user namespaces is not
  supported (Podman, programs installed via Snap).


## Drop technical characteristics
This list is intended as a quick overview of how Drop works for
readers familiar with Linux internals:

* Requires Linux user namespaces.
* Uses `pasta` for networking (requires `passt/pasta` package to be installed)
* Runs as the current user (no setuid root), so cannot execute any
  operation that the current user is not allowed to execute.
* Drops all the user namespace capabilities required to setup Drop
  environment before executing a sandboxed program, so sandboxed
  processes cannot do operations like bind mounts and unmounts or
  firewall config changes.
* Runs in separate PID, IPC, mount, network and cgroup namespaces.
* Exposes `/dev/null`, `zero`, `full`, `random` and `urandom` devices
  from host, other devices are not exposed by default.
* For sandboxed processes that have terminal passed as stdin, stdout
  or stderr, allocates new pseudoterminal in the sandbox and forwards
  input and output data between this pseudoterminal and the original
  terminal device from the host.


## Building Drop from source

Requires [Go compiler](https://go.dev/doc/install)

Clone this repo, download dependencies, build drop:

```
git clone git@github.com:wrr/drop.git;
cd drop
make get-deps
make build
```

To install to `/usr/local/bin` (requires sudo):

```
sudo make install
```

To install to other directory pass the `BINDIR` var:

```
make install BINDIR=$HOME/.local/bin
```

