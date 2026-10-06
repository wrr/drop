# Goals
 
* Support filtering outgoing traffic with firewall rules
* Filter not allowed requests before DNS query is sent out to limit
  DNS exfiltration.
* IP-level filtering, with domain name as the main user-visible target.
* It should be possible to include firewall rules in Drop TOML via
  external files. Multiple such files should be supported.
* Support interactive mode in which user is prompted to make an
  Allow/Always Allow/Deny/Always Deny decision. Permanent decision
  should be automatically saved by Drop in a firewall rules files
  selected by the user.


# Non-goals

* Strict domain filtering: allowed domain, means requests are allowed
  to IP addresses associated with this domain. If IP address also
  serves other domains, for example, via HTTP/SNI, requests to such
  domains are also implicitly allowed. Stronger guarantee would
  require application level filtering, such as could be provided by a
  SNI proxy for HTTP requests. This is out of scope of the Drop
  firewall proposed in this document.

* Incoming traffic filtering. `tcp_published_ports` and
  `udp_published_ports` (by default empty) remain the only mechanism
  provided by Drop to filter incoming traffic. For example, it is not
  possible to allow requests to a TCP port open in Drop, but only from
  specific IP addresses (host firewall rules of course are still used
  and can be used for more sophisticated incoming traffic filtering).


# Configuration

When `net.mode` is `isolated` `[net]` gets a new firewall setting. If
it is present, firewall is enabled in the isolated mode.

```
[net.firewall]

# What to do if no rule matches
default = "ask"
# What to do if the prompt cannot be shown (for example no terminal available). Allowed values: "deny", "allow"
ask_fallback = "deny"
ask_store_file = "user.rules"


rules = [
 "npm.rules"
 "user.rules"
]
```

Extending and changing the defaults with per environment settings is
possible with the same extension logic as for other settings.

TODO(wrr): current config makes it impossible to disable firewall per
environment if it is enabled in base.toml, or to reset rules per
environment.
maybe:
```
net.firewall.disable = true
net.firewall.clear_extended_rules = true
```
?

TODO(wrr): logs related config design

The rules files paths are relative to the config file. As with the
drop config files, sandboxed process must not be able to edit them or
even better read them. Drop may add checks to check and enforce this.

# Rules format


```
allow  registry.npmjs.org:21,443
allow  *.githubusercontent.com
deny   telemetry.example.com
allow  example.com:8443/udp
ask    google.com/tcp
allow  home.example.com:8080 localnet
allow  73.23.75.23:45
```

More specific domain matching rules takes precedence over more generic
ones.

IP addresses take precedence over domain names.

Missing ports in a rule mean all the ports. Such a rule has lower
precedence than a rule for identical domain, but with the port
specified.

Missing protocol means TCP.

If several rules for the exact same domain exist, deny rule wins, then
allow.
`*` matches also apex domain `*.example.com` -> `example.com`

Connections to local network and special addresses are rejected, unless the
rule has the localnet annotation. These include:

* private and special ranges: RFC1918, 100.64.0.0/10 (CGNAT,
  Tailscale), link-local including 169.254.169.254 (cloud metadata),
  loopback, fc00::/7, fe80::/10,
* other non-default routes from the host routing table (VPNs, IPv6
  LAN prefixes, which are globally routable addresses).

The check is done on the resolved IP address at connection time, so
it blocks DNS rebinding.

Port ranges are not supported with v1.

ICMP and other protocols are always rejected in v1.

# Rules lifetime
If the user allows or denies a connection, the decision is effective
until the end of the current Drop session.

If the user always allows or always denies a connection, the decision
also applies to the current session and is also written to the
`ask_store_file` and applies to all future Drop sessions using the
file. Existing Drop sessions do not reload the file and apply the
added rules.

# IPv6

Firewall v1 will not allow ipv6 traffic, but without any design
decision that would make this decision inherent and would make ipv6
support hard to add later
- The DNS proxy strips AAAA answers (with NOERROR), so clients don't try IPv6 at all.
- nftables rejects all IPv6 egress on pasta's interface. Reject, not
  drop, so clients fail over to IPv4 immediately instead of timing
  out.


# Architecture

Drop with firewall enabled continues to use the same `pasta` provided
networking as the current `isolated` mode. The filtering rules are
inserted via nftables.

Sandboxed process of course cannot have CAP_NET_ADMIN in the user
namespace, as it is already the case.

## Firewall component

* Created by Drop parent process.
* Installs firewall rules before Drop child process receives a message to run a sandboxed program.
* Uses google/nftables bindings
* Uses nfqueue (florianl/go-nfqueue ?) to make filtering decisions.
  Both libraries can be pointed to the sandbox user namespace, so firewall rules apply to the namespace.
* On systems without nftables support, fails Drop execution with actionable message
* Receives domain->ipaddresses mapping from DNS goroutine
* Blocks DNS (TCP/UDP 53) and DoT (853) to anything except the DNS
  proxy. DoH runs on 443 and can't be blocked by port, but a DoH
  client still needs to reach its server: by name (goes through the
  proxy and gets prompted like any other domain) or by hardcoded IP
  (raw IP prompt). Optionally, deny well-known DoH server names by
  default.
* Is responsible for displaying all the interactive prompts and deny decisions
* Is responsible for updating rules in response to the user
  prompt decisions. Implements proper locking to prevent files
  corruption
* if a connection attempt is made to an IP address for which domain
  name was not resolved, the IP address is shown in the prompt and any
  added rule applies to the raw IP address.
* Exposes a high level API to be used by DNS goroutine

## DNS proxy component

* Started by Drop parent process
* Listens on an ephemeral local port, the only port exposed to a sandbox for DNS resolution purposes (passed to the sandbox via /etc/resolv.conf)
* Upon receiving a query, passes the domain to Firewall above for resolution. If Firewall returns:
  * Deny: returns NXDOMAIN
  * Allow: runs the query.
  * Ask: DNS proxy asks the firewall for a fake, fresh IP address from
    a pool maintained by the firewall (198.18.0.0/15 range) and
    returns this address to the client as the address resolution. When
    a connection to this IP is made, Firewall displays the prompt to
    the user (at the connection time port and protocol are known), and
    if the user allows the requests, runs a real DNS query, remembers
    the peer IP address, adds a NAT rule to map the fake IP to the
    peer's IP, and allows the connection.
* passes query response to the Firewall.
* Handles only A and AAAA queries. HTTPS/SVCB get an empty NOERROR
  answer (browsers fall back to A/AAAA). Other types (TXT, MX, SRV,
  ...) are refused: rarely needed in a sandbox, to revisit if real use
  cases come up.
* Rules are matched against the queried name. IP addresses returned
  through a CNAME chain are associated with the queried name, not
  with the intermediate CNAME targets.
* Already established connections are not dropped when domain->IP
  address mapping is changed. Drop also doesn't discard old mappings
  for which TTL has expired, unless sandboxed program runs a new DNS
  query.  The assumption is that once a connection to an IP address is
  allowed within a session, there is little gain from complexity
  needed to later deny such connection based on DNS entry expiration.

### DNS based filtering alternative mechanism

A considered alternative mode (rejected, unless building v1 surfaces
unexpected problems).

The user is prompted when DNS query is run and destination port is not
yet known. If the user accepts the query to run a rule allowing
connection to `ask_default_allowed_ports` is added automatically
(either for local session or stored in the rules file) If later a
connection is made to a port which is on this list, it is allowed. If
a connection uses a different port, a new prompt is displayed.  If the
list is empty, the user always needs to accept first the DNS
resolution and then the actual connection attempt.  If
ask_default_allowed_ports is set to a string "*", a rule allowing
connections to all ports is added.  ask_default_allowed_ports = [22,
443]

Advantages of this approach:

* For a non-existent domain, the client always receives the correct NXDOMAIN
  answer for each DNS query.
* client sees real IP of the peer, there is no NAT involved.
* less complex

Drawbacks of this approach:
* User is queried before the destination port and protocol are known, the
  answer must use some default list of ports.
* DNS timeouts are short, often 1-5 seconds, not enough time for user
  to respond to the prompt, which results in timed-out queries, not
  the actual NXDOMAIN.

# Firewall prompt

Firewall will display the interactive prompt in the sandboxed
terminal. The prompt will be shown by the Drop parent process, which
runs outside the sandbox and has access to the terminal primary
end, isolated from the Sandboxed processes.

The parent pauses forwarding the child's output to the terminal (it
buffers it) as well as keyboard input to the child. Prompt receives
the keyboard input.

TODO(wrr): to test in practice, are interactive prompts usable when
multiple connections are attempted very quickly, such as in case of
package manager updates? Should multiple prompts be combined into one?
Should there be an auto accept mode that lasts for some time?

## Fake prompts
Because sandboxed process have access to the same terminal window,
they can spoof Drop's prompt. For a firewall prompt, the
consequences are rather benign, as the user is not entering sensitive
information, only allow/deny decisions. Drop config will allow the
user to set a string that will be displayed in the lower right corner
of the Drop prompt. Because sandboxed process shouldn't have access to
the Drop config files, it won't be able to spoof the string, which
will allow the user to distinguish genuine Drop prompts

## Tricking the user
A malicious program can attempt to trick the user into automatically
accepting a rogue request. A terminal game can for example require the
user to continuously press 'a' button, when the user is doing so, the
game can make a malicious requests, Drop will show the prompt which
will be immediately [a]ccepted. A remedy for this is to monitor the
input keys and introduce a read delay if the user was entering keys just
before the prompt was displayed.

## Terminal restoration

A non-trivial issue is restoration of the terminal state after the
prompt is displayed. The primary, although not bullet-proof approach
will be to use terminal resize signal to prompt sandboxed process to
redraw itself.

## Prompt outside of the terminal

The design will try to accommodate a future extensions where prompts
could alternatively be displayed in an external GUI application or a
local browser, avoiding some pitfalls of terminal-based prompts at the
price of less smooth user experience.

 
