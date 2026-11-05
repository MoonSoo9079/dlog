# Dlog

Dlog is a small central log service for Docker hosts. One server stores logs. An agent container on each host follows the logs of every running container and pushes them to the server.

The agent does not listen on a port. It only dials the server you configure, the same way a Zabbix agent in active mode only talks to its server.

## Authorization

Every agent carries the same certificate and private key. That certificate is what authorizes automatic registration. The agent signs a registration request with its hostname. The server checks the signature against the certificate it trusts and creates the host if the name is new. An existing enabled host is accepted again, so a rebuilt agent with an empty state directory comes back without an operator copying a key. A disabled host stays disabled.

You do not create hosts by hand, and you do not put a per-host deploy key on the agent. Copy `agent.crt` and `agent.key` once. Those two files are identical on every agent. `DLOG_HOSTNAME` is the host's name and must stay stable.

On first registration the server gives the agent a host token. The agent stores it in its state directory and sends it on later requests. That token is not an install step. If it stops working, the agent signs a new registration with the same certificate and replaces it.

| Zabbix | Dlog |
| --- | --- |
| Host registered on the server | Host row, created when the agent first registers |
| Shared credential | One ECDSA P-256 certificate and key, the same on every agent |
| Autoregistration | `POST /api/v1/register`, signed with that key |
| Active checks | The agent pushes batches. Nothing on the host accepts connections |
| Enable or disable a host | `enabled` or `disabled`. A disabled host is rejected with HTTP 403 |
| Availability | Heartbeat every 15 seconds. The console marks a host online when `last_seen` is within 45 seconds |
| `TLSCAFile` | `DLOG_CA_FILE`, a PEM bundle the agent uses to verify the server |

The private key on an agent can register any hostname and can take over an enabled hostname. Protect `agent.key`. Put TLS on the server (`-tls-cert` and `-tls-key`, or a reverse proxy) so a captured registration cannot be replayed after it expires. A registration timestamp is accepted for five minutes.

The first time the server starts without a certificate, it writes `agent.crt` and `agent.key` into the data directory and logs a warning. To replace the certificate, put a new ECDSA P-256 pair in those paths and restart the server, then install the same files on the agents.

## Quick start

Requires Go 1.26 or newer.

```bash
make build
make test

DLOG_ADMIN_TOKEN=local-dev-admin-token \
./bin/dlog-server

# second shell, on a machine with a Docker socket
DLOG_SERVER_URL=http://127.0.0.1:8080 \
DLOG_AGENT_CERT=data/server/agent.crt \
DLOG_AGENT_KEY=data/server/agent.key \
DLOG_HOSTNAME="$(hostname)" \
./bin/dlog-agent
```

Open http://127.0.0.1:8080 and sign in with `local-dev-admin-token`. That value is for a local lab. The server logs a warning when the admin token is short, and when it has just generated the agent certificate.

If you do not set an admin token, the first start writes one to `data/server/admin.token` and prints it once. The agent certificate is written beside it as `agent.crt` and `agent.key`.

### Containers

```bash
docker compose up --build
```

The lab agent mounts the host Docker socket, so it ships logs from every container on that daemon, not only the compose services. It registers as `dlog-lab` with the certificate the server generates into the `dlog-cred` volume. A `logger` service writes a stdout line and a stderr line every three seconds. Sign in at http://127.0.0.1:8080 with `local-dev-admin-token`.

To run only the agent on another host, copy `agent.crt` and `agent.key` from the server and mount those same files:

```bash
DLOG_SERVER_URL=https://logs.example.com \
DLOG_HOSTNAME=web-01 \
DLOG_AGENT_CERT_FILE=./agent.crt \
DLOG_AGENT_KEY_FILE=./agent.key \
docker compose -f deploy/agent-compose.yaml up -d --build
```

For a private CA, mount the PEM file and set `DLOG_CA_FILE` to that path. Podman works too: point `DLOG_DOCKER_HOST` at the Podman API socket (`unix:///run/podman/podman.sock`, or `$XDG_RUNTIME_DIR/podman/podman.sock` for rootless).

## What the agent collects

On startup, and every 30 seconds, the agent lists running containers and logs the names it will follow and the names it will skip. It also watches Docker events so a new container is followed without waiting for the next list. For each container it calls the Docker log API with `follow`, `stdout`, `stderr`, and `timestamps`, starting from the saved cursor or from `DLOG_SINCE` (default 10 minutes) the first time. The API version is the one the daemon advertises. Docker 29 rejects older clients, including a hardcoded `/v1.41`.

It skips:

- its own container, when the agent itself is running in a container (override with `DLOG_INCLUDE_SELF=true`)
- names in `DLOG_EXCLUDE` (comma-separated)
- containers labeled `dlog.exclude=true`

The Docker socket is root-equivalent. The agent container is root for that reason. On the host, the socket is usually mode `0660` and group `docker`; add your user to that group instead of running the agent with sudo. Treat `agent.key` and the state volume as secrets. The key file and the state file are mode `0600`.

Lines are buffered in memory (default 10,000). If the server is down, the agent retries with backoff and does not drop the buffer until it is full. Delivery is at-least-once: a crash after the server accepts a batch and before the cursor is saved can send those lines again.

## Operator console

The console is served by `dlog-server`.

- Hosts on the left, container filters across the top, log lines in the main pane.
- Search the host list by hostname or group name. Click a group header to collapse it. A search shows matches even inside a collapsed group. The line search matches text literally. `%` and `_` are not wildcards.
- Follow tails new lines. Older loads the previous page.
- Hosts opens a panel to create, rename, and delete groups, move a host into one group, and disable, enable, or delete hosts. Hosts appear on their own after an agent registers.

Deleting a host deletes its stored logs. The agent will register that hostname again if it still has the certificate and the host was not left disabled. Disable a host to refuse it.

## HTTP API

`POST /api/v1/register` is signed with the agent private key. The signature is the `X-Dlog-Signature` header, base64url of the ECDSA signature over the SHA-256 of the raw JSON body. The body is `{"hostname","agent_version","timestamp"}`. Ingest and heartbeat then send `Authorization: Bearer <host token>`, the token the agent saved at registration. Operator routes take the admin token the same way, or the `dlog_session` cookie from `POST /api/v1/login`.

| Method | Path | Who |
| --- | --- | --- |
| GET | `/api/v1/health` | public |
| POST | `/api/v1/register` | agent certificate |
| POST | `/api/v1/ingest` | host token |
| POST | `/api/v1/heartbeat` | host token |
| POST | `/api/v1/login` | admin token, sets the session cookie |
| GET | `/api/v1/hosts` | operator. Body is `{"hosts","groups"}`. Each host has `group_id` (`""` when ungrouped). |
| GET | `/api/v1/containers?host_id=` | operator |
| GET | `/api/v1/logs?host_id=&container_id=&stream=&q=&limit=&after_id=&before_id=` | operator |
| POST | `/api/v1/admin/groups` | operator, body `{"name"}`, 201 |
| PATCH | `/api/v1/admin/groups/{id}` | operator, body `{"name"}` |
| DELETE | `/api/v1/admin/groups/{id}` | operator. Hosts in the group stay, ungrouped. |
| POST | `/api/v1/admin/hosts/{id}/disable` | operator |
| POST | `/api/v1/admin/hosts/{id}/enable` | operator |
| PATCH | `/api/v1/admin/hosts/{id}` | operator, body `{"hostname":"new-name"}` |
| PATCH | `/api/v1/admin/hosts/{id}/group` | operator, body `{"group_id":"grp_…"}`. `""` clears the group. |
| DELETE | `/api/v1/admin/hosts/{id}` | operator |

A group name is 1–64 characters, starts with a letter or digit, and then uses letters, digits, spaces, `.`, `_`, or `-`. Names are unique ignoring case.

A new hostname returns 201. Registering an enabled hostname again returns 200 and a new host token; the previous token stops working. A disabled hostname returns 403. A bad signature returns 401. An ingest batch holds at most 1000 records. The agent sends at most 500. Invalid records in a valid batch are counted in `rejected` and dropped, so one bad line does not block the agent. Unknown host tokens get 401. Disabled hosts get 403.

## Configuration

Server flags override the same `DLOG_` variables.

| Flag | Variable | Default |
| --- | --- | --- |
| `-addr` | `DLOG_ADDR` | `:8080` |
| `-data-dir` | `DLOG_DATA_DIR` | `data/server` |
| `-admin-token` | `DLOG_ADMIN_TOKEN` | generated into `admin.token` |
| `-agent-cert` | `DLOG_AGENT_CERT` | `agent.crt` in the data directory |
| `-agent-key` | `DLOG_AGENT_KEY` | `agent.key` in the data directory, written only when the certificate is generated |
| `-retention` | `DLOG_RETENTION` | `168h` (`7d` is accepted; `0` keeps logs) |
| `-tls-cert` / `-tls-key` | `DLOG_TLS_CERT` / `DLOG_TLS_KEY` | empty |
| `-trust-proxy` | `DLOG_TRUST_PROXY` | false |
| `-secure-cookies` | `DLOG_SECURE_COOKIES` | false |
| `-log-level` | `DLOG_LOG_LEVEL` | `info` |

`dlog-server healthcheck` probes `http://127.0.0.1` on the port from `DLOG_ADDR`. Set `DLOG_HEALTH_URL` to override it.

Agent:

| Flag | Variable | Default |
| --- | --- | --- |
| `-server` | `DLOG_SERVER_URL` | required |
| `-agent-cert` | `DLOG_AGENT_CERT` | required, same certificate on every agent |
| `-agent-key` | `DLOG_AGENT_KEY` | required, private key for that certificate |
| `-hostname` | `DLOG_HOSTNAME` | hostname file, then the machine hostname |
| `-hostname-file` | `DLOG_HOSTNAME_FILE` | empty |
| `-state-dir` | `DLOG_STATE_DIR` | `data/agent` |
| `-docker-host` | `DLOG_DOCKER_HOST` | `/var/run/docker.sock` |
| `-ca-file` | `DLOG_CA_FILE` | system trust store |
| `-since` | `DLOG_SINCE` | `10m` |
| `-batch` | `DLOG_BATCH` | `200` |
| `-flush` | `DLOG_FLUSH` | `1s` |
| `-heartbeat` | `DLOG_HEARTBEAT` | `15s` |
| `-buffer` | `DLOG_BUFFER` | `10000` |
| `-include-self` | `DLOG_INCLUDE_SELF` | false |
| `-exclude` | `DLOG_EXCLUDE` | empty |

Hostnames must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`. Set `DLOG_HOSTNAME` to the node name. The container's own hostname is a poor identity if the container is recreated.

## Storage

Logs sit in one SQLite database, `dlog.db`, in the server data directory, with WAL mode when the filesystem allows it. That is enough for a homelab or a small fleet. It is not a multi-node log cluster. Put the data directory on a local disk.

A janitor runs once a minute and deletes lines older than the retention window. Operators query through indexes on host and container. A leading-wildcard `LIKE` search is fine at this size and will get slower as the table grows.

## Layout

```
cmd/server          dlog-server
cmd/agent           dlog-agent
internal/server     store, API, operator console
internal/agent      Docker log follower and shipper
internal/agentcred  shared agent certificate
internal/protocol   JSON contract
deploy/             images and the agent-only compose file
compose.yaml        local server, agent, and a logger
```
