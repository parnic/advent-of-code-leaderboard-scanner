# Advent of Code leaderboard tracker for Mattermost

## Summary

This is an application designed to be run on some sort of cron-like schedule or on its own with the `-d` argument where it will scan every 15 minutes. It will check the status of an [Advent of Code](https://adventofcode.com) leaderboard and report any diffs to the given Mattermost webhook.

## Configurables

The following configurables are supported in either argument ("-arg=val"), environment, or [.env](https://github.com/joho/godotenv) file form:

Argument | Env var | Description | Default
---- | ---- | ---- | ----
year | (none) | The event year to scan | "2023"
leaderboard | AOC_LEADERBOARD | The leaderboard ID to read (e.g. 1234567) | ""
session | AOC_SESSION | A valid session ID pulled from your web browser on a logged-in account | ""
webhookURL | AOC_WEBHOOK | The full URL for an incoming webhook to your Mattermost instance (e.g. https&#58;&#47;&#47;my.mattermost.server/hooks/abcd1234) | ""
d | (none) | Daemonize the application so it refreshes itself every 15 minutes | false
