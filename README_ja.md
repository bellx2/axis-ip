# axis-ip

[English](README.md) | 日本語

macOS、Linux、Windows 向けの非公式な Axis カメラ IP ユーティリティ（CLI と TUI）。同じ L2 セグメント上の Axis 機器を mDNS で探し、VAPIX Network Settings API で IPv4 アドレスを変更する。

公式の AXIS IP Utility は Windows 専用である。axis-ip は、機器を探す、ネットワーク設定を読む、静的アドレスを割り当てる、DHCP に戻す、という日常の作業を Mac や Linux マシンで行う。Windows でも動き、スクリプトから使える CLI と、ほかの OS と同じ操作を提供する。

> [!NOTE]
> axis-ip は個人による非公式のプロジェクトであり、Axis Communications AB とは関係がなく、同社の承認やサポートを受けていない。Axis 製品についての問い合わせは Axis へ行う。

## 動作環境

- macOS、Linux（amd64 / arm64）、Windows 10 以降（amd64 / arm64）
- AXIS OS 8.50 以降の Axis 機器（Network Settings API）
- ソースからビルドする場合は Go 1.26 以降。macOS では Xcode Command Line Tools も要る（cgo に C コンパイラが要る）

## インストール

### macOS：Homebrew

```sh
brew install bellx2/tap/axis-ip
```

[bellx2/homebrew-tap](https://github.com/bellx2/homebrew-tap) を tap し、axis-ip をソースからビルドする。ビルドに使う Go は Homebrew が依存として入れるので、事前に用意しなくてよい。

更新と削除は次のとおり。

```sh
brew upgrade axis-ip
brew uninstall axis-ip
```

初回の実行時に、ターミナルアプリへのローカルネットワークのアクセス許可を macOS が求めることがある。許可するか、[トラブルシューティング](#トラブルシューティング) を参照する。

### Linux：ビルド済みバイナリ

[リリースページ](https://github.com/bellx2/axis-ip/releases) から `axis-ip_<version>_linux_amd64.tar.gz` か `axis-ip_<version>_linux_arm64.tar.gz` をダウンロードし、`axis-ip` を `PATH` の通った場所に置く。

```sh
tar xzf axis-ip_<version>_linux_amd64.tar.gz
sudo install axis-ip_<version>_linux_amd64/axis-ip /usr/local/bin/
```

バイナリは静的リンクで、ほかのパッケージは要らない。同じページの `checksums.txt` に SHA-256 がある。

### Windows：ビルド済みバイナリ

[リリースページ](https://github.com/bellx2/axis-ip/releases) から `axis-ip_<version>_windows_amd64.zip` か `axis-ip_<version>_windows_arm64.zip` をダウンロードして展開し、Windows Terminal か PowerShell で `axis-ip.exe` を実行する。どこからでも実行するには、展開したフォルダを `PATH` に加える。実行ファイルのプロパティにはバージョンと著作権表示が入っている。

### Go

```sh
go install github.com/bellx2/axis-ip@latest
```

### ソースから

```sh
go build -o axis-ip .
```

TUI には [Bubble Tea](https://github.com/charmbracelet/bubbletea)、Bubbles、Lip Gloss を使う。

## 使い方

### TUI

引数なしで `axis-ip` を実行すると TUI が開く。起動時にスキャンし、機器を選んでアドレスを読み書きできる。

```sh
axis-ip [tui] [-timeout 2s] [-http-timeout 10s]
```

| 画面 | 操作 |
| --- | --- |
| 機器一覧 | `↑`/`↓` で選択、`enter` で開く、`o` で Web 画面をブラウザで開く、`r` で再スキャン、`q` で終了 |
| ログイン | ユーザー（既定 `root`）とパスワードを入力して `enter`。`AXIS_PASSWORD` があればパスワードの初期値になる。`tab` で欄を切り替え、`esc` で戻る |
| 詳細 | `getNetworkInfo` の結果を表示する。`e` で変更、`o` で Web 画面をブラウザで開く、`r` で再読込、`esc` で戻る |
| 変更 | モード欄で `space` を押すと静的と DHCP が切り替わる。静的なら、アドレスを `IP/prefix` の形で、必要ならゲートウェイも入力する。`tab`/`↑`/`↓` で移動し、`enter` で確認画面へ進む |
| 確認 | 現在の設定と新しい設定を並べて表示する。`y` で書き込み、`n`/`esc` で変更画面へ戻る |

- 機器一覧の表は機器の数だけの高さになり、端末に収まらないときだけスクロールする。
- 接続には、スキャンで得た IPv4 の先頭を使う。リンクローカル以外のアドレスが優先される。
- 一度認証できた機器は、TUI を終了するまでパスワードを聞き直さない。
- 機器が新しい設定を受け付けると、5 秒待ってから再スキャンする。

### scan

`_axis-video._tcp` をブラウズし、型番、MAC、ホスト名、IPv4 を出す。リンクローカル（169.254/16）は後ろに並ぶ。

```sh
axis-ip scan [-timeout 2s]
```

```
MODEL     MAC           HOST                     IPV4
P1455     ACCC8E000001  axis-accc8e000001.local  192.168.0.20, 169.254.10.20
```

### get

機器に `getNetworkInfo` を送り、モード（dhcp / static）、使用中のアドレス、ルータを出す。

```sh
axis-ip get -ip 192.168.0.20 [-user root]
```

### set

静的アドレスを書き込むか、DHCP に戻す。書き込む前に `getNetworkInfo` で認証と対象インターフェースを確かめ、現在の設定と新しい設定を並べて確認を求める。

```sh
axis-ip set -ip 192.168.0.20 -address 192.168.0.90/24 [-gateway 192.168.0.1]
axis-ip set -ip 192.168.0.20 -dhcp
```

機器は入力を検証した時点で成功を返し、アドレスはその後で切り替わる。成功応答の直後に元の IP へ届かなくなるのは正常である。切り替えに失敗した場合、機器は元の設定に戻る。新しいアドレスは `scan` で確認する。

### フラグ

| フラグ | 対象 | 既定値 | 説明 |
| --- | --- | --- | --- |
| `-timeout` | tui, scan | `2s` | 探索時間 |
| `-http-timeout` | tui | `10s` | HTTP タイムアウト |
| `-ip` | get, set | なし（必須） | 機器の現在の IP アドレスまたはホスト名 |
| `-user` | get, set | `root` | 管理者ユーザー名 |
| `-password` | get, set | なし | パスワード（`AXIS_PASSWORD` が優先） |
| `-timeout` | get, set | `10s` | HTTP タイムアウト |
| `-address` | set | なし | 静的アドレス（`IP/prefix`） |
| `-gateway` | set | なし | デフォルトゲートウェイ（`-address` のサブネット内） |
| `-dhcp` | set | `false` | DHCP に戻す。`-address` と同時には指定できない |
| `-interface` | set | `eth0` | 対象インターフェース。`eth0` がなければ先頭のインターフェース |
| `-yes` | set | `false` | 確認を省略する |

IP は位置引数ではなく `-ip` で渡す。位置引数が残っているとエラーになるので、その後ろのフラグが黙って無視されることはない。

### パスワード

次の順で使う。

1. 環境変数 `AXIS_PASSWORD`
2. `-password`
3. 端末からの入力（エコーなし）

## 機器との通信

- macOS の探索は `DNSServiceBrowse`、`DNSServiceResolve`、`DNSServiceGetAddrInfo` で mDNSResponder のキャッシュを読む。
- Linux と Windows の探索は、空いている UDP ポートから mDNS の問い合わせを送り、機器はユニキャストで応答する（RFC 6762 の legacy unicast）。avahi-daemon も UDP 5353 も使わないので、avahi-daemon、systemd-resolved、Windows 組み込みの mDNS と同時に動く。
- 認証は HTTP Digest（MD5 または SHA-256、qop=auth）。
- まず HTTP で接続し、届かないかリダイレクトされたときは HTTPS に切り替える。HTTPS では自己署名証明書を受け入れる。
- 書き込みには、実行するマシンから `-ip` のアドレスへ届く必要がある。別サブネットの固定アドレスしか持たない機器は、そのサブネットのアドレスをローカルインターフェイスへ一時的に足してから実行する。例は次のとおり。

  ```sh
  sudo ifconfig en0 alias 192.168.0.50 netmask 255.255.255.0   # macOS
  sudo ip addr add 192.168.0.50/24 dev eth0                    # Linux
  netsh interface ipv4 add address "Ethernet" 192.168.0.50 255.255.255.0   # Windows（管理者として実行）
  ```

## トラブルシューティング

- **`curl` は届くのに `no route to host` になる：** macOS のローカルネットワーク許可でバイナリが止められている。システム設定 > プライバシーとセキュリティ > ローカルネットワーク で使っているターミナルアプリをオンにし、ターミナルを再起動する。`curl` のような Apple 製のツールはこの制限を受けないので、`curl` が通っても許可がある証拠にはならない。
- **`DNSServiceBrowse failed -65563 (Service Not Running)`：** mDNSResponder を使えないサンドボックス内で実行している。サンドボックスの外で実行する。
- **Linux で `scan` が何も見つけない：** ファイアウォール（ufw、firewalld、nftables）がユニキャストの応答を落としている可能性がある。応答は UDP 5353 から空きポートへ届く。ローカルネットワークからの送信元ポート 5353 の UDP を許可するか、ファイアウォールを止めて切り分ける。
- **Windows で `scan` が何も見つけない：** Windows Defender ファイアウォールがユニキャストの応答を落としている可能性がある。axis-ip.exe の通信を許可するか聞かれたら、プライベートネットワークで許可する。聞かれない場合は、axis-ip.exe について送信元ポート 5353 の UDP を許可する受信規則を足し、ネットワークのプロファイルがプライベートになっていることを確かめる。
- **WSL2：** 既定の NAT ネットワークではマルチキャストが LAN に届かない。mirrored モードを使う。
- **コンテナ：** マルチキャストが LAN に届くよう、ホストネットワーク（`docker run --network host`）で実行する。

## 商標

AXIS、AXIS COMMUNICATIONS、AXIS OS、VAPIX は、Axis AB の各国における商標または登録商標である。Windows と Windows Terminal は Microsoft グループの商標、macOS は Apple Inc. の商標、Linux は Linus Torvalds の登録商標である。その他の商標はそれぞれの所有者に帰属する。本書では互換性の説明のためにのみ用いている。

## ライセンス

MIT。[LICENSE](LICENSE) を参照。
