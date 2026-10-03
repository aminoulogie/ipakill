@echo off
rem Test whether condor can use the tablet GPU (over Wi-Fi).
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0gl-test.ps1" %*
