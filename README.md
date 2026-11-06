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
| Availability | Heartbeat every 15 seconds. The console marks a host online when it was seen within the offline window, 45 seconds unless Settings changes it |
| `TLSCAFile` | `DLOG_CA_FILE`, a PEM bundle the agent uses to verify the server |

The private key on an agent can register any hostname and can take over an enabled hostname. Protect `agent.key`. Put TLS on the server (`-tls-cert` and `-tls-key`, or a reverse proxy) so a captured registration cannot be replayed after it expires. A registration timestamp is accepted for five minutes.

The first time the server starts without a certificate, it writes `agent.crt` and `agent.key` into the data directory and logs a warning. Settings can generate a new pair or replace the one the server trusts, and that takes effect without a restart. You can also put a new ECDSA P-256 pair in those paths and restart the server. Either way, install the same files on the agents. Hosts that already have a token keep sending logs.

## Quick start

Requires Go 1.26 or newer.

```bash
make build
make test

DLOG_ADMIN_USER=admin \
DLOG_ADMIN_PASSWORD=local-dev-admin-password \
./bin/dlog-server

# second shell, on a machine with a Docker socket
DLOG_SERVER_URL=http://127.0.0.1:8080 \
DLOG_AGENT_CERT=data/server/agent.crt \
DLOG_AGENT_KEY=data/server/agent.key \
DLOG_HOSTNAME="$(hostname)" \
./bin/dlog-agent
```

Open http://127.0.0.1:8080 and sign in as `admin` with `local-dev-admin-password`. That password is for a local lab. The server logs a warning when it is short, and when it has just generated the agent certificate.

If you do not set a password, the first start creates the admin account, writes the password to `data/server/admin.password`, and prints it once. Later starts leave existing accounts alone. The agent certificate is written beside it as `agent.crt` and `agent.key`.

### Containers

```bash
docker compose up --build
```

The lab agent mounts the host Docker socket, so it ships logs from every container on that daemon, not only the compose services. It registers as `dlog-lab` with the certificate the server generates into the `dlog-cred` volume. A `logger` service writes a stdout line and a stderr line every three seconds. Sign in at http://127.0.0.1:8080 as `admin` with `local-dev-admin-password`.

To run only the agent on another host, copy `agent.crt` and `agent.key` from the server and mount those same files:

```bash
DLOG_SERVER_URL=https://logs.example.com \
DLOG_HOSTNAME=web-01 \
DLOG_AGENT_CERT_FILE=./agent.crt \
DLOG_AGENT_KEY_FILE=./agent.key \
docker compose -f deploy/agent-compose.yaml up -d --build
```

`DLOG_AGENT_CERT_FILE` and `DLOG_AGENT_KEY_FILE` are the certificate files on the host. Compose mounts them into the container. The agent process reads `DLOG_AGENT_CERT` and `DLOG_AGENT_KEY`, which that compose file sets to the mounted paths.

For a private CA, mount the PEM file and set `DLOG_CA_FILE` to that path. Podman works too: point `DLOG_DOCKER_HOST` at the Podman API socket (`unix:///run/podman/podman.sock`, or `$XDG_RUNTIME_DIR/podman/podman.sock` for rootless).

### Published images

`.github/workflows/publish.yml` pushes both images to GitHub Container Registry. A push to `main` or `master` publishes `ghcr.io/<owner>/dlog-server:latest` and `ghcr.io/<owner>/dlog-agent:latest`. This repository's branch is `main`.

A version tag publishes those images under that version. `v0.1.0` and `0.1.0` both publish `:0.1.0`. A tag that starts with `v` also publishes that exact name, so `:v0.1.0` works. The server and the agent report the closest reachable version tag, without a leading `v`. A checkout with no version tag reports `0.1.0`.

`<owner>` is the GitHub user or organization, in lowercase. The first push creates the packages. A private repository keeps them private until you change the package visibility. Pulls of a private package need a GitHub token with `read:packages`.

## What the agent collects

On startup the agent lists running containers and logs the names it will follow and the names it will skip. It lists again every 30 seconds so a container missed by Docker events is still followed, and it logs that list only when the set changes. It also watches Docker events so a new container is followed without waiting for the next list. For each container it calls the Docker log API with `follow`, `stdout`, `stderr`, and `timestamps`, starting from the saved cursor or from `DLOG_SINCE` (default 10 minutes) the first time. The API version is the one the daemon advertises. Docker 29 rejects older clients, including a hardcoded `/v1.41`.

It skips:

- its own container, when the agent itself is running in a container (override with `DLOG_INCLUDE_SELF=true`)
- names in `DLOG_EXCLUDE` (comma-separated)
- containers labeled `dlog.exclude=true`

The Docker socket is root-equivalent. The agent container is root for that reason. On the host, the socket is usually mode `0660` and group `docker`; add your user to that group instead of running the agent with sudo. Treat `agent.key` and the state volume as secrets. The key file and the state file are mode `0600`.

Lines are buffered in memory (default 10,000). If the server is down, the agent retries with backoff and does not drop the buffer until it is full. Delivery is at-least-once: a crash after the server accepts a batch and before the cursor is saved can send those lines again. The server stores one row for a host, container, stream, millisecond, and message, so that retry does not create a second row. Two identical lines in the same millisecond are stored once.

## Operator console

The console is served by `dlog-server`.

- Hosts on the left, container filters across the top, log lines in the main pane.
- Search the host list by hostname, display name, or group name. Click a group header to collapse it. A search shows matches even inside a collapsed group. The line search matches text literally. `%` and `_` are not wildcards.
- Follow tails new lines. Older loads the previous page. The time control limits the pane to the last 15 minutes, the last hour, or lines from a start time you pick. Selecting text in the log pane keeps the scroll where it is.
- A host row shows how many lines the agent has dropped when that count is not zero.
- Account opens a page where the signed-in user changes their own password. The current password is required. This browser stays signed in. Sign out other sessions ends every other session for that account.
- The host list on the left is for reading logs. Hosts appear there after an agent registers. A display name, when set, is the name on that row. The hostname stays underneath it.
- Admin opens a page for hosts. There you set a display name, move a host into one group, and disable, enable, or delete a host.
- General is where an admin changes the server name and the sign-in tagline, how long logs are stored, how long a session lasts, and how soon a quiet host looks offline.
- Registration turns automatic registration on or off, picks the group new hosts join, and approves a host that registered while that was off.
- Certificate keeps the shared agent certificate. Generating or replacing it stops new registration until each agent has the new certificate and key. Hosts that already have a token keep sending logs.
- Groups is where you create, rename, or delete groups. A user sees every host, or only the groups you tick. Hosts with no group stay visible to admins and to users who can see all hosts. Deleting a group removes it from those users, and the hosts in it leave their view.
- Accounts is where you create accounts and set each one to User or Admin. Setting an account's password signs that account out. Users can read logs. Admins can manage hosts, groups, accounts, and settings. An admin cannot delete the signed-in account or remove the last admin. A directory account is listed here too. Its password and role come from the directory, and you can still choose which groups it sees or delete the row.
- Directory turns on sign-in through LDAP. The first successful sign-in creates an account that can see every host. A member of the admin group DN is an admin, and each later sign-in updates that role without changing the groups that account can see. A local account is never replaced, and a wrong local password is not checked against the directory. The bind password is stored with the other settings and is not shown again. Leave it blank when you save to keep it.

Deleting a host deletes its stored logs. The agent will register that hostname again if it still has the certificate and the host was not left disabled. Disable a host to refuse it.

## HTTP API

`POST /api/v1/register` is signed with the agent private key. The signature is the `X-Dlog-Signature` header, base64url of the ECDSA signature over the SHA-256 of the raw JSON body. The body is `{"hostname","agent_version","timestamp"}`. Ingest and heartbeat then send `Authorization: Bearer <host token>`, the token the agent saved at registration. Operator routes use the `dlog_session` cookie from `POST /api/v1/login`. A user account can read. An admin account can also change hosts, groups, accounts, and settings.

| Method | Path | Who |
| --- | --- | --- |
| GET | `/api/v1/health` | public |
| GET | `/api/v1/brand` | public. Body is `{"name","tagline"}`. `name` is `Dlog` when the setting is blank. |
| POST | `/api/v1/register` | agent certificate |
| POST | `/api/v1/ingest` | host token |
| POST | `/api/v1/heartbeat` | host token |
| POST | `/api/v1/login` | username and password, sets the session cookie. A local account is checked first. An unknown name, or an account whose source is `directory`, is checked against the directory when that is turned on. The first success creates the account. A wrong password is 401. A directory that cannot be reached is 503 `directory unavailable`. An HTML form then redirects to `/login?error=directory`. |
| GET | `/api/v1/me` | signed-in account. Body is `{"id","username","role","all_hosts","group_ids","source","created_at"}`. `source` is `local` or `directory`. |
| PATCH | `/api/v1/me` | signed-in account, body `{"current_password","password","sign_out_others"}`. Changes that account's password. `sign_out_others` ends every other session. A wrong current password is 400. A directory account is 400 `directory account`. |
| GET | `/api/v1/hosts` | signed-in account. Body is `{"hosts","groups"}`. A user with `all_hosts` sees every host. A user limited to groups sees only those groups and the hosts in them. Each host has `group_id` (`""` when ungrouped), `display_name` (`""` when unset), and `dropped`. |
| GET | `/api/v1/containers?host_id=` | signed-in account. A host outside the account is 404. |
| GET | `/api/v1/logs?host_id=&container_id=&stream=&q=&limit=&after_id=&before_id=&since=&until=` | signed-in account. `since` and `until` are RFC3339. A host outside the account is 404. |
| GET | `/api/v1/admin/users` | admin. Body is `{"users":[...]}`. |
| POST | `/api/v1/admin/users` | admin, body `{"username","password","role"}`, 201. `role` is `user` or `admin`. |
| PATCH | `/api/v1/admin/users/{id}` | admin, body `{"role"}` and/or `{"password"}` and/or `{"all_hosts","group_ids"}`. A new password signs that account out. `all_hosts` true sees every host and clears `group_ids`. `all_hosts` false sees only those groups. An empty `group_ids` sees no hosts. Role and password are 400 `directory account` for a directory account. Group scope still applies. |
| DELETE | `/api/v1/admin/users/{id}` | admin. Refuses the signed-in account and the last admin. |
| POST | `/api/v1/admin/groups` | admin, body `{"name"}`, 201 |
| PATCH | `/api/v1/admin/groups/{id}` | admin, body `{"name"}` |
| DELETE | `/api/v1/admin/groups/{id}` | admin. Hosts in the group stay, ungrouped. |
| POST | `/api/v1/admin/hosts/{id}/disable` | admin |
| POST | `/api/v1/admin/hosts/{id}/enable` | admin |
| PATCH | `/api/v1/admin/hosts/{id}` | admin, body `{"display_name":"Web 01"}` and/or `{"hostname":"new-name"}`. `display_name` of `""` clears it. The hostname is the registration name. Changing it makes the next registration of the old name create another host. |
| PATCH | `/api/v1/admin/hosts/{id}/group` | admin, body `{"group_id":"grp_…"}`. `""` clears the group. |
| DELETE | `/api/v1/admin/hosts/{id}` | admin |
| GET | `/api/v1/admin/settings` | admin. Body is the saved settings plus `pending`, the hostnames waiting for approval. The bind password is omitted. `ldap_bind_password_set` is true when one is stored. |
| PATCH | `/api/v1/admin/settings` | admin. Any of `brand_name`, `brand_tagline`, `auto_register`, `default_group_id`, `retention`, `session`, `offline`, `ldap_enabled`, `ldap_url`, `ldap_starttls`, `ldap_bind_dn`, `ldap_bind_password`, `ldap_clear_bind_password`, `ldap_base_dn`, `ldap_user_filter`, `ldap_username_attr`, `ldap_admin_group`. `brand_name` of `""` uses Dlog. `default_group_id` of `""` leaves new hosts ungrouped. `retention` of `0` keeps logs. `retention`, `session`, and `offline` use the same duration text as the flags (`7d`, `12h`, `45s`). A session is from 1 hour to 30 days. The offline window is from 30 seconds to 15 minutes. A directory URL is `ldap://` or `ldaps://`, with no user name in it. StartTLS applies only to `ldap://`. The user filter must contain one `%s` and no other `%`. An empty bind password leaves the saved one. `ldap_clear_bind_password: true` removes it. Sending both a new password and clear is 400. When the directory is on, the URL and base DN are required. |
| POST | `/api/v1/admin/pending/{hostname}/approve` | admin. Creates the host in the default group. The agent gets a token on its next registration. |
| DELETE | `/api/v1/admin/pending/{hostname}` | admin. Drops a waiting registration. |
| GET | `/api/v1/admin/certificate` | admin. Fingerprint, subject, validity, and the file paths. The private key is not included. |
| GET | `/api/v1/admin/certificate.pem` | admin. Downloads the public certificate. |
| POST | `/api/v1/admin/certificate` | admin. `{"action":"generate"}` writes a new pair and returns the private key once. `{"action":"replace","cert_pem","key_pem"}` installs an ECDSA P-256 pair. |

A group name is 1–64 characters, starts with a letter or digit, and then uses letters, digits, spaces, `.`, `_`, or `-`. Names are unique ignoring case.

A username is 1–64 characters, starts with a letter or digit, and then uses letters, digits, `.`, `_`, or `-`. Usernames are unique ignoring case. A password is 8–72 characters. `role` is `user` or `admin`.

A new hostname returns 201. Registering an enabled hostname again returns 200 and a new host token; the previous token stops working. A disabled hostname returns 403. A bad signature returns 401. An ingest batch holds at most 1000 records. The agent sends at most 500. Invalid records in a valid batch are counted in `rejected` and dropped, so one bad line does not block the agent. Unknown host tokens get 401. Disabled hosts get 403.

## Configuration

Server flags override the same `DLOG_` variables.

| Flag | Variable | Default |
| --- | --- | --- |
| `-addr` | `DLOG_ADDR` | `:8080` |
| `-data-dir` | `DLOG_DATA_DIR` | `data/server` |
| `-admin-user` | `DLOG_ADMIN_USER` | `admin`, used only when creating the first account |
| `-admin-password` | `DLOG_ADMIN_PASSWORD` | generated into `admin.password` when the database has no accounts |
| `-agent-cert` | `DLOG_AGENT_CERT` | `agent.crt` in the data directory |
| `-agent-key` | `DLOG_AGENT_KEY` | `agent.key` in the data directory, written only when the certificate is generated |
| `-retention` | `DLOG_RETENTION` | `168h` (`7d` is accepted; `0` keeps logs). Used only until Settings saves a retention. The saved value is what later starts use. |
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

A janitor runs once a minute and deletes lines older than the retention window from Settings. Operators query through indexes on host and container. A leading-wildcard `LIKE` search is fine at this size and will get slower as the table grows.

## Layout

```
cmd/server          dlog-server
cmd/agent           dlog-agent
internal/server     store, API, operator console
internal/agent      Docker log follower and shipper
internal/agentcred  shared agent certificate
internal/protocol   JSON contract
deploy/             images and the agent-only compose file
.github/workflows   publish images to ghcr.io
compose.yaml        local server, agent, and a logger
```
