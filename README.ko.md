<div align="center">

<img src="docs/images/mark.svg" width="72" alt="Tianshu 로고: 북두칠성의 첫 번째 별 두베를 밝힌 모양">

# Tianshu 天枢

**개인의 서버, 기기, 서비스, 프로젝트, 저장소를 한 화면에서 보는 셀프 호스팅 컨트롤 플레인. 읽기 전용 MCP 서버로 AI 에이전트도 같은 전체 그림을 볼 수 있습니다.**

[English](README.md) · [简体中文](README.zh-CN.md) · [繁體中文](README.zh-TW.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · [Français](README.fr.md) · [Deutsch](README.de.md) · [Português](README.pt-BR.md) · [Русский](README.ru.md)

</div>

![Tianshu 개요 화면: 실시간 별자리 지도, 호스트별 최근 60분 텔레메트리, 처리할 일](docs/images/overview-dark.png)

천추(天枢)는 북두칠성의 첫 번째 별입니다. Tianshu는 웹 콘솔이 내장된 단일 Go 바이너리로, "지금 내가 처리해야 할 일이 있는가?"에 한눈에 답합니다. 보유한 자산의 카탈로그를 유지하고, 실제 상태의 증거를 모아 짧은 할 일 목록으로 정리하며, 같은 읽기 모델을 [Model Context Protocol](https://modelcontextprotocol.io)로 AI 에이전트에 제공합니다.

## 기능

- 작업 공간(개인, 회사, 고객)별 기기, 서비스, 프로젝트, 저장소, 자격 증명 **참조**.
- 실시간 별자리 지도: 작업 공간마다 궤도 하나, 모양과 색으로 상태를 표시하고 호스트가 보고할 때마다 펄스가 중심으로 이동합니다.
- 텔레메트리 띠: 호스트별 최근 60분의 실제 보고. 데이터가 없으면 비어 있고 "정상"으로 표시하지 않습니다.
- 상태, 최신성, 수명 주기, 검수를 분리해 관리하며 오래된 데이터는 정상으로 보지 않습니다.
- 중복이 제거되고 해결되면 자동으로 닫히는 할 일 목록, 하루·일주일·한 달 미루기.
- Linux / Windows 에이전트, 읽기 전용 SSH 수집, HTTP / TCP / TLS 프로브.
- SSH 노출 모니터링(비밀번호 로그인, 실패 로그인, fail2ban, 키 지문).
- MCP 서버(Bearer 토큰, OAuth 2.1), 작업 공간 격리, 비밀 값을 저장하지 않는 설계, 모든 호출의 감사 로그.
- 기기 페어링 로그인, 명령 팔레트(`Ctrl K`), 다크/라이트 테마, 모바일 레이아웃, Android 셸.

## 빠른 시작

Go 1.27 이상, Node.js 20 이상이 필요합니다.

```bash
git clone https://github.com/Feianxc/tianshu.git && cd tianshu
bash scripts/build.sh
export WANGLOU_DATA=./data
.build/wanglou import examples/catalog   # 선택: 가상의 데모 데이터
.build/wanglou init                      # 관리자 토큰을 한 번만 표시
.build/wanglou serve                     # http://127.0.0.1:8787
```

자세한 내용은 [아키텍처](docs/ARCHITECTURE.md)와 [배포](docs/DEPLOYMENT.md)(영문)를 참고하세요. 콘솔 UI는 현재 중국어 간체입니다.

## 라이선스

[MIT](LICENSE) © 2026 Feianxc
