# Drop tour

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
