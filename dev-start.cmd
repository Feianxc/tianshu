@echo off
rem Start the Tianshu hub and the local collector. Stop with: dev-stop.cmd
rem Pass --no-browser to skip opening the console in the browser.
rem Both run under scripts\keep-running.cmd, so a crash restarts them after 5 seconds.
cd /d "%~dp0"
set WANGLOU_DATA=.local\data
set WANGLOU_VANTAGE=laptop
set WANGLOU_ADDR=127.0.0.1:8787
rem Errors and restarts go to .local\*.log so a process that dies can be diagnosed later.
start "wanglou-hub" /min cmd /c "scripts\keep-running.cmd .local\hub.log .build\wanglou.exe serve"
ping -n 3 127.0.0.1 >nul
start "wanglou-agent" /min cmd /c "scripts\keep-running.cmd .local\agent.log .build\wanglou-agent.exe run -config .local\agent.yaml"
if /i "%~1"=="--no-browser" goto :eof
start "" http://127.0.0.1:8787
