@echo off
rem Forward the Tianshu console on the server to http://127.0.0.1:8787 on this computer.
rem Useful for MCP clients configured with the local address and for USB debugging of the phone app
rem (adb reverse tcp:8787 tcp:8787 lands on this forward).
rem TIANSHU_SSH_HOST is an alias from your ~/.ssh/config; set it in .local\online-env.cmd.
if not defined TIANSHU_SSH_HOST (
  echo TIANSHU_SSH_HOST is not set. Put "set TIANSHU_SSH_HOST=<ssh alias>" in .local\online-env.cmd 1>&2
  exit /b 2
)
C:\Windows\System32\OpenSSH\ssh.exe -N -o BatchMode=yes -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 -o ServerAliveCountMax=3 -L 127.0.0.1:8787:127.0.0.1:8787 %TIANSHU_SSH_HOST%
