# wxexport

Exports 1:1 WeChat message and call history (flatpak WeChat 4.1.x on Linux) to JSON, CSV, and
SQLite, for your own analytics. See `docs/SPEC.md` for the full specification and `docs/FORMAT.md`
for findings from calibrating against a real account.

## Status

Phases 1–5 of the plan are implemented and tested, including end-to-end against a real WeChat
4.1.13.26 account: key capture, database decryption, contact listing, message decoding, and
export to all three formats. The TUI (Phase 6) is not built yet.

**Known issue** (docs/SPEC.md §B.1): `wxexport key capture` has been observed to make the real
WeChat process exit a few seconds after a successful capture. The captured key is still valid;
you may need to relaunch WeChat yourself afterward. `key capture` detects this and warns you.

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

## Testing

```sh
make test          # go vet + go test ./...
make race          # go test -race ./... (needs CGO_ENABLED=1)
```
