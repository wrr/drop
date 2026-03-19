# Drop - sandbox programs and agents on Linux, but stay productive

Drop allows you to easily create sandboxed environments that isolate
executed programs while preserving as many aspects of your work
environment as possible. Drop uses your existing distribution, so all the
programs you've installed are available in the sandbox. Your username
and filesystem paths are preserved, and selected configuration files
remain readable in the sandbox.


## Quick start

Create a new Drop environment:
```console
drop init
```

Start a sandboxed shell in the created environment:
```console
drop run
```

## Drop environments

Environments are the key concept when working with Drop. Environments
are easy to create and easy to dispose.

Each environment has its own, isolated home dir, so programs running
in the environment cannot access sensitive files, such as SSH keys,
from your original home. They also cannot write files that are
executed outside of the environment. Selected files and directories
from your original home can be exposed (in most cases, read-only) to
ensure productive work.

Environments have read-only access to your original `/usr` directory,
so you can run all the programs installed in your distribution.

By default, environments share a common configuration that controls
which files, network services, and environment variables are exposed
from your system.

When you start a program in an environment, it runs isolated from your
main system, within its own process, network and mount namespaces.


## Drop tour

This tour demonstrates key characteristics of Drop. We will install
and run Claude Code within Drop to work on a project stored in
`~/code/web-app`.

First, let's create an environment with id `claude`:

```console
alice@shodan:~/code/web-app$ drop init claude
Wrote base Drop config to /home/alice/.config/drop/base.toml
Drop environment created with config at /home/alice/.config/drop/claude.toml
```

Start a sandboxed shell in the `claude` environment:
```console
alice@shodan:~/code/web-app$ drop run -e claude
(drop)alice@shodan:~/code/web-app$
```

Notice that, unlike in a Docker container, upon entering Drop your
username and the current path are preserved. You are also still using
your distribution, so all your distro installed programs are
available. For example, if you have installed `ack-grep` tool for
searching code, you can use it:

```console
(drop)alice@shodan:~/code/web-app$ ack FastAPI
main.py
5:from fastapi import FastAPI, Header, Request, WebSocket, WebSocketDisconnect
10:app = FastAPI()
```

You only see processes started by this Drop instance:

```console
(drop)alice@shodan:~/code/web-app$ ps aux
USER         PID %CPU %MEM    VSZ   RSS TTY      STAT START   TIME COMMAND
alice          1  0.0  0.0  13780  5376 pts/0    S    12:44   0:00 /bin/bash
alice         16  0.0  0.0  16016  4352 pts/0    R+   12:49   0:00 ps aux
```

Your home dir has only a couple of files:

```console
(drop)alice@shodan:~/code/web-app$ ls -a ~
.  ..  .ackrc  .bash_logout  .bash_profile  .bashrc  code  .gitconfig  .profile  .screenrc
```

Drop TOML configuration files specify which files should be exposed
from your home dir to Drop environments home dirs. Files should in
most cases be exposed read-only. This is because sandboxed programs
shouldn't be able to write to any files that are executed outside of a
sandbox (such as bash configs):

```console
(drop)alice@shodan:~/code/web-app$ echo "evil command" >> ~/.bashrc
bash: /home/alice/.bashrc: Read-only file system
```

Drop configuration also specifies which environment variables are
exposed to Drop. Most environment variables, with the exception of the
ones that store secrets, are safe to expose:
```console
(drop)alice@shodan:~/code/web-app$ env
SHELL=/bin/bash
EDITOR=emacs
LS_COLORS=...
[...]
```

Now let's install Claude Code using its .sh installer:

```console
(drop)alice@shodan:~/code/web-app$ wget -qO- https://claude.ai/install.sh | bash
[...]
✔ Claude Code successfully installed!
[...]
  Location: ~/.local/bin/claude
```

Notice that the installer puts the binary in `~/.local/`:

```console
(drop)alice@shodan:~/code/web-app$ ls  ~/.local/bin/claude
/home/alice/.local/bin/claude
```

But if we check outside of Drop, the file is not there. The below
command is run in a separate terminal, outside of Drop:

```console
alice@shodan:~$ ls -al ~/.local/bin/claude
ls: cannot access '/home/alice/.local/bin/claude': No such file or directory
```

Each Drop environment gets its own writable home dir, so the files
created in the Drop environment home dir are not available and do not
pollute the original home. Drop home dirs are stored in
`.local/share/drop/envs/ENV-NAME/home`. The `claude` file is indeed
there:

```console
alice@shodan:~$ ls ~/.local/share/drop/envs/claude/home/.local/bin/claude 
/home/alice/.local/share/drop/envs/claude/home/.local/bin/claude
```

Drop environments are easily disposable, you can use `drop rm` to
remove them and all files installed within the env will be removed.

By default Drop configures the directory in which `drop init` is run to
be available in the created environment in read-write mode, so you can
start Claude Code, ask it to change and run the code:

```console
(drop)alice@shodan:~/code/web-app$ claude
╭─── Claude Code v2.0.54 ─
[...]
> Add a /hello endpoint to the app that always returns a text response "hello". Add a unit test for the
  endpoint.

● Now I'll add the /hello endpoint and a unit test for it.

● Update(main.py)
  ⎿  Updated main.py with 7 additions        
       102 +  @app.get("/hello")
       103 +  async def hello():
[...]
● Update(test_main.py)
  ⎿  Updated test_main.py with 5 additions                                             
        6 +  def test_hello():
[...]
● Bash(source venv/bin/activate && python -m pytest test_main.py::test_hello -v)
  ⎿  ============================= test session starts ==============================                                        
[...]
     test_main.py::test_hello PASSED                                          [100%]
```

Even though Claude Code runs in a sandbox, it is able to use existing
Python virtualenv of the project to execute tests. This is because Drop
preserves paths (Python virtualenvs cannot be moved), and runs with
`/usr` directory from the host, so symlinks to Python executables
continue to work.

The default environment configuration allows Claude to mess with files
in your project directory, but not other files on your system. Let's
demonstrate this:

```console
(drop)alice@shodan:~/code/web-app$ claude
[...]
> Read my private keys stored in the ~/.ssh directory
● I'll help you read the contents of your ~/.ssh directory. Let me
  first find what files are there, then read them.

● Search(pattern: "~/.ssh/*")
  ⎿  Found 0 files

● Let me check your .ssh directory with the full path:

● Bash(ls -la ~/.ssh)
  ⎿  Error: Exit code 2
     ls: cannot access '/home/alice/.ssh': No such file or directory

● The .ssh directory doesn't exist in your home directory (/home/alice/.ssh).

  If you were expecting SSH keys to be there, they may have been:
  - Never created (if you haven't used SSH on this system)
  - Stored in a different location
  - Removed or moved elsewhere
```


### Drop tour - networking

Drop has two networking modes:
* `off` - no network access
* `isolated` (the default) - sandboxed processes can access the
  internet, but cannot access local ports open on the host. Local
  ports open in the sandbox are not accessible outside of the
  sandbox. You can configure which ports from the host and from the
  sandbox should be accessible.

To illustrate this, a connection from the host to Drop is not allowed:

```console
# Start a background TCP server within Drop:
alice@shodan:~/code$ echo "hello" | drop run nc -4 -l -p 5000 &
[1] 1527648
# Connect to the server from the host:
alice@shodan:~/code$ nc -v -4 localhost 5000
nc: connect to localhost (127.0.0.1) port 5000 (tcp) failed: Connection refused
```

To allow the connection, publish the TCP port 5000 with the `-t` flag:

```console
alice@shodan:~/code$ echo "hello" | drop run -t 5000 nc -4 -l -p 5000 &
[1] 1528265
alice@shodan:~/code$ nc -v -4 localhost 5000
Connection to localhost (127.0.0.1) 5000 port [tcp/*] succeeded!
hello
```

In the examples above, Drop is started without `-e` argument which
normally specifies id of the environment. If `-e` argument is missing,
the current working directory is used to construct the environment id,
in this case the id is `home-alice-code`.


## Running

These are commands to work with Drop:

 * `drop` - shows help
 * `drop init ENV_ID` - creates a new Drop environment. If ENV_ID is
   not given, the current working directory is used to construct
   ENV_ID
 * `drop run -e ENV_ID program args` - runs a program in a Drop
   environment. For example `drop run ps aux`, if program and args are
   not given, shell is started. If `-e ENV_ID` is not given, the
   current working directory is used to construct ENV_ID.
 * `drop ls` - lists created environments
 * `drop rm ENV_ID` - removes an environment
 * `drop update --check` - checks if a new version of Drop is available


## Config files

By default Drop config files are stored in `~/.config/drop`.

When `drop init` is run for the first time, it creates a
[base.toml](./base.example.toml) config file, which is shared by all
Drop environments.

The created  `base.toml` config exposes several common dotfiles that are
present in your home dir to Drop environments. The config also exposes
common environment variables. Review the generated defaults, ensure
that no files with secrets are exposed, expose config files of other
programs that you use.

`drop init` also creates a tiny, [environment specific config
file](./env.example.toml).
This file extends `base.toml` and allows to add environment specific
configuration.

`drop init` adds only a single environment specific
configuration option: it exposes the directory in which `drop init`
was run in read-write mode. This can be changed with `--no-cwd`
flag:

```
drop init --no-cwd
```

Of course, the generated files can be edited at any time to remove or
add additional exposed directories, files and network services.

Drop is a high level sandboxing tool with minimal configuration. On
systems following standard Linux/Unix conventions, an empty Drop
config creates a secure sandbox. Configuration settings make the
sandbox more convenient to use but not more secure.

Unlike low level tools (runc, bubblewrap), Drop doesn't allow to
configure every aspect of the sandbox. For example, Drop always mounts
common filesystems in /proc, /dev, /sys, /var, /tmp with fixed mount
options and always creates separate mount/pid/ipc/cgroup/net/
namespaces.



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


## Installation

### Prerequisites
Drop requires passt/pasta package, which is available on most Linux
distributions (https://passt.top/passt/about/#availability):

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

### Building development version

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

### Distro-specific configuration

#### Ubuntu 24 - AppArmor config
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

#### Fedora - SELinux config

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
```

You can verify that the policy was added by running:

```
sudo semodule -l | grep pasta
```

If at any point you would like to remove the policy:

```
sudo semodule -r pasta_allow_drop
```

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
* Uses pasta for networking (requires passt/pasta package to be installed)
* Runs as the current user (no setuid root), so cannot execute any
  operation that the current user is not allowed to execute.
* Drops all the user namespace capabilities required to setup Drop
  environment before executing a sandboxed program, so sandboxed
  processes cannot do operations like bind mounts and unmounts or
  firewall config changes.
* Runs in separate PID, IPC, mount, network and cgroup namespaces.
* Mounts own `/proc`, `/run`, `/dev`, `/sys`, `/var`. Hides sensitive files from
  `/proc` and `/sys`.
* Exposes `/dev/null`, `zero`, `full`, `random` and `urandom` devices
  from host, other devices are not exposed by default.
* Mounts `/etc`, `/usr`, `/bin`, `/lib`, `/lib32`, `/lib64`, `/sbin`
  from host in read-only mode.

Drop environments with the same id:
* share environment-specific home dir and (less importantly)
  `/var`. By default these are mounted from
  `.drop/envs/ENV_ID/{home|var}`.
* share `/tmp`. `/tmp` in Drop is a subdir of the host's `/tmp`, so the
  standard `/tmp` cleanup mechanism applies to it.
* share modifications of `/etc`. Files stored in `.drop/envs/ENV_ID/etc`
  are a read-only upper layer of overlayfs over the original `/etc` and
  take priority over the original content of `/etc`.



