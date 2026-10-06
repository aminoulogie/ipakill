@echo off
rem Link the tablet's Soma tab to your Soma (asks for the server address and recovery code).
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0soma-link.ps1" %*
