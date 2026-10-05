@echo off
rem SurfaceFlinger GPU test: watch the tablet while it runs (over Wi-Fi). Restart the tablet after.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0gl-sf.ps1" %*
