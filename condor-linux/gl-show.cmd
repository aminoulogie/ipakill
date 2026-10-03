@echo off
rem Visual GPU test: watch the tablet while it runs (over Wi-Fi).
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0gl-show.ps1" %*
