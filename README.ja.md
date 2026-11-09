<div align="center">

<img src="docs/images/mark.svg" width="72" alt="Tianshu のロゴ：北斗七星の一番星ドゥーベを灯したもの">

# Tianshu 天枢

**個人のサーバー、デバイス、サービス、プロジェクト、リポジトリを一画面で把握するセルフホスト型コントロールプレーン。読み取り専用の MCP サーバーで、AI エージェントにも同じ全体像を見せられます。**

[English](README.md) · [简体中文](README.zh-CN.md) · [繁體中文](README.zh-TW.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · [Français](README.fr.md) · [Deutsch](README.de.md) · [Português](README.pt-BR.md) · [Русский](README.ru.md)

</div>

![Tianshu の概要画面：ライブ星図、ホストごとの直近 60 分のテレメトリ、対応が必要な項目](docs/images/overview-dark.png)

天枢（てんすう）は北斗七星の第一星です。Tianshu は Web コンソールを埋め込んだ単一の Go バイナリで、「いま自分が対応すべきことはあるか」に一目で答えます。所有物のカタログを保ち、実際の状態の証拠を集め、短い対応リストにまとめ、同じ読み取りモデルを [Model Context Protocol](https://modelcontextprotocol.io) で AI エージェントに公開します。

## 主な機能

- ワークスペース（個人・会社・顧客）ごとのデバイス、サービス、プロジェクト、リポジトリ、認証情報の**参照**。
- ライブ星図：ワークスペースごとの軌道、形と色で健全性を表示。ホストが報告するたびに中心へパルスが届きます。
- テレメトリ帯：ホストごとの直近 60 分の実際の報告。データがなければ空白のままで、「正常」とは表示しません。
- 健全性・鮮度・ライフサイクル・検収を分けて管理。古いデータは健全とみなしません。
- 重複を除き、解消すると自動で閉じる対応リスト。1 日・1 週間・1 か月のスヌーズ。
- Linux / Windows 用エージェント、読み取り専用の SSH 収集、HTTP / TCP / TLS プローブ。
- SSH の露出監視（パスワードログイン、失敗ログイン、fail2ban、鍵のフィンガープリント）。
- MCP サーバー（Bearer トークンと OAuth 2.1）、ワークスペース分離、秘密情報を保存しない設計、全呼び出しの監査ログ。
- デバイスペアリングによるサインイン、コマンドパレット（`Ctrl K`）、ダーク / ライトテーマ、モバイル表示、Android シェル。

## クイックスタート

Go 1.27 以上と Node.js 20 以上が必要です。

```bash
git clone https://github.com/Feianxc/tianshu.git && cd tianshu
bash scripts/build.sh
export WANGLOU_DATA=./data
.build/wanglou import examples/catalog   # 任意：架空のデモデータ
.build/wanglou init                      # 管理者トークンを一度だけ表示
.build/wanglou serve                     # http://127.0.0.1:8787
```

詳しくは [アーキテクチャ](docs/ARCHITECTURE.md) と [デプロイ](docs/DEPLOYMENT.md)（英語）をご覧ください。コンソールの UI は現在簡体字中国語です。

## ライセンス

[MIT](LICENSE) © 2026 Feianxc
