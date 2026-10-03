@echo off
rem GPU probe: watch the tablet while it runs (over Wi-Fi).
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0gl-probe.ps1" %*
