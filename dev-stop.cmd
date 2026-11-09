@echo off
rem Stop the local collector and hub. Data stays in .local\data.
rem Close the keep-running windows first, otherwise they would start the programs again.
taskkill /FI "WINDOWTITLE eq wanglou-agent*" /T /F >nul 2>&1
taskkill /FI "WINDOWTITLE eq wanglou-hub*" /T /F >nul 2>&1
taskkill /FI "WINDOWTITLE eq wanglou-console*" /T /F >nul 2>&1
taskkill /IM wanglou-agent.exe /F >nul 2>&1
taskkill /IM wanglou.exe /F >nul 2>&1
echo stopped
