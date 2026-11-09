@echo off
rem Keep a process running: when it exits, log the exit code and start it again 5 seconds later.
rem Usage: keep-running.cmd <log file> <program> [args...]
rem Stop with dev-stop.cmd, which closes these windows before killing the programs.
setlocal
set "LOG=%~1"
shift
set "ARGS="
:collect
if "%~1"=="" goto run
set "ARGS=%ARGS% %1"
shift
goto collect
:run
>> "%LOG%" echo [%date% %time%] start%ARGS%
call %ARGS% 2>> "%LOG%"
>> "%LOG%" echo [%date% %time%] exited with code %errorlevel%, restarting in 5 seconds
rem timeout fails without an interactive console, ping waits reliably (6 pings = 5 seconds)
ping -n 6 127.0.0.1 >nul
goto run
