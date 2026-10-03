@echo off
rem Get the latest code (GitHub, or a bundle in Downloads) and put it on the tablet.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0update.ps1" %*
