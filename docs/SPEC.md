# wxexport SPEC

> Proof of concept, use at your own risk. Intended only for exporting your own WeChat account's data from your own machine; see README.md.


## §G Goals
- G1: Discover the WeChat flatpak install, the account and the DBs without configuration.
- G2: Obtain and cache the DB passphrase for WeChat 4.1.x, with no sudo in the normal path.
- G3: Decrypt a consistent snapshot of the DBs, including WAL, into private plaintext SQLite copies using pure Go (no cgo).
- G4: Browse contacts and full 1:1 conversations in a TUI.
- G5: Export one contact's messages and calls to JSON, CSV and SQLite, with filters.
- G6: Decode every message into a normalized `Kind`, with structured call and media metadata.

## §N Non-goals
- Group chats, Moments (sns), favorites, and decrypting media files.
- Built-in analytics.
- Windows or macOS support.
- Writing to WeChat's DBs. The tool is **read-only** toward WeChat data.

## §C Constraints
- Go 1.27, linux/amd64 (ptrace register layout). A single static binary with `CGO_ENABLED=0`.
- TUI: `charm.land/bubbletea/v2`, `charm.land/bubbles/v2`, `charm.land/lipgloss/v2`.
- Dependencies:
  - `modernc.org/sqlite` (pure-Go SQLite)
  - `github.com/klauspost/compress/zstd`
  - `golang.org/x/crypto/pbkdf2`
  - `golang.org/x/sys/unix` (ptrace, `process_vm_readv`)
  - `github.com/BurntSushi/toml` (config)
  - The CLI uses stdlib `flag` with a small subcommand dispatcher.
- Never log or print the passphrase or keys, except with an explicit `key show --reveal`.

## §I Interfaces

**CLI** (`cmd/wxexport`):
```
wxexport                         # launches TUI (default)
wxexport doctor                  # env report: flatpak, binary, account(s), ptrace_scope, key status
wxexport key capture [--timeout 180s] [--no-restart]   # relaunch WeChat under tracer, catch passphrase
wxexport key set --passphrase <hex64>                    # manual
wxexport key check               # validate cached key against every DB (page-1 HMAC)
wxexport key forget
wxexport contacts [--search s] [--json]
wxexport export --contact <username|remark|nick> --format json,csv,sqlite --out <dir>
                [--from YYYY-MM-DD] [--to YYYY-MM-DD] [--kinds text,image,call,...]
                [--tz Europe/Paris] [--raw] [--force]
wxexport debug types --contact X   # histogram of (type,subtype) + N sample raw contents (to calibrate parsers)
global flags: --data-dir <xwechat_files/...> --account <wxid> --config <path> -v
```

**Files**:
- Config: `$XDG_CONFIG_HOME/wxexport/config.toml`. Holds the defaults: tz, formats, out dir, raw toggle, data dir override.
- Key cache: `$XDG_CONFIG_HOME/wxexport/keys.json`, mode **0600**:
  `{version:1, account, wechat_version, binary_sha256, hook_offset, passphrase_hex, captured_at, dbs:{<relpath>:{salt_hex, enc_key_hex}}}`.
- Plaintext work dir: `$XDG_RUNTIME_DIR/wxexport-<pid>/`, mode 0700. It is wiped on exit, including on SIGINT and SIGTERM.

## §D Crypto & storage spec (SQLCipher 4 profile used by WCDB)
- Page size 4096. Reserve 80 bytes = IV(16) + HMAC-SHA512(64). The IV is at `page[4016:4032]` and the HMAC at `page[4032:4096]`.
- Salt = first 16 bytes of the DB file, different for each file.
- `enc_key = PBKDF2-HMAC-SHA512(passphrase32, salt, 256000, 32)`.
- `mac_key = PBKDF2-HMAC-SHA512(enc_key, salt XOR 0x3a (each byte), 2, 32)`.
- Page n (1-based):
  - `off = 16 if n==1 else 0`.
  - Check `HMAC-SHA512(mac_key, page[off:4032] || le_u32(n)) == page[4032:4096]`.
  - Then `plain[off:4016] = AES-256-CBC-decrypt(enc_key, iv, page[off:4016])`, with no padding.
- Plaintext output:
  - Page 1 = `"SQLite format 3\x00"` + decrypted bytes + 80 zero bytes.
  - Other pages = decrypted bytes + 80 zero bytes.
  - Header byte 20 (reserved space) stays at 80, which is valid SQLite.
- **WAL**:
  - The 32-byte header is plaintext. Check the magic `0x377f0682` / `0x377f0683` (which picks the checksum endianness), page size 4096, and salt-1/salt-2.
  - Each frame is a 24-byte plaintext header plus a 4096-byte page that is encrypted exactly like DB page `pgno`.
  - A frame is valid while its header salts match and the cumulative checksum (computed over the header's first 8 bytes and the *stored, encrypted* page bytes) matches.
  - Apply frames up to and including the **last valid commit frame** (`db_size_after_commit != 0`). Later frames for the same pgno win. Truncate or extend the output to the commit size. Ignore everything after a mismatch.
- **Snapshot**:
  - Copy `X.db`, `X.db-wal` and `X.db-shm` (shm is not needed) into the work dir with `O_RDONLY`.
  - Re-stat the DB size and mtime afterwards, and retry up to 5 times if they changed.
  - Never open the live files with SQLite.

## §K Key acquisition spec (WeChat 4.1.x, flatpak)
1. **Binary location**: run `flatpak info --show-location com.tencent.WeChat` and append `/files/extra/wechat/wechat`. Compute its sha256 and read the version from `flatpak info`.
2. **Hook-offset ELF analysis** (`debug/elf`, x86-64):
   1. Find the anchor `com.Tencent.WCDB.Config.Cipher` in `.rodata` and get its VA.
   2. Scan `.text` for `48 8D 35 <disp32>` (`lea rsi,[rip+d]`) instructions whose target is the anchor VA.
   3. The instruction 7 bytes earlier must be `48 8D 3D <disp32>` (`lea rdi`). Its target is `cfgVA`.
   4. Scan `.text` for `lea rsi` instructions that target `cfgVA`. From each one, walk back at most 0x500 bytes to the prologue `55 41 57` (push rbp; push r15).
   5. The lowest such function start is `hook_va`.
   6. Cache it keyed by binary sha256.
3. **Launch and attach** (one OS-locked goroutine owns every ptrace call):
   1. Precondition: WeChat is not running. If it is, the TUI asks to confirm `flatpak kill com.tencent.WeChat`.
   2. `exec.Command("flatpak","run","com.tencent.WeChat")` with `Setpgid`.
   3. Every 2 ms, scan `/proc/*` for a pid whose `exe` readlink is `/app/extra/wechat/wechat` **and** that descends from our child (walk the PPid chain).
   4. `PTRACE_SEIZE` + `PTRACE_INTERRUPT` with options `TRACECLONE|TRACEFORK|TRACEVFORK|TRACEEXEC`, then attach to existing threads in `/proc/pid/task`.
4. **Breakpoint**:
   1. `base` = start of the maps line for `/app/extra/wechat/wechat` with file offset 0. `bp = base + hook_va - first_load_vaddr`.
   2. Save the original word and poke `0xCC` into it. Loop on `wait4(-1, __WALL)`. Non-breakpoint signals are re-injected and new threads tracked.
   3. On SIGTRAP with `rip-1 == bp`, read registers and collect candidates:
      - (a) `rdx==32` → 32 bytes at `rsi`;
      - (b) `*(u64)(rsi+16)==32` → 32 bytes at `*(u64)(rsi+8)`.
   4. Read the bytes with `process_vm_readv` (fallback: PEEKDATA).
   5. Check each candidate by deriving the key and checking the page-1 HMAC against one probe DB (`message/message_0.db`).
   6. Restore the original byte, set `rip=bp`, single-step, then re-arm or finish.
5. **Finish**:
   - Remove the breakpoint, detach every thread, and leave WeChat running.
   - Derive `enc_key` for every DB in parallel and write `keys.json` (0600).
   - Timeout: 180 s by default, cancellable.
   - The user must click **Log in** in WeChat during capture.
6. **Failure handling**: each case has a specific error message and remedy.
   - `EPERM` on seize: suggest `sudo wxexport key capture` or `sudo sysctl kernel.yama.ptrace_scope=0` (temporary).
   - Anchor or hook not found: report an unsupported version and suggest `key set`.
   - Timeout.
   - Candidate rejected.
   - WeChat exited (possible anti-debug).
7. **Key resolution at startup**:
   1. Use the cached `dbs` entries if every page-1 HMAC still validates.
   2. Otherwise re-derive from the cached passphrase (this handles new DB files or new salts).
   3. Otherwise report "no key" and offer capture.

## §M Message model & decoding spec

Source DBs:
- `contact.db` `contact(username, alias, remark, nick_name, local_type, …)`.
- `session.db` `SessionTable(username, last_timestamp, summary, …)`.
- Every `message/message_*.db` shard, excluding `*fts*`, `*resource*`, `biz_*` and `media_*`, provides:
  - `Msg_<md5hex(username)>(local_id, server_id, local_type, sort_seq, real_sender_id, create_time, status, source, message_content, compress_content, packed_info_data, WCDB_CT_message_content, WCDB_CT_source)`
  - `Name2Id(rowid, user_name)`.
- A contact's messages = UNION over shards, **deduped by (server_id, create_time, local_type)** when server_id≠0, ordered by `create_time, sort_seq, local_id`.
- Columns are discovered with `PRAGMA table_info`, so a missing column degrades gracefully instead of crashing.

Decoding rules:
- `type = local_type & 0xFFFFFFFF`, `sub = local_type >> 32`.
- Content: if `WCDB_CT_message_content == 4` or the bytes start with `28 b5 2f fd`, zstd-decode it (20 MiB cap). Otherwise treat it as UTF-8. The same applies to `source`.
- Direction: `sender = Name2Id[real_sender_id]`.
  - `sent` if `sender == myWxid`, `received` if `sender == contact`, otherwise `unknown`.
  - Unknowns are counted and shown in `doctor` and in export meta.
- 1:1 contacts (calibrated against real data — `contact.local_type` does **not** distinguish a
  person from an official account, so it is not used): exclude `username` ending in
  `@chatroom` (groups), starting with `gh_` (official/subscription accounts), or exactly one of
  the built-in pseudo-contacts `weixin`, `medianote`, `fmessage`, `notifymessage`,
  `floatbottle`, `qqmail`, `brandservicesessionholder`, `brandsessionholder`, `filehelper`. Keep
  everything else, including `@openim` (WeCom) contacts and legacy non-`wxid_` usernames. A
  "show all" toggle lifts this filter entirely (shows every session, including groups).
- Display name: remark, then nick_name, then alias, then username.

Normalized `Kind` values:

| type / sub | Kind | Extracted fields |
|---|---|---|
| 1 | `text` | text, char_count (runes) |
| 3 | `image` | media.md5, media.size (from XML `length`/`hdlength`), is_picture=true |
| 34 | `voice` | media.duration_ms (`voicelength`) |
| 43 | `video` | media.duration_s (`playlength`), size |
| 47 | `sticker` | media.md5, media.size (from XML `len`, not `length`) |
| 48 | `location` | label (falls back to poiname), lat (`x`), lng (`y`) |
| 42 | `contact_card` | username |
| 49 / 5 | `link` | title, url |
| 49 / 6 | `file` | title, ext, size |
| 49 / 57 | `quote` | text + referenced message summary |
| 49 / 19, 62 | `forward` | title |
| 49 / 4, 33, 36 | `applet` | title |
| 49 / 2000 | `transfer` | amount text |
| 49 / 2001 | `red_packet` | — |
| 49 / other | `app` | sub, title |
| 50 | `call` | call.{media voice/video/unknown, outcome, duration_s} |
| 10000 / 10002 with revokemsg | `recall` | — |
| 10000 other | `system` | text |
| else | `unknown` | type, sub |

**Call parser** (calibrated against real calls — see docs/FORMAT.md for the full XML sample):
- Parses the real `<voipmsg type="VoIPBubbleMsg"><VoIPBubbleMsg><msg><![CDATA[...]]></msg>
  <room_type>…</room_type>…</VoIPBubbleMsg></voipmsg>` XML. The `<duration>` field is **always
  `0` in practice and must not be trusted**; the only real duration is inside `<msg>`'s text.
  Falls back to scanning the plain content for the same text patterns if the XML doesn't parse.
- Duration regexes accept `mm:ss` and `h:mm:ss` after `Duration:` or `Call ended` (both
  confirmed real English wordings), or standalone after `通话中断` (which marks an
  interrupted-but-connected call, kind `interrupted`, still has a real duration).
- Outcome keywords (EN and ZH), checked against the `<msg>` text — run against all real calls sampled
  in the account's main chat, 0 landed in `unknown`:
  - completed: `Duration:` or `Call ended` with a parsed time (both confirmed real strings), or `通话中断` → `interrupted` (also completed-with-duration for aggregation purposes)
  - missed: `Already answered elsewhere` and `Call wasn't answered` (both confirmed real strings; `msg_type` is `101` for "answered elsewhere", `100` otherwise) — also `Missed`, `未接听`, `对方无应答` (documented but unconfirmed by real data)
  - canceled: `Canceled`, `已取消`
  - declined: `Call declined` (confirmed real string), `Declined`, `已拒绝`, `对方已拒绝`
  - busy: `Busy`, `忙线` (unconfirmed by real data)
  - otherwise: unknown
- Voice vs video: `room_type` was `0` for all real calls sampled sampled (no video call was available
  to confirm its value), so this falls back to keyword matching (`Video call` / `视频通话`) in
  the `<msg>` text; unconfirmed by real data.
- Unparsed calls are kept with `outcome=unknown` and the raw content when `--raw` is set. They are never dropped.

`Message` fields (also the export columns):
`local_id, server_id, shard, contact_username, sender_username, direction, timestamp_unix, timestamp_iso (RFC3339 in tz), date (YYYY-MM-DD in tz), iso_week (YYYY-Www), hour, type, sub_type, kind, text, char_count, is_picture, media_md5, media_size, media_duration_s, call_media, call_outcome, call_duration_s, url, title, raw_content (only with --raw)`.

## §E Export spec
- Filters: one contact, an inclusive date range in the chosen tz, and a set of kinds.
- Atomic: write to `name.tmp` then rename. Refuse to overwrite without `--force` or a TUI confirmation. Files are created with mode 0600.
- **JSON** `<contact>_<from>_<to>.json`:
  `{meta:{tool_version, exported_at, account, contact:{username, display_name}, tz, filters, counts_by_kind, unknown_direction_count, wechat_version}, messages:[Message…], calls:[subset view]}`.
  Written with a streaming encoder, so large chats don't need it all in memory.
- **CSV**: `messages.csv` (all the columns above) and `calls.csv` (local_id, timestamp_iso, date, iso_week, direction, call_media, call_outcome, call_duration_s).
  RFC 4180 via `encoding/csv`, UTF-8, with an optional BOM (`--excel`). Empty cells represent null.
- **SQLite** `<contact>.sqlite` (modernc):
  - Tables `meta(key, value)`, `contact`, `messages` (the columns above with typed affinities) and `calls`.
  - Indexes on `date`, `iso_week`, `kind` and `direction`.
  - Inserted in one transaction with a prepared statement.
  - No aggregates (the user chose raw export only). The README gives example queries for per-day counts, call minutes per week and picture counts.

## §U TUI spec (Bubble Tea v2)
**Implemented scope (deliberate reduction from the original sketch below):** four screens
(home, contacts, conversation, export) in one root `tui.Model`, built on bubbletea/v2,
bubbles/v2's `viewport` and `textinput`, and lipgloss/v2. Screens are a `screen` enum on the
root model rather than a screen stack; navigation is direct (no history beyond one "back" per
screen). The contacts list is a custom filterable slice+cursor (not `bubbles/list`), since a
plain substring filter and arrow-key cursor covers §U's actual requirement without that
component's own filtering state machine. There is **no dedicated interactive key-capture
screen**: `wxexport key capture` (the CLI command) already does this fully, including the
breakpoint wait and liveness check; the home screen reports key status and points at it, rather
than re-implementing the flow in the TUI. A `tui.Backend` interface (`Status`, `Contacts`,
`Messages`, `Export`) decouples the model from `wcdb`/`export` for testing. Verified end-to-end
against the real account via a pty-driven smoke test (home → contacts → filter → conversation,
with real decoded text/call/image/sticker/quote rows and date separators, clean quit).

Original sketch (superseded by the above for what's actually built, kept for the export form's
field list and the home/conversation key bindings, which the implementation does follow):
A root model holds a screen stack. Messages travel through `tea.Cmd`. Long work (snapshot and decrypt, capture, export) runs in commands that stream progress messages.

1. **Home / Status**:
   - Shows the install path, version, account, whether WeChat is running, key status, and the time of the last snapshot.
   - Keys: `c` capture key, `p` paste passphrase, `r` refresh snapshot, `enter` → Contacts.
2. **Key capture**:
   - A checklist with spinners: analyse binary → stop WeChat (confirm) → launch → attach → "Click **Log in** in WeChat now" with a countdown → validating → deriving keys.
   - A live log pane. `esc` cancels; cancelling detaches cleanly.
3. **Contacts**:
   - A `bubbles/list` of 1:1 contacts, ordered by the session's last timestamp, with fuzzy filtering (`/`).
   - Each row shows the display name, username, last message date and summary.
4. **Conversation**:
   - A `viewport` with chat bubbles: received on the left, sent on the right, date separators between days.
   - Kind badges such as `[image]`, `[voice 0:12]` and `[video call · 05:23 · completed]`.
   - The header shows totals (messages, first and last date, calls, pictures), which are simple counts, not analytics.
   - Keys: `/` search, `n`/`N` for next and previous hit, `g` jump to a date, `f` filter kinds, `e` → Export with this contact preselected.
   - Messages load lazily in pages of 500.
5. **Export**:
   - A form with format checkboxes, output dir (textinput, default from config), from/to dates (validated), kind multi-select, tz, a raw toggle, and an excel BOM toggle.
   - Shows a live preview of the matching count.
   - `enter` runs the export with a progress bar, then shows the written file paths and counts. `s` saves the choices as the config defaults.
- Global keys: `?` help (`bubbles/help`), `q`/`ctrl+c` quit (the work dir is cleaned up), and window resize handling.

## §V Invariants (each one backed by a test)
1. The tool never opens or writes WeChat's live files in write mode, and SQLite only ever opens copies.
2. Decryption verifies the HMAC of **every** page. Any mismatch is a hard error naming the DB and page, never silent garbage.
3. Uncommitted or invalid WAL frames are never applied.
4. The key cache and the plaintext work dir are mode 0600/0700. The work dir is removed on every exit path.
5. The passphrase and keys never appear in logs, errors or exports.
6. Every source row produces exactly one exported message, with no drops on parse failure (it falls back to `unknown`), and duplicates across shards are removed.
7. `date` and `iso_week` are computed in the configured tz, which defaults to the system local tz and is recorded in meta.
8. All ptrace calls happen on one OS-locked thread, and WeChat is always detached and left running, including on error or cancel.
9. Exports are atomic and never overwrite without consent.
10. After `ptracecap.Capture` returns, the caller verifies the target pid stays alive for a bounded window before reporting success; it must never fold "key captured" and "WeChat left running" into one claim, and must surface a distinct, clearly-worded warning if the target exits (see §B.1 — against the real WeChat binary, §V.8's "left running" half of this has been observed to fail even though detach itself completes without error, so it is checked explicitly rather than assumed).
11. `sqlciphertest.EncryptDB` (and any other SQLCipher-page-reserve test fixture) is only ever fed non-queryable, reserve-respecting content (real data confined to byte offsets `[0, UsableSize)` of each page, with the trailing 80 bytes left zero) — never a real SQLite file retrofitted after the fact, since a real file's own b-tree layout can legitimately place content in that trailing region (§B.2). Tests that need a real, queryable database to exercise `contacts`/`export`/`wcdb` use plain (already-decrypted) SQLite files via the pipeline's own unencrypted pass-through, never the test-only encryptor.

## §B Bugs found (backprop log)

| id | date | root cause | invariant |
|----|------|-----------|-----------|
| B1 | 2026-11-05 | A real WeChat 4.1.13.26 process, captured and detached cleanly by `ptracecap.Capture` (no error, `detachAll` succeeded, `internal/ptracecap`'s own end-to-end test proves the detach mechanics are correct against a synthetic multi-threaded target), nonetheless exited on its own ~7s after detach. Isolated via a control run: a normally-launched (untraced) WeChat instance of the same binary/account stayed alive 2+ minutes, while every traced capture killed the real instance. Cause unconfirmed — leading theories are (a) WeChat's own anti-debug/anti-tamper logic (e.g. polling `TracerPid` in `/proc/self/status`, or detecting the brief real `SIGSTOP` `ensureStopped` sends, or the INT3/single-step restore sequence) deliberately self-terminating once it notices it was traced, or (b) an unidentified side effect of our breakpoint's single-step-and-restore on that specific code path. The captured key itself is unaffected and was HMAC-validated against all 21 real databases — only the "leave WeChat running" side of the capture fails. Not yet fixed; `key capture` now detects and reports this explicitly (§V.10) instead of claiming success silently. | V10 |
| B2 | 2026-11-06 | Building an e2e fixture by creating a real multi-table SQLite database and pushing it through `sqlciphertest.EncryptDB` (the SQLCipher-page-reserve test encryptor) silently corrupted data: decrypting it back produced "malformed database schema". Root cause: SQLite's own b-tree pages pack cell content from the *end* of the page backward, so even a tiny schema's content legitimately reaches into the last bytes of page 1 — exactly the 80-byte region a genuine SQLCipher/WCDB database never touches, because that database's usable page size was reduced from the moment it was created. `EncryptDB`/`DecryptFile`'s page-reserve round-trip is correct (proven by `internal/sqlcipher`'s own tests, which deliberately use non-queryable random content confined to the usable region); the bug was in retrofitting an ordinary SQLite file into that scheme after the fact. Fixed by splitting e2e fixtures: key mechanics (encrypt/HMAC/cache) use the same reserve-respecting random-content style as `internal/sqlcipher`'s unit tests; `contacts`/`export` use plain (already-decrypted) databases via the pipeline's existing pass-through support for unencrypted files. | V11 |

---

