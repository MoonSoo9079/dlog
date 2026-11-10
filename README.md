# Dlog

Dlog is a small central log service for Docker hosts. One server stores logs. An agent container on each host follows the logs of every running container and pushes them to the server.

The agent does not listen on a port. It only dials the server you configure, the same way a Zabbix agent in active mode only talks to its server.

## Authorization

Every agent carries the same pre-shared key. That key is what authorizes automatic registration. The agent signs a registration request with its hostname. The server checks the signature against the key it trusts and creates the host if the name is new. An existing enabled host is accepted again, so a rebuilt agent with an empty state directory comes back without an operator copying a host token. A disabled host stays disabled.

You do not create hosts by hand, and you do not put a per-host deploy key on the agent. Set the same `DLOG_PSK_ID` and `DLOG_PSK_KEY` on every agent. `DLOG_HOSTNAME` is the host's name and must stay stable.

On first registration the server gives the agent a host token. The agent stores it in its state directory and sends it on later requests. That token is not an install step. If it stops working, the agent signs a new registration with the same key and replaces it.

| Zabbix | Dlog |
| --- | --- |
| Host registered on the server | Host row, created when the agent first registers |
| Shared credential | One pre-shared id and key, the same on every agent |
| Autoregistration | `POST /api/v1/register`, signed with that key |
| Active checks | The agent pushes batches. Nothing on the host accepts connections |
| Enable or disable a host | `enabled` or `disabled`. A disabled host is rejected with HTTP 403 |
| Availability | Heartbeat every 15 seconds. The console marks a host online when it was seen within the offline window, 45 seconds unless Settings changes it |
| Server certificate | A public certificate is checked with the system trust store. A self-signed certificate is accepted when its name matches the server URL |

The key on an agent can register any hostname and can take over an enabled hostname. Protect the shared key. Put TLS on the server (`-tls-cert` and `-tls-key`, or a reverse proxy) so a captured registration cannot be replayed after it expires. A registration timestamp is accepted for five minutes. A self-signed certificate does not prove the server's identity to a network attacker who can present another certificate for that name. Use a publicly trusted certificate when the path to the server is not trusted.

The first time the server starts with `DLOG_PSK_ID` and `DLOG_PSK_KEY` empty, it writes `psk.id` and `psk.key` into the data directory and logs a warning with the id, those paths, and the key fingerprint. The key itself is not logged. Copy the two values onto every agent as `DLOG_PSK_ID` and `DLOG_PSK_KEY`. Settings can generate a new key or replace the one the server trusts, and that takes effect without a restart. Setting both variables installs that id and key on every start and overwrites the files in the data directory, including a key changed in Settings. The id is 1–64 characters, starting with a letter or digit. The key is 32 bytes, unpadded base64url. Hosts that already have a token keep sending logs.

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
DLOG_PSK_ID="$(cat data/server/psk.id)" \
DLOG_PSK_KEY="$(cat data/server/psk.key)" \
DLOG_HOSTNAME="$(hostname)" \
./bin/dlog-agent
```

Open http://127.0.0.1:8080 and sign in as `admin` with `local-dev-admin-password`. That password is for a local lab. The server logs a warning when it is short, and when it has just generated the agent key.

If you do not set a password, the first start creates the admin account, writes the password to `data/server/admin.password`, and prints it once. Later starts leave existing accounts alone. When the agent key variables are empty, the server writes `psk.id` and `psk.key` beside that password. Read those files into `DLOG_PSK_ID` and `DLOG_PSK_KEY` on each agent.

### Containers

```bash
docker compose up --build
```

The lab agent mounts the host Docker socket, so it ships logs from every container on that daemon, not only the compose services. It registers as `dlog-lab` with the same id and key the server is given in `compose.yaml`. A `logger` service writes a stdout line and a stderr line every three seconds. Sign in at http://127.0.0.1:8080 as `admin` with `local-dev-admin-password`.

To run only the agent on another host, set the same id and key the server is using:

```bash
DLOG_SERVER_URL=https://logs.example.com \
DLOG_HOSTNAME=web-01 \
DLOG_PSK_ID="$(cat psk.id)" \
DLOG_PSK_KEY="$(cat psk.key)" \
docker compose -f deploy/agent-compose.yaml up -d --build
```

`DLOG_PSK_ID` and `DLOG_PSK_KEY` are the id and the key. The agent compose file requires both. `cat` is only a way to read the values the server wrote into `psk.id` and `psk.key`.

To run only the server:

```bash
docker compose -f deploy/server-compose.yaml up -d --build
```

That file publishes host port 8080 to container port 8080 and stores the database in a volume. Leave `DLOG_ADMIN_PASSWORD` unset to generate the first admin password into that volume. Leave the agent key variables unset to generate `psk.id` and `psk.key` there, then set the same values on each agent. Setting both variables uses that id and key and writes them into the volume on every start. `DLOG_PUBLISH_PORT` changes the host port. It is not a server setting. Leave `DLOG_ADDR` at `:8080` unless you also change the port mapping.

`deploy/agent-compose.yaml` and `deploy/server-compose.yaml` set every `DLOG_` variable the agent and the server read. An unset variable uses the default written in the file. Paths that follow a volume stay on that mount: the agent state directory and the server data directory. Configuration says what each variable does and why it is set.

The agent trusts a public server certificate through the system trust store. It also accepts a self-signed certificate when the name on the certificate matches the server URL and the certificate is current. No CA file is required. A certificate signed by a private CA is accepted only when that CA is already in the system trust store. Podman works too: mount its API socket on `/var/run/docker.sock`, or point `DLOG_DOCKER_HOST` at the socket path inside the container (`unix:///run/podman/podman.sock`, or `$XDG_RUNTIME_DIR/podman/podman.sock` for rootless) and mount that socket there.

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

The Docker socket is root-equivalent. The agent container is root for that reason. On the host, the socket is usually mode `0660` and group `docker`; add your user to that group instead of running the agent with sudo. Treat the shared key and the state volume as secrets. On the server, `psk.key` is mode `0600`. The agent state file is mode `0600`.

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
- Shared key keeps the pre-shared id and key. Generating or replacing it stops new registration until each agent has the new id and key. Hosts that already have a token keep sending logs. The key is shown once, when it is generated.
- Groups is where you create, rename, or delete groups. A user sees every host, or only the groups you tick. Hosts with no group stay visible to admins and to users who can see all hosts. Deleting a group removes it from those users, and the hosts in it leave their view.
- Accounts is where you create accounts and set each one to User or Admin. Setting an account's password signs that account out. Users can read logs. Admins can manage hosts, groups, accounts, and settings. An admin cannot delete the signed-in account or remove the last admin. A directory account is listed here too. Its password and role come from the directory, and you can still choose which groups it sees or delete the row.
- Directory turns on sign-in through LDAP. The first successful sign-in creates an account that can see every host. A member of the admin group DN is an admin, and each later sign-in updates that role without changing the groups that account can see. A local account is never replaced, and a wrong local password is not checked against the directory. The bind password is stored with the other settings and is not shown again. Leave it blank when you save to keep it. Test connection checks the form and does not save it. A blank bind password uses the saved one.

Deleting a host deletes its stored logs. The agent will register that hostname again if it still has the key and the host was not left disabled. Disable a host to refuse it.

## HTTP API

`POST /api/v1/register` is signed with the shared key. `X-Dlog-PSK-Id` is the id. `X-Dlog-Signature` is unpadded base64url of HMAC-SHA256 over the raw JSON body. The body is `{"hostname","agent_version","timestamp"}`. Ingest and heartbeat then send `Authorization: Bearer <host token>`, the token the agent saved at registration. Operator routes use the `dlog_session` cookie from `POST /api/v1/login`. A user account can read. An admin account can also change hosts, groups, accounts, and settings.

| Method | Path | Who |
| --- | --- | --- |
| GET | `/api/v1/health` | public |
| GET | `/api/v1/brand` | public. Body is `{"name","tagline"}`. `name` is `Dlog` when the setting is blank. |
| POST | `/api/v1/register` | shared key |
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
| POST | `/api/v1/admin/directory/test` | admin. The same directory fields as the settings PATCH. Nothing is saved. A blank bind password uses the saved one. The URL and base DN are required even when the directory is off. `200` with `{"ok":true}` means the server dialed, bound when a bind DN is set, and read the base DN. An admin group DN is read the same way. `400` is `bind failed`, `base DN was not found`, `admin group was not found`, or a settings validation error. `503` is `could not connect` or `directory unavailable`. |
| POST | `/api/v1/admin/pending/{hostname}/approve` | admin. Creates the host in the default group. The agent gets a token on its next registration. |
| DELETE | `/api/v1/admin/pending/{hostname}` | admin. Drops a waiting registration. |
| GET | `/api/v1/admin/psk` | admin. Id, key fingerprint, and the data-directory files the server wrote (`psk.id`, `psk.key`). The key is not included. |
| POST | `/api/v1/admin/psk` | admin. `{"action":"generate"}` writes a new id and key and returns the key once. `{"action":"replace","psk_id","psk_key"}` installs an id and a 32-byte key. The key is unpadded base64url. |

A group name is 1–64 characters, starts with a letter or digit, and then uses letters, digits, spaces, `.`, `_`, or `-`. Names are unique ignoring case.

A username is 1–64 characters, starts with a letter or digit, and then uses letters, digits, `.`, `_`, or `-`. Usernames are unique ignoring case. A password is 8–72 characters. `role` is `user` or `admin`.

A new hostname returns 201. Registering an enabled hostname again returns 200 and a new host token; the previous token stops working. A disabled hostname returns 403. A bad signature returns 401. An ingest batch holds at most 1000 records. The agent sends at most 500. Invalid records in a valid batch are counted in `rejected` and dropped, so one bad line does not block the agent. Unknown host tokens get 401. Disabled hosts get 403.

## Configuration

Each setting is an environment variable. The matching flag overrides it. Durations take a Go duration or a day suffix, such as `7d`, `168h`, `10m`, or `15s`. `0` means zero. A boolean accepts `1`, `true`, `yes`, or `on`, and `0`, `false`, `no`, or `off`. An empty value keeps the default.

The server image sets `DLOG_DATA_DIR=/var/lib/dlog` and `DLOG_ADDR=:8080`. The agent image sets `DLOG_STATE_DIR=/var/lib/dlog-agent` and `DLOG_DOCKER_HOST=/var/run/docker.sock`. The compose files set those again so the path the process uses is the path of the volume. The other compose values are the defaults below, so leaving the host variable unset does not change behavior. Pass `DLOG_PSK_ID` and `DLOG_PSK_KEY` in when you start the agent.

### Server

| Variable | Flag | Default | What it does |
| --- | --- | --- | --- |
| `DLOG_ADDR` | `-addr` | `:8080` | Address the server listens on. The image and `deploy/server-compose.yaml` keep `:8080` because that is the container port the file publishes. Change it only together with that port mapping. |
| `DLOG_DATA_DIR` | `-data-dir` | `data/server` | Directory for the database, the generated admin password, and the generated agent key. The image and the server compose file use `/var/lib/dlog` because that directory is the persistent volume. |
| `DLOG_ADMIN_USER` | `-admin-user` | `admin` | Username of the first account. It is read only when the database has no accounts. A later start does not rename the account. |
| `DLOG_ADMIN_PASSWORD` | `-admin-password` | empty | Password of that first account. Empty makes the server generate one, print it once, and store it in `admin.password` inside the data directory. The server compose file leaves it empty so the first start still does that. Set it when the first password should be one you chose. |
| `DLOG_PSK_ID` | `-psk-id` | empty | Shared agent PSK id. Empty, together with an empty `DLOG_PSK_KEY`, keeps or generates `psk.id` in the data directory. Set both to use an id and key you already have. A set pair is written into the data directory on every start, so a key changed in Settings is replaced on the next restart. The server compose file leaves both empty so the generated key stays in the data volume. The id is logged. The key is not. |
| `DLOG_PSK_KEY` | `-psk-key` | empty | Shared agent PSK key, 32 bytes as unpadded base64url. Set it together with `DLOG_PSK_ID`. One of the two set, and the other empty, stops the server. The key is not logged. |
| `DLOG_RETENTION` | `-retention` | `168h` | How long logs are kept. This seeds Settings only when retention has never been saved. `0` keeps logs. After that, the console value wins, so changing this variable on a later start does nothing until you change retention in Settings. |
| `DLOG_TLS_CERT` | `-tls-cert` | empty | PEM certificate for HTTPS. Empty serves plain HTTP. Set it together with `DLOG_TLS_KEY`. The health check then uses `https`. |
| `DLOG_TLS_KEY` | `-tls-key` | empty | Private key for that certificate. The server refuses to start when only one of the two is set. |
| `DLOG_TRUST_PROXY` | `-trust-proxy` | false | Trust `X-Forwarded-For` for the client address and `X-Forwarded-Proto` when deciding that the session cookie is `Secure`. Set it when a reverse proxy is the only client the server sees. Leave it false for direct connections, so a caller cannot pick their own address. |
| `DLOG_SECURE_COOKIES` | `-secure-cookies` | false | Always mark the session cookie `Secure`, so a browser sends it only over HTTPS. Set it when operators reach the server through HTTPS. With `DLOG_TRUST_PROXY`, a proxy that sends `X-Forwarded-Proto: https` already marks the cookie `Secure`. |
| `DLOG_LOG_LEVEL` | `-log-level` | `info` | How much the server writes to stderr. `debug`, `info`, `warn`, or `error`. |
| `DLOG_HEALTH_URL` | | empty | URL for `dlog-server healthcheck`. Empty probes `http://127.0.0.1` on the port in `DLOG_ADDR`, or `https` when `DLOG_TLS_CERT` is set. Set it when that derived URL is the wrong probe. |

### Agent

| Variable | Flag | Default | What it does |
| --- | --- | --- | --- |
| `DLOG_SERVER_URL` | `-server` | required | Base URL of the server. The agent only dials out. The agent compose file requires it so the container cannot start without a server to call. `https` uses the system trust store. A self-signed certificate is also accepted when its name matches this URL and it is within its dates. The name must be a DNS SAN, or an IP SAN when the URL host is an IP. |
| `DLOG_PSK_ID` | `-psk-id` | required | Shared PSK id. The same value on every agent. The agent compose file requires it. |
| `DLOG_PSK_KEY` | `-psk-key` | required | Shared PSK key, 32 bytes as unpadded base64url. The agent signs its registration with it. The agent compose file requires it. |
| `DLOG_HOSTNAME` | `-hostname` | the machine hostname | Name recorded for this host. It must stay stable and match `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`. The compose file requires it because a new container gets a new hostname and would show up as a different host. |
| `DLOG_HOSTNAME_FILE` | `-hostname-file` | empty | File to read the hostname from, used only when `DLOG_HOSTNAME` is empty. Set it when the name should come from a mounted file, such as the host's `/etc/hostname`. |
| `DLOG_STATE_DIR` | `-state-dir` | `data/agent` | Directory for the host token and the log cursors. The image and the compose file use `/var/lib/dlog-agent` because that is the state volume. Losing it makes the agent register again and re-read Docker from `DLOG_SINCE`. |
| `DLOG_DOCKER_HOST` | `-docker-host` | `/var/run/docker.sock` | Docker API socket, or a `unix://` URL. This is the daemon whose containers are shipped. The compose file keeps the default and mounts the host socket on that path, so the agent sees the host's containers rather than an empty daemon of its own. For Podman, mount that socket on the same path, or set this to the socket path inside the container. |
| `DLOG_SINCE` | `-since` | `10m` | How far back to read a container the first time. Later reads continue from the saved cursor. It keeps a new agent from importing the daemon's whole history. An invalid value stops the agent. |
| `DLOG_BATCH` | `-batch` | `200` | Lines sent in one request. The server accepts at most 1000, and the agent will not send more than 500. A value that is not a positive number stays 200. |
| `DLOG_FLUSH` | `-flush` | `1s` | Longest a line waits before a short batch is sent. A smaller value shows lines sooner. A larger value groups more lines into one request. An invalid value stops the agent. |
| `DLOG_HEARTBEAT` | `-heartbeat` | `15s` | How often the agent reports that the host is up, including how many buffered lines it has dropped. That report refreshes the host's last-seen time. The console marks the host offline after the offline window saved in Settings. An invalid value stops the agent. |
| `DLOG_BUFFER` | `-buffer` | `10000` | Unsent lines kept in memory while the server cannot be reached. The buffer is not written to disk. When it fills, the oldest lines are dropped and the drop count is shown on the host. A value that is not a positive number stays 10000. |
| `DLOG_INCLUDE_SELF` | `-include-self` | false | Also ship the agent container's own logs. It is false so the agent's own output is not mixed into the logs it collects. The compose file sets `false` for that reason. |
| `DLOG_EXCLUDE` | `-exclude` | empty | Comma-separated container names to skip, besides containers labeled `dlog.exclude=true`. The agent compose service has that label so it is skipped even when its name changes. |
| `DLOG_LOG_LEVEL` | `-log-level` | `info` | How much the agent writes to stderr. `debug`, `info`, `warn`, or `error`. |

### Compose only

Compose reads these names. The server and the agent do not.

| Variable | What it does |
| --- | --- |
| `DLOG_PUBLISH_PORT` | Host port forwarded to container port 8080 in `deploy/server-compose.yaml`. The default is `8080`. It does not change `DLOG_ADDR`. |

## Storage

Logs sit in one SQLite database, `dlog.db`, in the server data directory, with WAL mode when the filesystem allows it. That is enough for a homelab or a small fleet. It is not a multi-node log cluster. Put the data directory on a local disk.

A janitor runs once a minute and deletes lines older than the retention window from Settings. Operators query through indexes on host and container. A leading-wildcard `LIKE` search is fine at this size and will get slower as the table grows.

## Layout

```
cmd/server          dlog-server
cmd/agent           dlog-agent
internal/server     store, API, operator console
internal/agent      Docker log follower and shipper
internal/agentcred  shared agent pre-shared key
internal/protocol   JSON contract
deploy/             images and compose files for the server and the agent
.github/workflows   publish images to ghcr.io
compose.yaml        local server, agent, and a logger
```
