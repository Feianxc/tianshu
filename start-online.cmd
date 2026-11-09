@echo off
rem Daily use when the Tianshu hub runs on a server.
rem Starts the local collector (.local\agent.yaml, which points at the server) and a console forward
rem to http://127.0.0.1:8787, then opens the console. Both restart by themselves if they exit.
rem Settings live in .local\online-env.cmd (not committed):
rem   set TIANSHU_URL=https://tianshu.example.com
rem   set TIANSHU_SSH_HOST=my-server
rem Stop with dev-stop.cmd. Pass --no-browser to skip the browser.
rem dev-start.cmd is for local development only: it runs a separate hub on this computer.
cd /d "%~dp0"
if exist .local\online-env.cmd call .local\online-env.cmd
if not defined TIANSHU_URL set TIANSHU_URL=http://127.0.0.1:8787
start "wanglou-console" /min cmd /c "scripts\keep-running.cmd .local\console.log scripts\console-tunnel.cmd"
start "wanglou-agent" /min cmd /c "scripts\keep-running.cmd .local\agent.log .build\wanglou-agent.exe run -config .local\agent.yaml"
if /i "%~1"=="--no-browser" goto :eof
ping -n 4 127.0.0.1 >nul
start "" %TIANSHU_URL%
