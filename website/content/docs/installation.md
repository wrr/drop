---
title: Installation
weight: 2
---

## Prerequisites

Drop requires passt/pasta package for isolated networking, which is
[available on most Linux distributions](https://passt.top/passt/about/#availability):

```console
$ sudo apt-get install passt  # Debian/Ubuntu
$ sudo dnf install passt      # Fedora
$ sudo pacman -S passt        # Arch
```

## Install Drop

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

## Distro-specific setup

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

Fedora SELinux policy has rules that allow `passt/pasta` operations
required by Podman, but the policy does not cover Drop usage. With the
default policy, starting Drop will result in an error containing
`netns dir open: Permission denied, exiting`.

Drop requires `pasta` to be able to access namespace files in
`/proc/<pid>/ns` that belong to unconfined processes. To create such a
policy:

```
cd $(mktemp -d)
cat > pasta_allow_drop.te << 'EOF'
module pasta_allow_drop 1.0;
require {
        type pasta_t;
        type unconfined_t;
        class dir open;
}
allow pasta_t unconfined_t:dir open;
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
