<div align="center">

<img src="docs/images/mark.svg" width="72" alt="天樞標誌：北斗七星，點亮第一顆天樞">

# 天樞 Tianshu

**自架的個人總控台：一個畫面看清所有伺服器、裝置、服務、專案與程式碼倉庫，並以唯讀的 MCP 服務讓 AI Agent 看到同樣的全局。**

[English](README.md) · [简体中文](README.zh-CN.md) · [繁體中文](README.zh-TW.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · [Français](README.fr.md) · [Deutsch](README.de.md) · [Português](README.pt-BR.md) · [Русский](README.ru.md)

</div>

![天樞總覽：即時星圖、每台主機最近 60 分鐘的觀測帶與待處理事項](docs/images/overview-dark.png)

天樞是北斗七星的第一顆星。這個專案是單一 Go 執行檔，內嵌網頁控制台，只回答一件事：**現在有沒有事情需要我處理？** 它維護一份資產目錄、收集每項資產真實狀態的證據、彙整成簡短的待處理清單，並透過 [Model Context Protocol](https://modelcontextprotocol.io) 把同一份唯讀資料提供給 AI Agent。

## 功能

- 依空間（個人、公司、客戶）區分的裝置、服務、專案、倉庫與憑證**參照**。
- 即時星圖：每個空間一圈軌道，形狀與顏色代表健康狀態；主機每次回報都有光點回到中心。
- 觀測帶：每台主機最近 60 分鐘的真實回報，沒有資料就顯示空白，不會顯示為正常。
- 健康、新鮮度、生命週期、驗收分開記錄；過期資料不算健康。
- 自動去重、問題解決後自動關閉的待處理清單，可延後一天、一週或一個月。
- Linux 與 Windows 採集器，或唯讀 SSH 採集；HTTP、TCP、TLS 探測。
- SSH 登入入口監控：密碼登入、失敗登入次數、fail2ban、金鑰指紋，新增金鑰即為緊急事項。
- MCP 服務（Bearer 權杖與 OAuth 2.1），空間隔離、只存憑證位置不存秘密、每次呼叫都有稽核紀錄。
- 裝置配對登入、命令面板（`Ctrl K`）、深色與淺色主題、手機版面與 Android 殼程式。

## 快速開始

需要 Go 1.27+ 與 Node.js 20+。

```bash
git clone https://github.com/Feianxc/tianshu.git && cd tianshu
bash scripts/build.sh
export WANGLOU_DATA=./data
.build/wanglou import examples/catalog   # 選用：虛構的示範資料
.build/wanglou init                      # 產生管理員權杖，只顯示一次
.build/wanglou serve                     # http://127.0.0.1:8787
```

詳細說明請見 [架構](docs/ARCHITECTURE.md) 與 [部署](docs/DEPLOYMENT.md)（英文），或[簡體中文文件](docs/zh-CN/ARCHITECTURE.md)。控制台介面目前為簡體中文。

## 授權

[MIT](LICENSE) © 2026 Feianxc
