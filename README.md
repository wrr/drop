# Drop - Linux sandboxing for CLI programs that doesn't get in the way


## Motivation

Even though process isolation solutions such as virtual machines or
Docker/Podman are widely used for deploying production software to
servers, they are not as widely used for installing and running
programs and LLM agents on local machines. Linux users still commonly
run programs from third-party repositories such as PyPi, NPM, RPM or
via .sh installation scripts using their normal user accounts without
any added isolation. This exposes all sensitive files like .ssh keys,
browser cookies or saved browser passwords to the third-party programs
and risks fully compromising the machine if an installed program or
its dependency turns out to be compromised.

Drop's goal is to be more convenient for local workflows than Docker
or virtual machines. Drop uses your existing distribution, so all the
programs you've installed are available in the sandbox. Your username
and filesystem paths are preserved, and selected configuration files
(like .bashrc) remain readable in the sandbox.

## Installation

The simplest way to install Drop is to download an executable from
[GitHub releases](https://github.com/wrr/drop/releases/latest/) and
place in in your PATH.

Download Drop:
```
# Set ARCH to either amd64 or arm64
ARCH=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')

curl -o drop -L https://github.com/wrr/drop/releases/latest/download/drop-linux-$ARCH
```

On Ubuntu 24 which uses AppArmor to control which programs are allowed
to use user namespaces, it is best to have `drop` executable owned and
writeable by root only, so, for example, installed to
`/usr/local/bin`:

```
sudo install -m 755 drop /usr/local/bin/
```

Alternatively, on systems that do not use AppArmor, local installation
which doesn't require root and keeps the executable owned by your
users is also good:

```
install -m 755 drop ~/.local/bin/
```

### Building from source

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

## Ubuntu 24 - AppArmor config
Ubuntu uses AppArmor profiles to specify which programs can use Linux
user namespaces. To create a profile for Drop (assuming the binary is
in `/usr/local/bin/drop`):

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

Note that to keep the AppArmor mechanism fully effective, it is best if binaries
that are allowed to use Linux user namespaces are owned by root
and writable only by root. If you install `drop` in, for example,
`~/bin` and keep the binary owned by you, someone who gains access to
your uses account will be able to bypass the AppArmor restrictions (by
replacing `~/bin/drop` with some other binary that would normally not
have access to user namespaces).

## Fedora - SELinux config

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

If at anypoint you would like to remove the policy:

```
sudo semodule -r pasta_allow_drop
```

## Running

These are the basic commands to work with Drop:

 * `drop` - starts a sandboxed shell
 * `drop program args` - runs a sandboxed program, for example `drop ps aux`
 * `drop -h` - shows help


## Config file

Upon the first start Drop creates [a default configuration
file](./config.example.toml) in `~/.config/drop/config.toml`. The
comments in the file explain each setting.

The default config exposes several common dotfiles, that are present
in your home dir to Drop environments. The config also exposes common
environment variables. Review the generated defaults, ensure that no
files with secrets are exposed, expose config files of other programs
that you use when working with CLI.

Drop is a high level sandboxing tool with minimal configuration. On
systems following standard Linux/Unix conventions, an empty Drop
config creates a secure sandbox. Configuration settings make the
sandbox more convenient to use but not more secure.

Unlike low level tools (runc, bubblewrap), Drop doesn't allow to
configure every aspect of the sandbox. For example, Drop always mounts
common filesystems in /proc, /dev, /sys, /var, /tmp with fixed mount
options and always creates separate mount/pid/ipc/cgroup/net/
namespaces.

Drop does not aim to maximally restrict each sandboxed program with
program specific profiles that, for example, restrict available system
calls to the absolute minimum. Creation of such profiles is difficult and
time-consuming.  Instead, the aim is to apply the same restrictions to
all sandboxed programs, without requiring extensive program-specific
configuration.


## Environment variables

Environment variables that Drop uses are:

* `DROP_HOME` - use it to change the location where Drop stores all
  its files - default config, environment dirs, runtime files. If not
  set, `~/.drop` is used.
* `DROP_CONFIG` - overwrites the default config location.
* `DROP_ENV` - set by Drop and available in the sandbox, contains the
  id of the currently active Drop environment. Can be used to modify
  shell prompt within Drop or to conditionally load some config files
  that should apply only in Drop or only outside of Drop.
* `debian_chroot` - set by Drop to modify the default shell prompt on
  Debian-based systems (adds `(drop)` prefix to prompt).

To change sanboxed shell prompt on non-Debian-based systems, add the
following to your shell configuration file, such as `.bashrc`:

```bash
if [ -n "$DROP_ENV" ]; then
    export PS1="(drop) $PS1"
fi
```

## Drop tour

In this tutorial we will install and run Claude Code within Drop to
work on a project `web-app` stored in `~/code/web-app`.

Drop workflow is inspired by the workflow provided by Python
virtualenv. First we will create and enter Drop environment called `claude`:

```console
alice@shodan:~/code/web-app$ drop -e claude
(drop)alice@shodan:~/code/web-app$
```

Notice that, unlike in Docker, upon entering Drop your username and
the current path is preserved. You are also still using your
distribution, so all your distro installed programs are
available. For example, if you have `ack-grep` installed to
search code, you can use it:

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
alice          1  0.0  0.0  13780  5376 ?        S    12:44   0:00 /bin/bash
alice         16  0.0  0.0  16016  4352 ?        R+   12:49   0:00 ps aux
```

Your home dir has only couple of files:

```console
(drop)alice@shodan:~/code/web-app$ ls -a ~
.  ..  .ackrc  .bash_logout  .bash_profile  .bashrc  code  .gitconfig  .profile  .screenrc
```

Drop configuration file (by default stored in `~/.drop/config.toml`)
specifies which files should be exposed from your home dir to Drop
environments home dirs. Config files that you expose to Drop should in
most cases be exposed read-only. This is because sandboxed programs
shouldn't be able to write to any files that are executed outside of a
sandbox (such as bash configs):

```console
(drop)alice@shodan:~/code/web-app$ echo "evil-command" >> ~/.bashrc
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
...
```

Now let's install Claude Code using its .sh installer:

```console
(drop)alice@shodan:~/code/web-app$ wget -qO- https://claude.ai/install.sh | bash
Setting up Claude Code...

✔ Claude Code successfully installed!

  Version: 2.0.54

  Location: ~/.local/bin/claude


  Next: Run claude --help to get started

✅ Installation complete!
```

The important thing to notice is that the installer puts the binary in
`~/.local/`:

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

Each Drop environment gets its own home dir. The files created in the
Drop environment home dir are not available and do not pollute the
original home dir. Drop home dirs are stored in
`.drop/envs/ENV-NAME/home`. The `claude` file is indeed there:

```console
ls ~/.drop/envs/claude/home/.local/bin/claude 
/home/alice/.drop/envs/claude/home/.local/bin/claude
```

Drop environments are easily disposable, you can just remove them and
all files installed within the env will be removed.


The default Drop config makes the current working directory available
for reading and writing (with the exception of the `.git` subdir), so you
can start Claude Code, ask it to change and run the code:

```console
(drop)alice@shodan:~/code/web-app$ claude
╭─── Claude Code v2.0.54 ─
...
╰───────────────────────────
> Add a /hello endpoint to the app that always returns a text response "hello". Add a unit test for the
  endpoint.

● Now I'll add the /hello endpoint and a unit test for it.

● Update(main.py)
  ⎿  Updated main.py with 7 additions        
       102 +  @app.get("/hello")
       103 +  async def hello():
       104 +      return Response(
       105 +          content="hello",
       106 +          media_type="text/plain"
       107 +      )
       108 +  

● Update(test_main.py)
  ⎿  Updated test_main.py with 5 additions                                             
        6 +  def test_hello():
        7 +      response = client.get("/hello")
        8 +      assert response.status_code == 200
        9 +      assert response.text == "hello"
       10 +      assert response.headers["content-type"] == "text/plain; charset=utf-8"

[...]
● Bash(source venv/bin/activate && python -m pytest test_main.py::test_hello -v)
  ⎿  ============================= test session starts ==============================                                        
[...]
     test_main.py::test_hello PASSED                                          [100%]
```

Even though Claude Code runs in a sandbox, it is able to use existing
Python virtulenv of the project to execute tests. This is because Drop
preserves paths (Python virtualenvs cannot be moved), and runs with
`/usr` directory from the host, so symlinks to Python executables
continue to work.

Drop default configuration allows Claude to mess files in your project
directory, but not other files on your system. Let's demonstrate this:

```console
(drop)alice@shodan:~/code/web-app$ claude
╭─── Claude Code v2.0.54 ─
...
╰───────────────────────────
> Read my private keys stored in the ~/.ssh directory
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
...
```


### Drop tour - networking

Drop has two networking modes:
* `off` - no network access
* `isolated` (the default) - sandboxed processes can access the
  internet, but cannot access local ports open on the host. Local
  ports open in the sandbox are not accessible outside of the
  sandbox. You can configure which ports from the host and from the
  sandbox should be be accessible.

To illustrate this, a connection from the host to Drop is not allowed:

```console
# Start a background TCP server within Drop:
alice@shodan:~/code$ echo "hello" | drop nc -4 -l -p 5000 &
[1] 1527648
# Connect to the server from the host:
alice@shodan:~/code$ nc -v -4 localhost 5000
nc: connect to localhost (127.0.0.1) port 5000 (tcp) failed: Connection refused
```

To allow the connection, publish the TCP port 5000 with the `-t` flag:

```console
alice@shodan:~/code$ echo "hello" | drop -t 5000 nc -4 -l -p 5000 &
[1] 1528265
alice@shodan:~/code$ nc -v -4 localhost 5000
Connection to localhost (127.0.0.1) 5000 port [tcp/*] succeeded!
hello
```

In the examples above, Drop is started without `-e` argument which
normally specifies id of the environment. If `-e` argument is missing,
the current working directory is used to construct the environment id,
in this case the id is `home-alice-code`.

## Limitations

* CLI only, X window programs will not run in the sandbox.
* Only a small set of basic devices is available in the sandbox,
  not possible to, for example, play or record sound.
* setuid programs don't run in the sandbox.
* Running other programs that depend on Linux user namespaces not
  supported (Podman, programs installed via Snap).


## Drop technical characteristics
This list is intended as a quick overview of how Drop works for
readers familiar with Linux internals.

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
  from host in read-only mode. On modern distros all of them except
  `/etc` and `/usr` are symlinks to subdirs of `/usr`.

Drop environments with the same id:
* share environment-spoecific home dir and (less importantly)
  `/var`. By default these are mounted from
  `.drop/envs/ENV_ID/{home|var}`.
* share `/tmp`. `/tmp` in Drop is a subdir of the host's `/tmp`, so the
  standard `/tmp` cleanup mechanism applies to it.
* share modifications of `/etc`. Files stored in `.drop/envs/ENV_ID/etc`
  are a read-only upper layer of overlayfs over the original `/etc` and
  take priority over the original content of `/etc`.

