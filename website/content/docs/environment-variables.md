---
title: Environment variables
description: "Environment variables Drop sandbox uses: DROP_HOME, DROP_ENV and DROP_GVISOR_DEBUG_LOG."
weight: 6
---

Environment variables that Drop uses are:

* `DROP_HOME` - if set, changes the location where Drop stores its
  files: configuration, environment dirs, runtime files. If not set,
  the XDG specification is followed.
* `DROP_ENV` - set by Drop and available in the sandbox, contains the
  id of the currently active Drop environment. Can be used to modify
  the shell prompt within Drop or to conditionally load some config
  files that should apply only in Drop or only outside of Drop.
* `DROP_GVISOR_DEBUG_LOG` - if set to a directory path, enables gVisor
  debugging and writes gVisor logs to this directory.

To change the sandboxed shell prompt on non-Debian-based systems, add
the following to your shell configuration file, such as `.bashrc`:

```bash
if [ -n "$DROP_ENV" ]; then
    export PS1="(drop) $PS1"
fi
```

To configure which of your own environment variables are exposed to
sandboxed programs, use the [exposed_vars]({{< ref
"configuration.md#exposed_vars" >}}) setting in the TOML config file.
