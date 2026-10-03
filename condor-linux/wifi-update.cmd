@echo off
rem Put the latest condor-init on the tablet over Wi-Fi (no USB). First time: wifi-update.cmd <tablet IP>
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0wifi-update.ps1" %*
