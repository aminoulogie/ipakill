@echo off
rem Build condor-init and run it on the tablet. Works from cmd or PowerShell: dev.cmd
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0dev.ps1" %*
