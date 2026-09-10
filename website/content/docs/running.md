---
title: Running
weight: 3
---

Drop's workflow is inspired by Python's virtualenv: create an easily
disposable environment, enter it, work normally - but with enforced
sandboxing.

## Create a new drop environment

`drop init [ENV_ID]` creates a new drop environment. If ENV_ID is
missing, Drop derives the id from your current working
directory path. For example:

```console
alice@zax:~/project$ drop init 
Drop environment created with config at /home/alice/.config/drop/home-alice-project.toml
```

When `drop init` is run for the first time, it creates a
[base.toml](https://github.com/wrr/drop/blob/main/docs/base.example.toml) config file, which by default is shared
by all Drop environments.

The created  `base.toml` config exposes several common dotfiles that are
present in your home dir to Drop environments. The config also exposes
common environment variables. Review the generated defaults, ensure
that no files with secrets are exposed, expose config files of other
programs that you use.

`drop init` also creates a tiny, [environment specific config
file](https://github.com/wrr/drop/blob/main/docs/env.example.toml).
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

## Run a sandboxed program

`drop run [-e ENV_ID] [command]` runs the command within the sandbox.
If `[-e ENV_ID]` is not passed, Drop derives the id from your current
working directory path.

For example, to start a sandboxed shell:

```console
alice@zax:~/project$ drop run bash
```

Within the environment the sandbox restrictions are applied:

```console
(drop)alice@zax:~/project$ file ~/.ssh
/home/alice/.ssh: cannot open `/home/alice/.ssh' (No such file or directory)
(drop)alice@zax:~/project$ echo "evil command" >> ~/.bashrc
bash: /home/alice/.bashrc: Read-only file system
```

## Managing Drop environments

Drop environments are persistent. If a process started by `drop run`
terminates, the environment is still there and the files created by
the process are kept.

For example, create a `new_file` within an environment `webapp`:

```console
$ drop run -e webapp bash -c 'echo hello > ~/new_file'
```

Subsequent `drop run` within the same environment still sees the
created file:

```console
$ drop run -e webapp cat ~/new_file
hello
```

`drop ls` lists all the created environments, `drop rm` removes an
environment and all its files.

With Drop environments you don't need to track what files and dirs
programs created in your home dir. Because environments have their own
home, you can just remove an environment and files created within the
environment are all removed.
