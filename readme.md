# App Store Monitor

A small local monitor for Apple App Store and Google Play releases. The default
configuration watches the Android apps for Depop, eBay, and Vinted and sends a
Discord message when their Play Store metadata changes.

Google Play does not expose a universal version for every app and device. The
monitor therefore tracks both the published version and the Play Store's
updated date. This means eBay is still detected when Google omits its version.

## Local setup

Requirements: Go 1.24 or later and a Discord webhook.

```sh
git clone https://github.com/b-nnett/appstore-monitor.git
cd appstore-monitor
cp config.json config.local.json
chmod 600 config.local.json
```

Put the webhook in `config.local.json` under `webhook_url`, then build and run:

```sh
go build -o appstore-monitor .
APPSTORE_MONITOR_CONFIG=config.local.json ./appstore-monitor
```

The webhook can instead be supplied with `DISCORD_WEBHOOK_URL`. Paths for the
config, state, and log can be overridden with `APPSTORE_MONITOR_CONFIG`,
`APPSTORE_MONITOR_STATE`, and `APPSTORE_MONITOR_LOG`.

The first successful run seeds `state.json` without sending three noisy
first-seen alerts. Later changes generate notifications. If Discord delivery
fails, state is deliberately left unchanged so the next run retries it.

## Hourly local schedule

Edit the path below, then add this entry with `crontab -e`:

```cron
# App Store Monitor — hourly Android marketplace checks (Depop, eBay, Vinted)
0 * * * * cd /absolute/path/to/appstore-monitor && APPSTORE_MONITOR_CONFIG=config.local.json ./appstore-monitor >> cron.log 2>&1
```

This runs at minute zero of every hour. No hosted scheduler or CI service is
required.

## Configuration

Each Google Play entry accepts:

- `package_id`: the Android application ID.
- `country`: the two-letter Play Store market, defaulting to `gb`.
- `language`: the two-letter language, defaulting to `en`.

Apple App Store URLs remain supported for other personal configurations.
`notify_on_first_seen` can be enabled if initial alerts are desired.
