---
title: gVisor
weight: 5
---

Drop supports two runtimes:

* `native` - sandboxed programs run directly on the host kernel. Linux namespaces are used for isolation.
* `gvisor` - for added isolation, alongside Linux namespaces,
  sandboxed programs run on the [gVisor](https://gvisor.dev) user-space
  kernel.

{{< include "images/drop-runtimes.svg" >}}

To use the gVisor runtime, you need `runsc`
[installed](https://gvisor.dev/docs/user_guide/install/). Then select
the gVisor runtime either in the Drop TOML config by changing `runtime
= "native"` to `runtime = "gvisor"`, or by passing `--runtime=gvisor`
parameter to the `drop run` command, like:

```console
$ drop run --runtime gvisor ps aux
```

Both runtimes support the same config options and create identically
configured sandboxes. A runtime can be changed back and forth for
existing Drop environments.

gVisor adds some performance overhead to system calls and is not 100%
compatible with vanilla Linux kernel, although [compatibility issues
are rare](https://gvisor.dev/application-compatibility/).
