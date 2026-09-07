---
title: Installation
weight: 1
---

## Prerequisites

Drop requires passt/pasta package for isolated networking, which is
[available on most Linux distributions](https://passt.top/passt/about/#availability):

```console
$ sudo apt-get install passt  # Debian/Ubuntu
$ sudo dnf install passt      # Fedora
$ sudo pacman -S passt        # Arch
```

{{< tabs >}}

{{< tab name="Install release binary" >}}
Download a prebuilt binary from
[GitHub releases](https://github.com/wrr/drop/releases/latest/) and place it in your PATH:

```
# Set ARCH to either amd64 or arm64
ARCH=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')

curl -o drop -L https://github.com/wrr/drop/releases/latest/download/drop-linux-$ARCH
install drop ~/.local/bin/
```

{{< /tab >}}

{{< tab name="Install with Go" >}}
An alternative to downloading the release binaries is to use the [Go
compiler](https://go.dev/doc/install) (1.24+) to build and install
Drop with a single command:

```
CGO_ENABLED=0 go install github.com/wrr/drop/cmd/drop@latest
```

The option `CGO_ENABLED=0` produces a statically linked binary and does not
require a C compiler, but is not strictly required.
{{< /tab >}}

{{< tab name="Clone and build" >}}
If you would like to build Drop from a cloned git repo (Requires [Go compiler](https://go.dev/doc/install)):

```
git clone git@github.com:wrr/drop.git;
cd drop
make get-deps
make build
```

To install to `/usr/local/bin`:

```
sudo make install
```

To install to other directory pass the `BINDIR` var:

```
make install BINDIR=$HOME/.local/bin
```
{{< /tab >}}

{{< /tabs >}}
