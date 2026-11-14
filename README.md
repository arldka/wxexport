<div align="center">

# 💬 wxexport

**Export your WeChat 1:1 chat and call history to JSON, CSV and SQLite.**

For your own analytics · flatpak WeChat 4.1.x on Linux · written in Go

![Status](https://img.shields.io/badge/status-proof%20of%20concept-orange)
![Platform](https://img.shields.io/badge/platform-Linux%20x86--64-informational)
![Go](https://img.shields.io/badge/Go-1.x-00ADD8?logo=go&logoColor=white)
![Formats](https://img.shields.io/badge/export-JSON%20%7C%20CSV%20%7C%20SQLite-brightgreen)

**English** · [简体中文](README.zh-CN.md)

[Usage](#usage) · [Status](#status) · [Privacy](#privacy-and-safety-notes) · [Spec](docs/SPEC.md) · [Format notes](docs/FORMAT.md)

</div>

---

> [!WARNING]
> **Proof of concept / work in progress. Use at your own risk.**
> This is an experimental personal project, not a finished or supported product. It is provided
> "as is", without warranty of any kind. It may have bugs, produce incomplete or incorrect
> exports, break with any WeChat update, or (see the known issue below) cause WeChat to exit.
> Back up your data first. You are solely responsible for how you use it.

> [!CAUTION]
> **Use only on your own data.**
> wxexport is meant for exporting **your own** WeChat account's history, from **your own**
> machine, with **your own** logged-in session. Do not use it on anyone else's account, device or
> messages, or anywhere you lack explicit permission. Doing so may be illegal (unauthorized
> access, privacy and data-protection laws) and may violate WeChat's terms of service. Your
> exports contain private conversations, including other people's messages: keep them secure and
> do not publish or share them.

Exports 1:1 WeChat message and call history to JSON, CSV, and SQLite. See `docs/SPEC.md` for the
full specification and `docs/FORMAT.md` for findings from calibrating against a real account.

## Status

Early, experimental, and tested against a single setup (WeChat 4.1.13.26, flatpak, x86-64 Linux).
Implemented: key capture, database decryption, contact listing, message decoding, export to all
three formats, and a basic TUI. Expect rough edges, and expect it to stop working on other WeChat
versions.

**Known issue** (docs/SPEC.md §B.1): `wxexport key capture` has been observed to make the real
WeChat process exit a few seconds after a successful capture. The captured key is still valid;
you may need to relaunch WeChat yourself afterward. `key capture` detects this and warns you.

## What `key capture` does, and why it is sensitive

To read your databases, `key capture` relaunches WeChat as a child process and uses `ptrace` to
read the database passphrase once, at login. That is a debugger-style technique and is only
appropriate for **your own account on your own machine**. The passphrase is stored in
`~/.config/wxexport/keys.json` (mode 0600). Anyone who obtains that file and your database files
can read your chats, so never commit, upload or share it (it is in `.gitignore`).

## Privacy and safety notes

- Decrypted copies of your databases are written to a private temporary directory
  (`$XDG_RUNTIME_DIR`) while the tool runs, and removed on exit. If the tool is killed, remove
  leftover `wxexport-*` directories yourself.
- Exports (JSON/CSV/SQLite) are written with owner-only permissions (0600) and contain plaintext
  messages. Store them accordingly.
- CSV cells that begin with `=`, `+`, `-` or `@` are prefixed with `'` so spreadsheets do not
  execute message content as formulas.
- Do not paste real chat content, keys or database files into public issues or pull requests.

## Usage

```sh
go build -o bin/wxexport ./cmd/wxexport

./bin/wxexport doctor                       # check the environment
./bin/wxexport key capture                   # one-time: capture the database key
./bin/wxexport key check                     # re-validate the cached key
./bin/wxexport contacts                      # list 1:1 contacts
./bin/wxexport contacts --search Alice
./bin/wxexport debug types --contact Alice   # (type,sub) histogram, for calibration

./bin/wxexport export --contact Alice \
    --format json,csv,sqlite --out ./export \
    --from 2024-01-01 --to 2024-12-31 \
    --tz Europe/Paris
```

Global flags (`--data-dir`, `--account`, `--config`) and `export`'s own flags (`--kinds`, `--raw`,
`--excel`, `--force`) are documented in `wxexport -h` / `wxexport export -h`.

## Example analytics (SQLite export)

```sql
-- messages per day
SELECT date, count(*) FROM messages GROUP BY date ORDER BY date DESC;

-- messages per day, sent vs received
SELECT date, direction, count(*) FROM messages GROUP BY date, direction ORDER BY date DESC;

-- call minutes per ISO week
SELECT iso_week, sum(call_duration_s) / 60.0 AS minutes FROM calls GROUP BY iso_week ORDER BY iso_week;

-- average call length (completed/interrupted calls only)
SELECT avg(call_duration_s) FROM calls WHERE call_outcome IN ('completed', 'interrupted');

-- pictures exchanged per month
SELECT substr(date, 1, 7) AS month, count(*) FROM messages WHERE kind = 'image' GROUP BY month;

-- busiest hour of day
SELECT hour, count(*) FROM messages GROUP BY hour ORDER BY count(*) DESC LIMIT 5;
```

## Disclaimer

wxexport is an independent project and is not affiliated with, endorsed by, or supported by
Tencent or WeChat. "WeChat" is a trademark of its owner. The authors accept no liability for data
loss, account actions, or any other consequence of using this software; see the proof-of-concept
notice at the top. Use it only on your own data.

## Testing

```sh
make test          # go vet + go test ./...
make race          # go test -race ./... (needs CGO_ENABLED=1)
```
