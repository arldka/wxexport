<div align="center">

# 💬 wxexport

**将你的微信 1:1 聊天与通话记录导出为 JSON、CSV 和 SQLite。**

用于你自己的数据分析 · Linux 上 flatpak 版微信 4.1.x · 使用 Go 编写

![状态](https://img.shields.io/badge/状态-概念验证-orange)
![平台](https://img.shields.io/badge/平台-Linux%20x86--64-informational)
![Go](https://img.shields.io/badge/Go-1.x-00ADD8?logo=go&logoColor=white)
![格式](https://img.shields.io/badge/导出-JSON%20%7C%20CSV%20%7C%20SQLite-brightgreen)

[English](README.md) · **简体中文**

[使用方法](#使用方法) · [状态](#状态) · [隐私与安全](#隐私与安全说明) · [规范](docs/SPEC.md) · [格式说明](docs/FORMAT.md)

</div>

---

> [!WARNING]
> **概念验证 / 开发中项目，使用风险自负。**
> 这是一个实验性的个人项目，并非成熟或受支持的产品。软件按"现状"提供，不附带任何形式的担保。
> 它可能存在缺陷，导出的数据可能不完整或不正确，可能因微信更新而失效，或者(见下文已知问题)
> 导致微信退出。请先备份你的数据。使用后果由你自行承担。

> [!CAUTION]
> **仅限用于你自己的数据。**
> wxexport 仅用于在**你自己的设备**上，通过**你自己已登录的会话**，导出**你自己的**微信账号历史记录。
> 请勿用于他人的账号，设备或消息，也不要在未获明确授权的场景中使用。否则可能违法(未经授权访问，
> 隐私与数据保护法律)，并可能违反微信的服务条款。导出的内容包含私人对话(包括他人的消息)：
> 请妥善保管，切勿公开或分享。

将微信 1:1 单聊消息和通话记录导出为 JSON，CSV 和 SQLite。完整规范见 `docs/SPEC.md`,
基于真实账号校准的发现见 `docs/FORMAT.md`。

## 状态

早期，实验性，仅在单一环境下测试过(微信 4.1.13.26，flatpak，x86-64 Linux)。
已实现：密钥获取，数据库解密，联系人列表，消息解码，导出为三种格式，以及基础 TUI。
可能存在各种不完善之处，在其他微信版本上很可能无法使用。

**已知问题**(docs/SPEC.md §B.1)：已观察到 `wxexport key capture` 在成功获取密钥几秒后会使真实的微信进程退出。
获取到的密钥仍然有效；之后你可能需要自行重新启动微信。`key capture` 会检测到这种情况并给出警告。

## `key capture` 做了什么，为什么它很敏感

为了读取你的数据库，`key capture` 会以子进程方式重新启动微信，并在登录时使用 `ptrace` 读取一次数据库口令。
这是一种调试器式的技术，只适用于**你自己机器上你自己的账号**。口令保存在
`~/.config/wxexport/keys.json`(权限 0600)。任何人获得该文件以及你的数据库文件，
都可以读取你的聊天记录，因此切勿提交，上传或分享它(它已在 `.gitignore` 中)。

## 隐私与安全说明

- 解密后的数据库副本在工具运行期间写入私有临时目录(`$XDG_RUNTIME_DIR`)，退出时删除。
  如果工具被强制终止，请自行删除残留的 `wxexport-*` 目录。
- 导出文件(JSON/CSV/SQLite)以仅所有者可访问的权限(0600)写入，且包含明文消息，请相应妥善保存。
- 以 `=`，`+`，`-` 或 `@` 开头的 CSV 单元格会被加上 `'` 前缀，避免电子表格把消息内容当作公式执行。
- 请勿把真实的聊天内容，密钥或数据库文件粘贴到公开的 issue 或 pull request 中。

## 使用方法

```sh
go build -o bin/wxexport ./cmd/wxexport

./bin/wxexport doctor                       # 检查运行环境
./bin/wxexport key capture                   # 一次性操作:获取数据库密钥
./bin/wxexport key check                     # 重新验证已缓存的密钥
./bin/wxexport contacts                      # 列出 1:1 联系人
./bin/wxexport contacts --search Alice
./bin/wxexport debug types --contact Alice   # (type,sub) 直方图,用于校准

./bin/wxexport export --contact Alice \
    --format json,csv,sqlite --out ./export \
    --from 2024-01-01 --to 2024-12-31 \
    --tz Europe/Paris
```

全局参数(`--data-dir`、`--account`、`--config`)以及 `export` 自身的参数(`--kinds`、`--raw`、
`--excel`、`--force`)详见 `wxexport -h` / `wxexport export -h`。

## 分析示例(SQLite 导出)

```sql
-- 每天的消息数
SELECT date, count(*) FROM messages GROUP BY date ORDER BY date DESC;

-- 每天的消息数,区分发送与接收
SELECT date, direction, count(*) FROM messages GROUP BY date, direction ORDER BY date DESC;

-- 每个 ISO 周的通话分钟数
SELECT iso_week, sum(call_duration_s) / 60.0 AS minutes FROM calls GROUP BY iso_week ORDER BY iso_week;

-- 平均通话时长(仅限已完成/被中断的通话)
SELECT avg(call_duration_s) FROM calls WHERE call_outcome IN ('completed', 'interrupted');

-- 每月交换的图片数
SELECT substr(date, 1, 7) AS month, count(*) FROM messages WHERE kind = 'image' GROUP BY month;

-- 一天中最忙的小时
SELECT hour, count(*) FROM messages GROUP BY hour ORDER BY count(*) DESC LIMIT 5;
```

## 免责声明

wxexport 是一个独立项目，与腾讯或微信没有任何关联，也未获其认可或支持。"微信/WeChat"是其所有者的商标。
对于因使用本软件造成的数据丢失，账号处罚或任何其他后果，作者概不负责；请参阅顶部的概念验证声明。
请仅在你自己的数据上使用。

## 测试

```sh
make test          # go vet + go test ./...
make race          # go test -race ./...(需要 CGO_ENABLED=1)
```
