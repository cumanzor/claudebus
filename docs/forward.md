# Forwarding a relay mailbox to a webhook

`cbus forward` delivers one relay mailbox to an outbound HTTPS webhook, so a hosted bot
or automation can receive bus messages with nothing listening inbound on your side.

```text
peer --cbus send--> relay mailbox --durable stream--> cbus forward --HTTPS POST--> webhook
```

It is a durable relay consumer, the same stream `cbus connect` uses. It sends each message
to the webhook and acknowledges it only after the webhook settles it. The relay keeps the
cursor, so there is no local state file, and it runs on any machine with relay credentials.

The webhook answers on the bus by running an ordinary `cbus send` with the same relay
credentials, or by any other route you give it. `cbus forward` carries one direction only.

## Setup

Relay access works as for any remote peer: `CBUS_SITE_<HOST>_URL` and `cbus auth set <host>`
([relay.md](relay.md)). The stream sends only the bearer token, so a relay behind a front
door needs the same `/tail/durable-v1` exception `cbus connect` does
([security.md](security.md#deploying-a-relay)). Then write the webhook's address and key to a file only you can read:

```sh
( umask 077; read -rp 'URL: ' u; read -rsp 'KEY: ' k; echo
  printf 'url=%s\nkey=%s\n' "$u" "$k" > ~/.config/cbus-hook )
```

`url` must be `https`; plain `http` is accepted only to loopback, for local receivers. `key`
is sent as `Authorization: Bearer <key>`. The file must not be readable by group or others
(not checked on Windows).

Try it, then start it:

```sh
cbus send demo@server/hook --from demo@server/laptop -- 'hello'
cbus forward demo@server/hook --webhook-config ~/.config/cbus-hook --dry-run   # logs the next message; no POST, no ACK
cbus forward demo@server/hook --webhook-config ~/.config/cbus-hook --once      # delivers what is queued, then exits
```

Without `--once` it runs until stopped and reconnects by itself.

**A mailbox with a backlog:** everything already queued is delivered on the first run. Check
with `cbus list demo@server` first.

**One consumer per mailbox:** the forwarder holds the consumer id `forward-<alias>`, and the
relay refuses a different active consumer. Do not also connect a session to that alias.

## What the webhook receives

Each message is one POST with a JSON body:

```json
{"id": "1793686899527647914.000001.6dc0bd3cbd1bfdce.json",
 "from": "demo@server/laptop", "to": "demo/hook", "ts": "2026-11-03T06:21:39Z",
 "text": "hello"}
```

| Field | Meaning |
|---|---|
| `id` | The relay spool id. Stable across redelivery, so **dedupe on it**. |
| `from`, `to`, `ts`, `text` | As sent. `from` is not verified (see [Security](#security)). |
| `truncated` | Present when `text` was over 16 KiB: the original length in bytes. `text` is cut on a UTF-8 boundary. |
| `relayId`, `eventId` | Present when the message carries them. |

Delivery is **at-least-once**. A crash after a successful POST and before the ACK sends that
message again. Delivery is in order, one message at a time.

Presence events are acknowledged and not sent.

## How a response settles a message

| Response | Result |
|---|---|
| 2xx | Delivered, ACKed |
| A message over 256 KiB, or one that cannot be read | Dead-lettered without a POST, ACKed |
| Other 4xx | Dead-lettered at once, ACKed: the message will never be accepted |
| 401, 403, 408, 429, 5xx, a 3xx, a timeout or a network error | Retried with backoff from 2 s up to 5 min, then dead-lettered after `--max-attempts` (default 10) |

Redirects are never followed: Go would replay the POST as a bodiless GET and report success.
Update the URL if the endpoint moves.

`--timeout` bounds one request (default 30 s). A receiver slower than that never succeeds, so
raise it for one that does its work before answering, for example `--timeout 90s`.

Retries hold up later messages, because delivery is in order. With the default settings a
failing message gives up after about 14 minutes of backoff plus the request time, so **a longer
webhook outage dead-letters messages one at a time**. Raise `--max-attempts` if you would
rather wait.

### Dead letters

A dead letter is appended, with the raw message, to
`$CBUS_DIR/forward/<channel>@<host>/<alias>.deadletter.jsonl` (mode 600, or `--dead-letter FILE`)
before the ACK. If that write fails, the message stays on the relay and the forwarder reconnects
and tries again. Check this file: nothing else tells you a message was given up on, apart from
the log line.

## Rate cap

`--per-min` and `--per-hour` (default 6 and 30) cap the POSTs, retries included. Over the cap the
forwarder holds; nothing is dropped, and messages wait on the relay. This limits the cost when
two automated peers answer each other. The counter is in memory, so a restart resets it.

It limits a loop rather than preventing one. A receiver should not reply to acknowledgements.

## Running it as a service

`--print-service` prints a definition for this forwarder, with its flags. It installs nothing,
so you can read it first. It works on Linux (a systemd user unit) and macOS (a LaunchAgent),
with the install commands in a comment at the top. Windows is not covered.

```sh
cbus forward demo@server/hook --webhook-config ~/.config/cbus-hook --timeout 90s --print-service
```

It holds no secrets: it names the config file instead of copying it. A service does not inherit
your shell, so `CBUS_SITE_<HOST>_URL`, `CBUS_DIR`, `CBUS_HOST`, `CBUS_RELAY_LOCAL_URL` and
`XDG_CONFIG_HOME` go into the definition when set. Print it from a shell that has them.

- **Linux:** a user unit stops at logout unless you run `loginctl enable-linger $USER`. Logs:
  `journalctl --user -u <name>`.
- **macOS:** the relay token is in the Keychain, which a LaunchAgent in your login session can
  read and an ssh session cannot. Logs go to `~/Library/Logs`.

## Security

- **Outbound only.** Nothing new listens. The relay's front door is unchanged.
- **Treat every message as untrusted.** `from` is set by the sender and not verified, and the
  relay has one bearer token with no per-channel access control, so any peer holding it can
  write to this mailbox. A receiver that acts on `text` should treat it as input from another
  program, not as instructions from you.
- **Channels are not a boundary.** Separate channels keep rosters apart. They do not isolate
  anything from a peer that holds the relay token.
- **The key** stays in the config file and is never logged. Rotate it by editing the file and
  restarting. Delivery errors omit the webhook URL.
- **The message text leaves your network.** Everything sent to this mailbox goes to the webhook's
  operator.

See [security.md](security.md) for the relay itself.
