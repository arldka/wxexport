# Calibration findings (Phase 4, against a real WeChat 4.1.13.26 account)

Everything below was confirmed by decrypting the real databases with `wxexport` and inspecting
them directly (schema via `sqlite3`, content via a throwaway zstd-decompression tool). Real
usernames, names and message text are **not** reproduced here or in test fixtures; only
structure (tags/attributes/values that matter for parsing) is kept, with fabricated data.

## Schema: matches §M exactly

`contact.db` `contact` table, `session.db` `SessionTable`, and `message_N.db`
`Msg_<md5(username)>` + `Name2Id` all match the columns listed in SPEC.md §M verbatim, including
indexes (`_SENDERID`, `_SERVERID`, `_SORTSEQ`, `_TYPE_SEQ`).

`Name2Id(user_name TEXT PRIMARY KEY, is_session INTEGER)` has **no explicit integer id column**;
`real_sender_id` refers to SQLite's implicit `rowid` of this table (confirmed: rowid 1 was the
chat's other participant, rowid 2 was the account's own wxid).

## Contact filter: §M's draft rule was wrong, corrected here

The original spec guessed `contact.local_type in {1,2}` selects real 1:1 contacts. Real data
disproves this:
- `local_type` values seen: `0` (service/system, e.g. `notifymessage`), `1` (used for **both**
  official/subscription accounts `gh_*` **and** normal personal friends, e.g. `weixin`,
  `medianote`, `fmessage`, `filehelper`, and ordinary `wxid_*` friends), `2` (`@chatroom` groups),
  `3` (also ordinary personal `wxid_*` friends, no detectable difference from type 1 ones other
  than presumably one-way/pending vs. mutual), `5`/`6` (`@openim` — WeCom/enterprise external
  contacts, still legitimate 1:1 conversations).
- **`local_type` cannot distinguish a real person from an official account.** The correct filter
  is string-based:
  - Exclude `username` ending in `@chatroom` (groups).
  - Exclude `username` starting with `gh_` (official/subscription accounts).
  - Exclude a small fixed set of built-in pseudo-contacts: `weixin`, `medianote`, `fmessage`,
    `notifymessage`, `floatbottle`, `qqmail`, `brandservicesessionholder`, `brandsessionholder`,
    `filehelper`.
  - Keep everything else, including `@openim` accounts and legacy (pre-`wxid_`) numeric/alpha
    usernames (seen in the wild in this account's own session list).
- Display name precedence (remark → nick_name → alias → username) is confirmed correct.

## `local_type` bit layout: confirmed

`type = local_type & 0xFFFFFFFF`, `sub = local_type >> 32`. Confirmed real values: `1` (text),
`3` (image), `34` (voice — not present in the sampled chat, kept from research), `43` (video —
present), `47` (sticker), `48` (location), `50` (call), `10000` (system), and 64-bit values
`244813135921` → `(49, 57)` quote/reply, `21474836529` → `(49, 5)` link, `25769803825` → `(49,
6)` file. All match SPEC.md's table exactly.

## Compression: confirmed

`WCDB_CT_message_content == 4` (and the same flag column pattern for `source`) means zstd,
magic `28 b5 2f fd`. Plain text rows have the flag `0`/NULL. Both `message_content` and `source`
use the same flag independently.

## Call (type 50): real `<voipmsg>` XML, duration only in text

```xml
<voipmsg type="VoIPBubbleMsg"><VoIPBubbleMsg><msg><![CDATA[Duration: 39:18]]></msg>
<room_type>0</room_type>
<red_dot>false</red_dot>
<roomid>...</roomid>
<roomkey>0</roomkey>
<inviteid>...</inviteid>
<msg_type>100</msg_type>
<timestamp>...</timestamp>
<identity>...</identity>
<duration>0</duration>
<inviteid64>...</inviteid64>
<business>1</business>
<caller_memberid>0</caller_memberid>
<callee_memberid>1</callee_memberid>
<force_update>0</force_update>
</VoIPBubbleMsg></voipmsg>
```

- **The `<duration>` field itself is always `0`.** The only place a real duration appears is the
  human-readable text inside `<msg><![CDATA[...]]]></msg>`. Any implementation that trusts
  `<duration>` will get zero for every call; this tool parses the `<msg>` text instead.
- Observed `<msg>` text forms, across **all real calls in the account's main chat** (a
  second shard's chats had 0 calls to check): the decoder was run against every one of these
  and classified all of them (0 `unknown`), with this exact outcome breakdown: `completed` 73,
  `missed` 10, `interrupted` 8, `canceled` 3, `declined` 3.
  - `Duration: MM:SS` / `Duration: H:MM:SS` and `Call ended MM:SS` (English, completed call —
    two different wordings for the same outcome were both observed).
  - `通话中断 MM:SS` / `通话中断 H:MM:SS` (Chinese, "call interrupted" — still carries a real
    elapsed duration; treated as a **completed** call with that duration, kind `interrupted`
    noted separately for callers who want it).
  - `Already answered elsewhere` and `Call wasn't answered` — both observed; the call was not
    answered on this device (either answered on another of the user's own devices, or not
    answered at all). `msg_type` is `101` for every "answered elsewhere" case (`100` for
    everything else observed, including "wasn't answered"). Both treated as `missed`.
  - `Canceled` — caller canceled before pickup.
  - `Call declined` — callee explicitly declined.
  - No `Missed`/`未接听`/`Busy`/`忙线` strings were observed in this sample, but are kept as
    recognized fallbacks per prior research since they are documented WeChat UI strings.
- `room_type` was `0` for every one of the calls in the sampled chat (all apparently voice
  calls); no video call sample was available to confirm its video value, so voice/video
  detection falls back to keyword matching (`Video call`/`视频通话`) in the `<msg>` text, which is
  also unconfirmed by real data but is the best available signal.

## Media XML: confirmed, with one correction

- **Image (type 3):** `<img ... length="12345" md5="0123456789abcdef0123456789abcdef" .../>` —
  matches §M exactly (`length` and `md5` attributes on `<img>`).
- **Sticker (type 47):** `<emoji ... md5="..." len="2048" .../>` — **the size attribute is
  `len`, not `length`** (different from image). §M's table is corrected accordingly.
- **Location (type 48):** `<location x="12.34..." y="56.78..." label="" poiname="Location"
  .../>` — `x` is latitude, `y` is longitude (confirmed by real-world magnitude: x in the latitude
  range, y in the longitude range). `label` can be
  empty with the human-readable name only in `poiname`; display text should prefer `label` then
  fall back to `poiname`.
- **Link (type 49/5):** `<appmsg><title>`, `<des>`, `<url>` present as expected.
- **File (type 49/6):** `<appmsg><title>` (filename with extension already included),
  `<appattach><totallen>` (bytes), `<appattach><fileext>` is a **child element**, not an
  attribute (§M's table said "attribute"; corrected).
- **Quote/reply (type 49/57):** `<appmsg><title>` (the reply text) plus a `<refermsg>` block with
  `<fromusr>`, `<chatusr>`, `<displayname>`, `<content>` (itself escaped/nested XML or plain
  text depending on the quoted message's own type), `<createtime>`. Matches §M.

## Sender resolution: `real_sender_id` can legitimately resolve to an empty username

Running the full `wcdb` package against the real account's largest chat (thousands of merged messages
across 2 shards) gave some **`unknown`** direction messages. Every one of
them is `local_type=10000` (system messages, e.g. recall notices) whose `real_sender_id` maps to a
`Name2Id` row with an **empty `user_name`** (confirmed: `Name2Id` rowid 3 in that shard has
`user_name=''`). This is real WeChat data, not a decoder bug — system messages legitimately have
no human sender, and `direction=unknown` is the correct, honest result (per docs/SPEC.md §V — 
unknowns are counted and surfaced, never guessed).

## Sender resolution: confirmed, with a useful cross-check

`real_sender_id` → `Name2Id` rowid lookup is confirmed correct and matches the account's own
wxid for rowid-mapped self-messages. As an extra cross-check (not required, not relied upon):
sticker (`<emoji fromusername=".." tousername="..">`) and some appmsg payloads carry explicit
`fromusername`, which independently agreed with the `real_sender_id` resolution in every sampled
case.
