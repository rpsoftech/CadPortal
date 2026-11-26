# Uptime Kuma: the outside watcher

The portal's own alerts (Telegram messages from the API, the backup service and the watchdog) cannot be sent when
the whole server loses power or its network. Uptime Kuma runs on **another server** and notices that the portal went
quiet. It sends you a Telegram message through the same bot.

Use a small server (1 vCPU, 1 GB) at another provider or in another region. It only needs Docker and Caddy.

## 1. Start it

```bash
git clone https://github.com/rpsoftech/CadPortal.git && cd CadPortal/deploy/uptime-kuma
docker compose up -d
```

Make a DNS record, for example `status.goldenoakjewels.in`, that points to this server. Install Caddy, put the block
from `Caddyfile.example` in its Caddyfile with your name, and reload it. Open the address and **create the admin user
at once**: the first visitor becomes the admin.

## 2. Telegram (Settings, Notifications, Setup Notification)

- Type **Telegram**, bot token and chat id from the portal's `.env` (`TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID`; the
  same bot and chat are fine).
- Turn on **Default enabled** and **Apply on all existing monitors**, then Save. Press **Test**: a message arrives.

## 3. The monitors (Add New Monitor)

Make these for each portal server. Use the server's name in the monitor name (staging, production).

| Name | Type | Settings |
|---|---|---|
| Staging site | HTTP(s) | URL `https://<portal address>/healthz`, interval 60 s, retries 2. Turn on **Certificate Expiry Notification**. |
| Staging heartbeat | Push | Heartbeat interval **300** s, retries 1. Copy its **Push URL**. |
| Staging backup | Push | Heartbeat interval **93600** s (26 hours; for several backups a day use a little over the longest gap), retries 0. Copy its Push URL. |

The **site check** tells you the address is reachable from outside (DNS, Caddy, the certificate, the web container).
The two **push** monitors tell you the API and the backup service are alive: the portal calls them, so the server needs
no open port for this.

## 4. Connect the portal (on the portal server's `.env`)

```
HEARTBEAT_URL=<the Staging heartbeat Push URL>
BACKUP_HEARTBEAT_URL=<the Staging backup Push URL>
```

The Push URL looks like `https://status.example.com/api/push/<token>?status=up&msg=OK&ping=`. Then restart the
services so they read it:

```bash
docker compose -f docker-compose.prod.yml up -d api backup
```

The API pings only while it can reach its database; the backup service pings only after a *good* backup. Keep the
URLs secret: anyone with one can fake a heartbeat. The portal never writes them to its logs.

## 5. Test it

- Stop the API for 10 minutes (`docker compose -f docker-compose.prod.yml stop api`): "Staging heartbeat" goes down
  and a Telegram message arrives. Start it again: a recovery message follows.
- Production gets its own three monitors and its own two URLs in its own `.env`.

## Keeping it healthy

- Update by changing the image version in `docker-compose.yml`, then `docker compose pull && docker compose up -d`.
- Back up the `kuma-data` volume (settings and history only; losing it is a nuisance, not a disaster):
  `docker run --rm -v uptime-kuma_kuma-data:/d -v "$PWD":/b alpine tar czf /b/kuma-data.tgz -C /d .`
- Add the Kuma server's own address to a free outside check (UptimeRobot, Healthchecks.io) if you want to know when
  the watcher itself is down.
