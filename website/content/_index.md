---
layout: hextra-home
---

{{< hextra/hero-headline >}}
Linux sandboxing that doesn't get in your way
{{< /hextra/hero-headline >}}

{{< hextra/hero-subtitle >}}
Isolate programs and LLM agents without leaving your familiar work environment
{{< /hextra/hero-subtitle >}}

{{< asciinema file="drop-sandbox-demo.cast" speed="1.5" poster="npt:0:11" >}}


## Use cases
{{< feature-list usecases="true" >}}
{{< feature title="Skip coding agent permission prompts">}}

Run agents with `--dangerously-skip-permissions` and let Drop
enforce permissions for you. A hallucinated `rm -rf ~` wipes a
disposable environment, not your actual home dir. A prompt injection
targeting `~/.ssh` finds nothing. A connection to services running on
a localhost is rejected.

{{< /feature >}}

{{< feature title="Isolate out-of-distro packages">}}

Install programs from public package repositories like PyPI, NPM and
Go Packages, but keep your system protected in case the program is
compromised or is a victim of a supply chain attack. 

{{< /feature >}}
{{< /feature-list >}}

## How it works
{{< feature-list >}}
{{< feature title="Disposable, isolated environments">}}

Inspired by Python's virtualenv, Drop lets you create and enter easily
disposable environments. Each environment has its own home
directory while the original home is hidden.

{{< /feature >}}

{{< feature title="Your existing distribution">}}

Unlike Docker/Podman, Drop uses your existing distribution, so there
is no container setup work: every program you've already installed is
available in the sandbox.

{{< /feature >}}


{{< feature title="Flexible config language">}}

High-level TOML config lets you specify which files, dirs and local
network services should be exposed to the sandbox. By default, all Drop
environments share a base config, so you can configure Drop once and
then create new environments without any configuration work.

{{< /feature >}}

{{< feature title="Rootless">}}

Drop doesn't require root to run. It runs within a Linux user
namespace, with own processes, mount, network, IPC and cgroup
namespaces. Drop drops all the user namespace capabilities before
executing a sandboxed program, so the program cannot do privileged
operations within the user namespace, like bind mounts.

{{< /feature >}}

{{< feature title="gVisor integration">}}

As an option, Drop supports running programs on the gVisor user-space
kernel. This is an additional isolation layer that prevents programs
from accessing the host kernel directly, significantly reducing
potential to exploit kernel vulnerabilities.

{{< /feature >}}

{{< /feature-list >}}
