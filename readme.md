# App Store Monitor

App Store Monitor is a tool for tracking updates to apps on the Apple App Store and Google Play Store. It notifies you via Discord webhook when a new version of a monitored app is released, helping you stay ahead of API changes and minimize downtime for your projects.

## Features

- **Supports Apple App Store & Google Play Store**
- **Customizable app list** via `config.json`
- **Discord webhook notifications** for new app versions
- **Persistent state tracking** to avoid duplicate alerts
- **Easy scheduling** with cron or other job schedulers

## Usage

1. **Configure your apps and webhook**  
   Edit `config.json` to specify the apps you want to monitor and your Discord webhook URL.

2. **Run the monitor**  
   Execute the tool manually or schedule it using cron for periodic checks (e.g., every hour).

3. **Receive notifications**
    When a new version is detected, you'll receive a message in your Discord channel.

## Recommendations

To avoid rate limiting, schedule checks no more frequently than once per hour.
Review and update your app list in config.json as needed.

## Contributions

I use this personally, so I will be fairly active in maintaining it. If you're keen for changes (cache bypasses, notifications on other platforms, move to app API's rather than web), feel free to make a PR.