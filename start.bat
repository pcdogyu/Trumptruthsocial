@echo off
setlocal
cd /d "%~dp0"

if exist truthsocial.exe (
    truthsocial.exe
) else (
    go run .
)
